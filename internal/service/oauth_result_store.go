package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"ai-unisub/internal/oauth"
)

type OAuthResult struct {
	SessionID  string
	SubjectID  string
	Service    string
	Credential oauth.OAuthCredential
}

// OAuthResultStore stores short-lived OAuth results in process memory.
// It is appropriate for the current single-process Web Service. Results are
// intentionally lost when the process exits.
type OAuthResultStore struct {
	mu     sync.Mutex
	ttl    time.Duration
	values map[string]oauthResultValue
}

const oauthResultTTL = 10 * time.Minute

type oauthResultValue struct {
	subjectID string
	result    OAuthResult
	expiresAt time.Time
}

func NewOAuthResultStore() *OAuthResultStore {
	return &OAuthResultStore{ttl: oauthResultTTL, values: map[string]oauthResultValue{}}
}

func (s *OAuthResultStore) Put(result OAuthResult) (string, error) {
	if result.SubjectID == "" || result.Service == "" {
		return "", errors.New("subject and OAuth service are required")
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw[:])
	s.mu.Lock()
	for key, value := range s.values {
		if !value.expiresAt.After(time.Now()) {
			delete(s.values, key)
		}
	}
	s.values[id] = oauthResultValue{subjectID: result.SubjectID, result: result, expiresAt: time.Now().Add(s.ttl)}
	s.mu.Unlock()
	return id, nil
}

// FindSession only reveals a completed result ID to the original session owner.
func (s *OAuthResultStore) FindSession(sessionID, name, subjectID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, value := range s.values {
		if !value.expiresAt.After(time.Now()) {
			delete(s.values, id)
			continue
		}
		if sessionID != "" && value.result.SessionID == sessionID && value.result.Service == name && value.subjectID == subjectID {
			return id, true
		}
	}
	return "", false
}

func (s *OAuthResultStore) Take(id, subjectID string) (OAuthResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[id]
	if !ok || value.subjectID != subjectID || !value.expiresAt.After(time.Now()) {
		if ok && !value.expiresAt.After(time.Now()) {
			delete(s.values, id)
		}
		return OAuthResult{}, errors.New("oauth result not found")
	}
	delete(s.values, id)
	return value.result, nil
}
