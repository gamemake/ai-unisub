package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

type OAuthManager struct {
	adapters map[string]OAuthAdapter
	store    CredentialStore
	mu       sync.Mutex
	sessions map[string]OAuthSession
	refresh  map[string]*sync.Mutex
}

const oauthSessionTTL = 10 * time.Minute

func NewManager(store CredentialStore) *OAuthManager {
	return &OAuthManager{adapters: make(map[string]OAuthAdapter), store: store, sessions: make(map[string]OAuthSession), refresh: make(map[string]*sync.Mutex)}
}

// NewOAuthManager is the explicit constructor name used by integrations.
func NewOAuthManager(store CredentialStore) *OAuthManager { return NewManager(store) }

func (m *OAuthManager) Register(adapter OAuthAdapter) error {
	if m == nil || adapter == nil || adapter.Service() == "" {
		return errors.New("invalid oauth adapter")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.adapters[adapter.Service()]; ok {
		return fmt.Errorf("oauth service %q is already registered", adapter.Service())
	}
	m.adapters[adapter.Service()] = adapter
	return nil
}

func (m *OAuthManager) adapter(service string) (OAuthAdapter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.adapters[service]
	if !ok {
		return nil, fmt.Errorf("oauth service %q is not registered", service)
	}
	return a, nil
}

func (m *OAuthManager) Start(ctx context.Context, service, subjectID, redirectURI string) (*StartResult, error) {
	a, err := m.adapter(service)
	if err != nil {
		return nil, err
	}
	session := OAuthSession{ID: randomID(), Service: service, SubjectID: subjectID, RedirectURI: redirectURI, ExpiresAt: time.Now().Add(oauthSessionTTL)}
	result := &StartResult{SessionID: session.ID, ExpiresAt: session.ExpiresAt}
	switch adapter := a.(type) {
	case PKCEAdapter:
		session.State = randomID()
		session.CodeVerifier = randomID() + randomID()
		built, err := adapter.BuildAuthorizationURL(ctx, AuthorizationInput{Service: service, State: session.State, CodeVerifier: session.CodeVerifier, RedirectURI: redirectURI})
		if err != nil {
			return nil, err
		}
		result.AuthorizationURL = built.AuthorizationURL
	case DeviceAdapter:
		device, err := adapter.StartDeviceAuthorization(ctx, DeviceStartInput{Service: service})
		if err != nil {
			return nil, err
		}
		session.DeviceCode = device.DeviceCode
		if !device.ExpiresAt.IsZero() {
			session.ExpiresAt = device.ExpiresAt
			result.ExpiresAt = session.ExpiresAt
		}
		result.UserCode, result.VerificationURI = device.UserCode, device.VerificationURI
		result.AuthorizationURL = device.VerificationURI
	default:
		return nil, ErrUnsupportedFlow
	}
	m.mu.Lock()
	m.sessions[session.ID] = session
	m.mu.Unlock()
	return result, nil
}

func (m *OAuthManager) session(id string, consume bool) (OAuthSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return OAuthSession{}, ErrSessionNotFound
	}
	if time.Now().After(s.ExpiresAt) {
		delete(m.sessions, id)
		return OAuthSession{}, ErrSessionExpired
	}
	if consume {
		delete(m.sessions, id)
	}
	return s, nil
}

func (m *OAuthManager) Complete(ctx context.Context, sessionID, code, state string) (*OAuthCredential, error) {
	s, err := m.session(sessionID, false)
	if err != nil {
		return nil, err
	}
	if s.State == "" {
		return nil, ErrUnsupportedFlow
	}
	if state != s.State {
		return nil, ErrStateMismatch
	}
	if _, err := m.session(sessionID, true); err != nil {
		return nil, err
	}
	a, err := m.adapter(s.Service)
	if err != nil {
		return nil, err
	}
	adapter, ok := a.(PKCEAdapter)
	if !ok {
		return nil, ErrUnsupportedFlow
	}
	result, err := adapter.Exchange(ctx, code, state, s.CodeVerifier, s.RedirectURI)
	if err != nil {
		return nil, err
	}
	if result == nil || result.AccessToken == "" {
		return nil, errors.New("oauth exchange returned no access token")
	}
	return result, nil
}

// SessionForState returns the short-lived session bound to state. It is used
// by web callback handlers, while the manager still performs the authoritative
// state and expiry checks in Complete.
func (m *OAuthManager) SessionForState(state string) (OAuthSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, session := range m.sessions {
		if session.State == state {
			if time.Now().After(session.ExpiresAt) {
				delete(m.sessions, id)
				return OAuthSession{}, ErrSessionExpired
			}
			return session, nil
		}
	}
	return OAuthSession{}, ErrSessionNotFound
}

