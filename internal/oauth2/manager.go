package oauth2

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"ai-unisub/internal/database"
	"ai-unisub/internal/proxy2"
)

const oauthSessionTTL = 10 * time.Minute

type oauthManager struct {
	db    database.Database
	proxy proxy2.ProxyManager

	mu       sync.Mutex
	adapters map[string]OAuthAdapter
	sessions map[string]OAuthSession
}

// NewManager 创建负责 OAuth 授权会话和凭据生命周期的管理器。
func NewManager(db database.Database, proxy proxy2.ProxyManager) OAuthManager {
	m := &oauthManager{
		db:       db,
		proxy:    proxy,
		adapters: make(map[string]OAuthAdapter),
		sessions: make(map[string]OAuthSession),
	}
	m.adapters[OAuthServiceXAI] = NewXAI(XAIConfig{})
	m.adapters[OAuthServiceOpenAI] = NewOpenAI(OpenAIConfig{})
	m.adapters[OAuthServiceAnthropic] = NewAnthropic(AnthropicConfig{})
	m.adapters[OAuthServiceDummy] = NewDummyAdapter()
	return m
}

func (m *oauthManager) Register(adapter OAuthAdapter) error {
	if adapter == nil || adapter.Service() == "" {
		return errors.New("invalid oauth adapter")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.adapters[adapter.Service()]; exists {
		return fmt.Errorf("oauth service %q is already registered", adapter.Service())
	}
	m.adapters[adapter.Service()] = adapter
	return nil
}

func (m *oauthManager) Start(ctx context.Context, service, subjectID, redirectURI string, proxyGroup int) (*StartResult, error) {
	adapter, err := m.adapter(service)
	if err != nil {
		return nil, err
	}
	client, err := m.httpClient(service, proxyGroup)
	if err != nil {
		return nil, err
	}
	sessionID, err := randomID()
	if err != nil {
		return nil, fmt.Errorf("generate oauth session ID: %w", err)
	}
	session := OAuthSession{
		ID:          sessionID,
		Service:     service,
		SubjectID:   subjectID,
		RedirectURI: redirectURI,
		HTTPClient:  client,
		ExpiresAt:   time.Now().Add(oauthSessionTTL),
	}
	result := &StartResult{SessionID: session.ID, ExpiresAt: session.ExpiresAt}

	switch flow := adapter.(type) {
	case PKCEAdapter:
		if redirectURI == "" {
			return nil, errors.New("redirect URI is required for PKCE authorization")
		}
		session.State, err = randomID()
		if err != nil {
			return nil, fmt.Errorf("generate oauth state: %w", err)
		}
		first, err := randomID()
		if err != nil {
			return nil, fmt.Errorf("generate PKCE verifier: %w", err)
		}
		second, err := randomID()
		if err != nil {
			return nil, fmt.Errorf("generate PKCE verifier: %w", err)
		}
		session.CodeVerifier = first + second
		built, err := flow.BuildAuthorizationURL(ctx, AuthorizationInput{
			HTTPClient: client, Service: service, State: session.State,
			CodeVerifier: session.CodeVerifier, RedirectURI: redirectURI,
		})
		if err != nil {
			return nil, err
		}
		if built.AuthorizationURL == "" {
			return nil, errors.New("oauth adapter returned no authorization URL")
		}
		result.AuthorizationURL = built.AuthorizationURL
		if !built.ExpiresAt.IsZero() && built.ExpiresAt.Before(session.ExpiresAt) {
			session.ExpiresAt = built.ExpiresAt
			result.ExpiresAt = built.ExpiresAt
		}
	case DeviceAdapter:
		device, err := flow.StartDeviceAuthorization(ctx, DeviceStartInput{HTTPClient: client, Service: service})
		if err != nil {
			return nil, err
		}
		if device.DeviceCode == "" {
			return nil, errors.New("oauth adapter returned no device code")
		}
		session.DeviceCode = device.DeviceCode
		if !device.ExpiresAt.IsZero() {
			session.ExpiresAt = device.ExpiresAt
			result.ExpiresAt = device.ExpiresAt
		}
		result.AuthorizationURL = device.VerificationURI
		result.UserCode = device.UserCode
		result.VerificationURI = device.VerificationURI
	default:
		return nil, ErrUnsupportedFlow
	}

	m.mu.Lock()
	m.sessions[session.ID] = session
	m.mu.Unlock()
	return result, nil
}

func (m *oauthManager) Complete(ctx context.Context, sessionID, code, state string) (*OAuthCredential, error) {
	session, err := m.session(sessionID, false)
	if err != nil {
		return nil, err
	}
	if session.State == "" {
		return nil, ErrUnsupportedFlow
	}
	if state != session.State {
		return nil, ErrStateMismatch
	}
	adapter, err := m.adapter(session.Service)
	if err != nil {
		return nil, err
	}
	flow, ok := adapter.(PKCEAdapter)
	if !ok {
		return nil, ErrUnsupportedFlow
	}
	if _, err := m.session(sessionID, true); err != nil {
		return nil, err
	}
	credential, err := flow.Exchange(ctx, code, state, session.CodeVerifier, session.RedirectURI, session.HTTPClient)
	if err != nil {
		return nil, err
	}
	return validCredential(credential, "exchange")
}

func (m *oauthManager) SessionForState(state string) (OAuthSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, session := range m.sessions {
		if session.State != state {
			continue
		}
		if sessionExpired(session) {
			delete(m.sessions, id)
			return OAuthSession{}, ErrSessionExpired
		}
		return session, nil
	}
	return OAuthSession{}, ErrSessionNotFound
}

func (m *oauthManager) SessionForSubjectState(service, subjectID, state string) (string, error) {
	session, err := m.SessionForState(state)
	if err != nil {
		return "", err
	}
	if session.Service != service || session.SubjectID != subjectID {
		return "", ErrSessionNotFound
	}
	return session.ID, nil
}

func (m *oauthManager) SessionForSubject(sessionID, service, subjectID string) (OAuthSession, error) {
	session, err := m.session(sessionID, false)
	if err != nil {
		return OAuthSession{}, err
	}
	if session.Service != service || session.SubjectID != subjectID {
		return OAuthSession{}, ErrSessionNotFound
	}
	return session, nil
}

func (m *oauthManager) DiscardSession(sessionID string) error {
	_, err := m.session(sessionID, true)
	return err
}

func (m *oauthManager) Poll(ctx context.Context, sessionID string) (*OAuthCredential, error) {
	session, err := m.session(sessionID, false)
	if err != nil {
		return nil, err
	}
	adapter, err := m.adapter(session.Service)
	if err != nil {
		return nil, err
	}
	flow, ok := adapter.(DeviceAdapter)
	if !ok || session.DeviceCode == "" {
		return nil, ErrUnsupportedFlow
	}
	credential, err := flow.PollDeviceToken(ctx, session.DeviceCode, session.HTTPClient)
	if err != nil {
		return nil, err
	}
	credential, err = validCredential(credential, "device authorization")
	if err != nil {
		return nil, err
	}
	if _, err := m.session(sessionID, true); err != nil {
		return nil, err
	}
	return credential, nil
}

func (m *oauthManager) Refresh(ctx context.Context, service string, credential *OAuthCredential, proxyGroup int) (*OAuthCredential, error) {
	if credential == nil {
		return nil, errors.New("credential is nil")
	}
	adapter, err := m.adapter(service)
	if err != nil {
		return nil, err
	}
	client, err := m.httpClient(service, proxyGroup)
	if err != nil {
		return nil, err
	}
	refreshed, err := adapter.Refresh(ctx, credential, client)
	if err != nil {
		return nil, err
	}
	refreshed, err = validCredential(refreshed, "refresh")
	if err != nil {
		return nil, err
	}
	refreshed.RefreshToken = cmp.Or(refreshed.RefreshToken, credential.RefreshToken)
	refreshed.TokenType = cmp.Or(refreshed.TokenType, credential.TokenType)
	refreshed.AccountID = cmp.Or(refreshed.AccountID, credential.AccountID)
	refreshed.AccountName = cmp.Or(refreshed.AccountName, credential.AccountName)
	refreshed.Email = cmp.Or(refreshed.Email, credential.Email)
	return refreshed, nil
}

func (m *oauthManager) Revoke(ctx context.Context, service string, credential *OAuthCredential, client *http.Client) error {
	if credential == nil {
		return errors.New("credential is nil")
	}
	adapter, err := m.adapter(service)
	if err != nil {
		return err
	}
	revocable, ok := adapter.(RevocableAdapter)
	if !ok {
		return errors.New("oauth service does not support revoke")
	}
	loggedClient, err := m.revokeClient(service, client)
	if err != nil {
		return err
	}
	return revocable.Revoke(ctx, credential, loggedClient)
}

func (m *oauthManager) adapter(service string) (OAuthAdapter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	adapter, ok := m.adapters[service]
	if !ok {
		return nil, fmt.Errorf("oauth service %q is not registered", service)
	}
	return adapter, nil
}

func (m *oauthManager) session(id string, consume bool) (OAuthSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return OAuthSession{}, ErrSessionNotFound
	}
	if sessionExpired(session) {
		delete(m.sessions, id)
		return OAuthSession{}, ErrSessionExpired
	}
	if consume {
		delete(m.sessions, id)
	}
	return session, nil
}

func (m *oauthManager) httpClient(service string, proxyGroup int) (*http.Client, error) {
	if proxyGroup < 0 {
		return nil, errors.New("proxy group must not be negative")
	}
	if m.proxy == nil {
		return nil, errors.New("proxy manager is not configured")
	}
	return &http.Client{Transport: &proxyTransport{manager: m.proxy, groupID: proxyGroup, app: "oauth:" + service}, Timeout: 30 * time.Second}, nil
}

func (m *oauthManager) revokeClient(service string, supplied *http.Client) (*http.Client, error) {
	if supplied == nil {
		return m.httpClient(service, 0)
	}
	if m.db == nil {
		return nil, errors.New("oauth database is not configured")
	}
	client := *supplied
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = &recordingTransport{base: transport, db: m.db, app: "oauth:" + service}
	return &client, nil
}

func sessionExpired(session OAuthSession) bool {
	return session.ExpiresAt.IsZero() || !time.Now().Before(session.ExpiresAt)
}

func validCredential(credential *OAuthCredential, operation string) (*OAuthCredential, error) {
	if credential == nil || credential.AccessToken == "" {
		return nil, fmt.Errorf("oauth %s returned no access token", operation)
	}
	return credential, nil
}

func randomID() (string, error) {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
