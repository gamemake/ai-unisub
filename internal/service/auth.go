package service

import (
	"ai-unisub/internal/common"
	"ai-unisub/internal/database"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

type AuthMethod int
type Principal struct {
	User     *database.PersistedUser
	Account  *database.PersistedAccount
	APIKeyID string
	Method   AuthMethod
}
type AuthService interface {
	Principal(context.Context) (Principal, bool)
	SessionPrincipal(*http.Request) (Principal, bool)
	EnsureAdmin(string, string) error
	CreateSession(*database.PersistedUser) (string, error)
	DeleteSession(string)
	SetSessionCookie(http.ResponseWriter, string)
	ClearSessionCookie(http.ResponseWriter, string)
}
type authService struct {
	s        *Service
	mu       sync.RWMutex
	sessions map[string]session
}
type session struct {
	UserID    string
	ExpiresAt time.Time
}
type principalKey struct{}

func (a *authService) Principal(c context.Context) (Principal, bool) {
	p, ok := c.Value(principalKey{}).(Principal)
	return p, ok
}

// SessionPrincipal validates the browser session without applying route
// middleware. This is needed by public page routes that redirect based on
// login state.
func (a *authService) SessionPrincipal(r *http.Request) (Principal, bool) {
	return a.session(r)
}
func PrincipalFromContext(c context.Context) (Principal, bool) {
	p, ok := c.Value(principalKey{}).(Principal)
	return p, ok
}
func (a *authService) EnsureAdmin(n, p string) error {
	if n == "" || p == "" {
		return errors.New("admin username and password are required")
	}
	xs, e := a.s.db.ListUsers()
	if e != nil {
		return e
	}
	for _, u := range xs {
		if u.Name == n {
			if u.PasswordHash == "" {
				u.PasswordHash = hashPassword(p)
				u.UpdatedAt = time.Now().UTC()
				return a.s.db.SaveUser(&u)
			}
			return nil
		}
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	now := time.Now().UTC()
	return a.s.db.SaveUser(&database.PersistedUser{ID: hex.EncodeToString(id[:]), Name: n, Role: database.UserRoleAdmin, Enabled: true, PasswordHash: hashPassword(p), CreatedAt: now, UpdatedAt: now})
}
func (a *authService) CreateSession(user *database.PersistedUser) (string, error) {
	if user == nil || user.ID == "" {
		return "", errors.New("user is required")
	}
	users, err := a.s.db.ListUsers()
	if err != nil {
		return "", err
	}
	valid := false
	for _, candidate := range users {
		if candidate.ID == user.ID {
			valid = true
			break
		}
	}
	if !valid {
		return "", errors.New("user not found")
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	ttl := a.s.cfg.SessionTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	a.mu.Lock()
	a.sessions[hex.EncodeToString(token[:])] = session{UserID: user.ID, ExpiresAt: time.Now().Add(ttl)}
	a.mu.Unlock()
	return hex.EncodeToString(token[:]), nil
}
func (a *authService) DeleteSession(token string) {
	a.mu.Lock()
	delete(a.sessions, token)
	a.mu.Unlock()
}
func (a *authService) SetSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: "session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(a.sessionTTL().Seconds())})
}
func (a *authService) ClearSessionCookie(w http.ResponseWriter, token string) {
	if token != "" {
		a.DeleteSession(token)
	}
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", HttpOnly: true, MaxAge: -1, SameSite: http.SameSiteLaxMode})
}
func (a *authService) sessionTTL() time.Duration {
	if a.s.cfg.SessionTTL > 0 {
		return a.s.cfg.SessionTTL
	}
	return 24 * time.Hour
}
func (a *authService) session(r *http.Request) (Principal, bool) {
	c, e := r.Cookie("session")
	if e != nil {
		return Principal{}, false
	}
	a.mu.Lock()
	value, ok := a.sessions[c.Value]
	if ok && !value.ExpiresAt.After(time.Now()) {
		delete(a.sessions, c.Value)
		ok = false
	}
	a.mu.Unlock()
	if !ok {
		return Principal{}, false
	}
	users, err := a.s.db.ListUsers()
	if err != nil {
		return Principal{}, false
	}
	for _, u := range users {
		if u.ID == value.UserID && u.Enabled {
			return Principal{User: &u, Method: AuthMethod(AuthSession)}, true
		}
	}
	a.DeleteSession(c.Value)
	return Principal{}, false
}

func (a *authService) apiKey(r *http.Request) (Principal, int, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return Principal{}, http.StatusUnauthorized, false
	}
	keys, err := a.s.db.ListAPIKeys("")
	if err != nil {
		return Principal{}, http.StatusInternalServerError, false
	}
	var key database.PersistedAPIKey
	for _, candidate := range keys {
		if subtle.ConstantTimeCompare([]byte(candidate.Key), []byte(parts[1])) == 1 {
			if candidate.ValidSeconds > 0 && !candidate.CreatedAt.Add(time.Duration(candidate.ValidSeconds)*time.Second).After(time.Now().UTC()) {
				return Principal{}, http.StatusUnauthorized, false
			}
			key = candidate
			break
		}
	}
	if key.ID == "" {
		return Principal{}, http.StatusUnauthorized, false
	}
	users, err := a.s.db.ListUsers()
	if err != nil {
		return Principal{}, http.StatusInternalServerError, false
	}
	var user database.PersistedUser
	for _, candidate := range users {
		if candidate.ID == key.UserID && candidate.Enabled {
			user = candidate
			break
		}
	}
	if user.ID == "" {
		return Principal{}, http.StatusForbidden, false
	}
	accounts, err := a.s.db.ListAccounts()
	if err != nil {
		return Principal{}, http.StatusInternalServerError, false
	}
	var account database.PersistedAccount
	for _, candidate := range accounts {
		if candidate.ID == key.AccountID {
			account = candidate
			break
		}
	}
	if account.ID == "" {
		return Principal{}, http.StatusForbidden, false
	}
	return Principal{User: &user, Account: &account, APIKeyID: key.ID, Method: AuthMethod(AuthAPIKey)}, http.StatusOK, true
}

func (a *authService) middleware(mode AuthMode, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == AuthNone {
			next.ServeHTTP(w, r)
			return
		}
		status := http.StatusUnauthorized
		var p Principal
		var ok bool
		if mode == AuthAPIKey {
			p, status, ok = a.apiKey(r)
		} else {
			p, ok = a.session(r)
		}
		if !ok {
			if mode == AuthAPIKey || strings.HasPrefix(r.URL.Path, "/api/") {
				message := common.MessageUnauthorized
				if status == http.StatusForbidden {
					message = common.MessageForbidden
				}
				if status == http.StatusInternalServerError {
					message = common.MessageInternalServerError
				}
				writeError(w, status, message)
			} else {
				http.Redirect(w, r, "/login", 303)
			}
			return
		}
		if mode == AuthSessionAdmin && p.User.Role != database.UserRoleAdmin {
			writeError(w, 403, common.MessageForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}
func writeError(w http.ResponseWriter, status int, msg string) {
	common.WriteError(w, status, msg)
}

const passwordIterations = 120000

func hashPassword(password string) string {
	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		panic(err)
	}
	sum := passwordDigest(salt[:], password)
	return "sha256$" + hex.EncodeToString(salt[:]) + "$" + hex.EncodeToString(sum[:])
}
func passwordDigest(salt []byte, password string) [32]byte {
	sum := sha256.Sum256(append(append([]byte(nil), salt...), []byte(password)...))
	for i := 1; i < passwordIterations; i++ {
		sum = sha256.Sum256(sum[:])
	}
	return sum
}
func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 3 || parts[0] != "sha256" {
		return false
	}
	salt, e := hex.DecodeString(parts[1])
	if e != nil {
		return false
	}
	sum := passwordDigest(salt, password)
	want, e := hex.DecodeString(parts[2])
	return e == nil && subtle.ConstantTimeCompare(sum[:], want) == 1
}