// SessionForSubjectState is the stricter lookup variant for callers that
// already know the expected service and subject. It prevents a caller from
// accidentally accepting a session belonging to another user.
func (m *OAuthManager) SessionForSubjectState(service, subjectID, state string) (string, error) {
	session, err := m.SessionForState(state)
	if err != nil {
		return "", err
	}
	if session.Service != service || session.SubjectID != subjectID {
		return "", ErrSessionNotFound
	}
	return session.ID, nil
}

// SessionForSubject returns a live session only when it belongs to the
// expected service and subject. It is used by authenticated Web handlers
// before operations that address a session by ID.
func (m *OAuthManager) SessionForSubject(sessionID, service, subjectID string) (OAuthSession, error) {
	session, err := m.session(sessionID, false)
	if err != nil {
		return OAuthSession{}, err
	}
	if session.Service != service || session.SubjectID != subjectID {
		return OAuthSession{}, ErrSessionNotFound
	}
	return session, nil
}

// DiscardSession consumes a pending session without contacting the upstream
// service. Web handlers use it when the authorization server returns an error.
func (m *OAuthManager) DiscardSession(sessionID string) error {
	_, err := m.session(sessionID, true)
	return err
}

func (m *OAuthManager) Poll(ctx context.Context, sessionID string) (*OAuthCredential, error) {
	s, err := m.session(sessionID, false)
	if err != nil {
		return nil, err
	}
	a, err := m.adapter(s.Service)
	if err != nil {
		return nil, err
	}
	adapter, ok := a.(DeviceAdapter)
	if !ok {
		return nil, ErrUnsupportedFlow
	}
	result, err := adapter.PollDeviceToken(ctx, s.DeviceCode)
	if err != nil {
		return nil, err
	}
	_, _ = m.session(sessionID, true)
	return result, nil
}

func (m *OAuthManager) Refresh(ctx context.Context, service string, credential *OAuthCredential) (*OAuthCredential, error) {
	a, err := m.adapter(service)
	if err != nil {
		return nil, err
	}
	if credential == nil {
		return nil, errors.New("credential is nil")
	}
	refreshed, err := a.Refresh(ctx, credential)
	if err != nil {
		return nil, err
	}
	if refreshed == nil || refreshed.AccessToken == "" {
		return nil, errors.New("oauth refresh returned no access token")
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = credential.RefreshToken
	}
	if refreshed.TokenType == "" {
		refreshed.TokenType = credential.TokenType
	}
	if refreshed.AccountID == "" {
		refreshed.AccountID = credential.AccountID
	}
	if refreshed.AccountName == "" {
		refreshed.AccountName = credential.AccountName
	}
	if refreshed.Email == "" {
		refreshed.Email = credential.Email
	}
	return refreshed, nil
}

func (m *OAuthManager) GetValidAccessToken(ctx context.Context, service, credentialID string) (string, error) {
	if m == nil || m.store == nil {
		return "", errors.New("credential store is not configured")
	}
	raw, err := m.store.LoadCredential(credentialID)
	if err != nil {
		return "", err
	}
	var credential OAuthCredential
	if err := json.Unmarshal(raw, &credential); err != nil {
		return "", err
	}
	if credential.AccessToken == "" {
		return "", errors.New("credential has no access token")
	}
	if credential.ExpiresAt.IsZero() || time.Now().Before(credential.ExpiresAt.Add(-60*time.Second)) {
		return credential.AccessToken, nil
	}
	m.mu.Lock()
	lock := m.refresh[credentialID]
	if lock == nil {
		lock = &sync.Mutex{}
		m.refresh[credentialID] = lock
	}
	m.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	raw, err = m.store.LoadCredential(credentialID)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(raw, &credential); err != nil {
		return "", err
	}
	if time.Now().Before(credential.ExpiresAt.Add(-60 * time.Second)) {
		return credential.AccessToken, nil
	}
	refreshed, err := m.Refresh(ctx, service, &credential)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(refreshed)
	if err != nil {
		return "", err
	}
	if err := m.store.SaveCredential(credentialID, encoded); err != nil {
		return "", err
	}
	return refreshed.AccessToken, nil
}

func (m *OAuthManager) Revoke(ctx context.Context, service string, credential *OAuthCredential) error {
	a, err := m.adapter(service)
	if err != nil {
		return err
	}
	r, ok := a.(RevocableAdapter)
	if !ok {
		return errors.New("oauth service does not support revoke")
	}
	return r.Revoke(ctx, credential)
}

func randomID() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
