package service

import (
	"ai-unisub/internal/common"
	"ai-unisub/internal/database"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// APIModule exposes the authenticated management JSON API described by
// docs/service-api.md. Provider request forwarding remains the responsibility
// of the gateway module.
type APIModule struct{}

func NewAPIModule() *APIModule    { return &APIModule{} }
func (m *APIModule) Name() string { return "api" }
func (m *APIModule) Close() error { return nil }

func (m *APIModule) Init(ctx ModuleContext) error {
	ctx.HandleFunc("/api/", RouteOptions{Auth: AuthSession, Name: "api"}, func(w http.ResponseWriter, r *http.Request) {
		m.handle(ctx, w, r)
	})
	return nil
}

func (m *APIModule) handle(ctx ModuleContext, w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/api")
	if path == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch parts[0] {
	case "me":
		if len(parts) == 1 && r.Method == http.MethodGet {
			m.me(w, r)
			return
		}
	case "password":
		if len(parts) == 1 && r.Method == http.MethodPost {
			m.password(ctx, w, r)
			return
		}
	case "users":
		m.users(ctx, w, r, parts)
		return
	case "providers":
		m.providers(ctx, w, r, parts)
		return
	case "keys":
		m.keys(ctx, w, r, parts)
		return
	case "calls":
		if len(parts) == 1 && r.Method == http.MethodGet {
			m.calls(ctx, w, r)
			return
		}
	}
	http.NotFound(w, r)
}

func currentUser(r *http.Request) (*database.PersistedUser, bool) {
	p, ok := PrincipalFromContext(r.Context())
	return p.User, ok && p.User != nil
}

func isAdmin(r *http.Request) bool {
	u, ok := currentUser(r)
	return ok && u.Role == database.UserRoleAdmin
}

func (m *APIModule) me(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r)
	if !ok {
		common.WriteError(w, http.StatusUnauthorized, common.MessageUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": u.ID, "name": u.Name, "role": u.Role, "server_version": Version})
}

func (m *APIModule) password(ctx ModuleContext, w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r)
	if !ok {
		common.WriteError(w, http.StatusUnauthorized, common.MessageUnauthorized)
		return
	}
	var input struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.NewPassword) < 8 || input.OldPassword == "" {
		common.WriteError(w, http.StatusBadRequest, common.MessageInvalidPassword)
		return
	}
	if !verifyPassword(u.PasswordHash, input.OldPassword) {
		common.WriteError(w, http.StatusBadRequest, common.MessageOldPasswordIncorrect)
		return
	}
	u.PasswordHash = hashPassword(input.NewPassword)
	u.UpdatedAt = time.Now().UTC()
	if err := ctx.Database().SaveUser(u); err != nil {
		common.WriteError(w, http.StatusInternalServerError, common.MessageCouldNotSavePassword)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (m *APIModule) users(ctx ModuleContext, w http.ResponseWriter, r *http.Request, parts []string) {
	if !isAdmin(r) {
		common.WriteError(w, http.StatusForbidden, common.MessageForbidden)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		users, err := ctx.Database().ListUsers()
		if err != nil {
			common.WriteError(w, 500, common.MessageCouldNotListUsers)
			return
		}
		items := make([]map[string]any, 0, len(users))
		for _, u := range users {
			items = append(items, publicUser(u))
		}
		writeJSON(w, 200, map[string]any{"items": items, "total": len(items)})
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPost {
		m.createUser(ctx, w, r)
		return
	}
	if len(parts) != 2 || parts[1] == "" {
		if len(parts) == 3 && parts[1] != "" && parts[2] == "password" && r.Method == http.MethodPost {
			m.resetUserPassword(ctx, w, r, parts[1])
			return
		}
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPut {
		m.updateUser(ctx, w, r, parts[1])
		return
	}
	if r.Method == http.MethodDelete {
		m.deleteUser(ctx, w, r, parts[1])
		return
	}
	http.NotFound(w, r)
}

func (m *APIModule) resetUserPassword(ctx ModuleContext, w http.ResponseWriter, r *http.Request, id string) {
	var input struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.Password) < 8 {
		common.WriteError(w, http.StatusBadRequest, common.MessagePasswordTooShort)
		return
	}
	users, err := ctx.Database().ListUsers()
	if err != nil {
		common.WriteError(w, http.StatusInternalServerError, common.MessageCouldNotListUsers)
		return
	}
	current, _ := currentUser(r)
	for i := range users {
		if users[i].ID != id {
			continue
		}
		if current != nil && current.ID == id {
			common.WriteError(w, http.StatusBadRequest, common.MessageCannotResetCurrentUser)
			return
		}
		users[i].PasswordHash = hashPassword(input.Password)
		users[i].UpdatedAt = time.Now().UTC()
		if err := ctx.Database().SaveUser(&users[i]); err != nil {
			common.WriteError(w, http.StatusInternalServerError, common.MessageCouldNotSaveUser)
			return
		}
		writeJSON(w, http.StatusOK, publicUser(users[i]))
		return
	}
	common.WriteError(w, http.StatusNotFound, common.MessageUserNotFound)
}

func publicUser(u database.PersistedUser) map[string]any {
	return map[string]any{"id": u.ID, "name": u.Name, "labels": u.Labels, "role": u.Role, "enabled": u.Enabled, "created_at": u.CreatedAt, "updated_at": u.UpdatedAt}
}

func (m *APIModule) createUser(ctx ModuleContext, w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name, Password string
		Role           database.UserRole `json:"role"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Name == "" || len(input.Password) < 8 || (input.Role != database.UserRoleAdmin && input.Role != database.UserRoleUser) {
		common.WriteError(w, 400, common.MessageInvalidUser)
		return
	}
	users, err := ctx.Database().ListUsers()
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotListUsers)
		return
	}
	for _, u := range users {
		if u.Name == input.Name {
			common.WriteError(w, 400, common.MessageUserNameExists)
			return
		}
	}
	id, err := randomID(16)
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotGenerateUserID)
		return
	}
	now := time.Now().UTC()
	user := &database.PersistedUser{ID: id, Name: input.Name, Role: input.Role, Enabled: true, PasswordHash: hashPassword(input.Password), CreatedAt: now, UpdatedAt: now}
	if err := ctx.Database().SaveUser(user); err != nil {
		common.WriteError(w, 500, common.MessageCouldNotSaveUser)
		return
	}
	writeJSON(w, 201, publicUser(*user))
}

func (m *APIModule) updateUser(ctx ModuleContext, w http.ResponseWriter, r *http.Request, id string) {
	current, _ := currentUser(r)
	var input struct {
		Role    database.UserRole `json:"role"`
		Enabled *bool             `json:"enabled"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Role != "" && input.Role != database.UserRoleAdmin && input.Role != database.UserRoleUser {
		common.WriteError(w, 400, common.MessageInvalidRole)
		return
	}
	users, err := ctx.Database().ListUsers()
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotListUsers)
		return
	}
	for i := range users {
		if users[i].ID == id {
			if current != nil && current.ID == id && (input.Role == database.UserRoleUser || (input.Enabled != nil && !*input.Enabled)) {
				common.WriteError(w, 400, common.MessageCannotDemoteCurrentUser)
				return
			}
			if input.Role != "" {
				users[i].Role = input.Role
			}
			if input.Enabled != nil {
				users[i].Enabled = *input.Enabled
			}
			users[i].UpdatedAt = time.Now().UTC()
			if err := ctx.Database().SaveUser(&users[i]); err != nil {
				common.WriteError(w, 500, common.MessageCouldNotSaveUser)
				return
			}
			writeJSON(w, 200, publicUser(users[i]))
			return
		}
	}
	common.WriteError(w, 404, common.MessageUserNotFound)
}

func (m *APIModule) deleteUser(ctx ModuleContext, w http.ResponseWriter, r *http.Request, id string) {
	current, _ := currentUser(r)
	if current != nil && current.ID == id {
		common.WriteError(w, 400, common.MessageCannotDeleteCurrentUser)
		return
	}
	users, err := ctx.Database().ListUsers()
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotListUsers)
		return
	}
	found := false
	for _, u := range users {
		if u.ID == id {
			found = true
			break
		}
	}
	if !found {
		common.WriteError(w, 404, common.MessageUserNotFound)
		return
	}
	if err := ctx.Database().DeleteUser(id); err != nil {
		common.WriteError(w, 500, common.MessageCouldNotDeleteUser)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *APIModule) providers(ctx ModuleContext, w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 1 && r.Method == http.MethodGet {
		accounts, err := ctx.Database().ListAccounts()
		if err != nil {
			common.WriteError(w, 500, common.MessageCouldNotListProviders)
			return
		}
		items := make([]map[string]any, 0, len(accounts))
		for _, a := range accounts {
			items = append(items, publicAccount(ctx.Database(), a))
		}
		writeJSON(w, 200, map[string]any{"items": items, "total": len(items)})
		return
	}
	if !isAdmin(r) {
		common.WriteError(w, 403, common.MessageForbidden)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPost {
		m.createProvider(ctx, w, r)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodDelete {
		m.deleteProvider(ctx, w, r, parts[1])
		return
	}
	if len(parts) == 2 && r.Method == http.MethodPut {
		m.updateProvider(ctx, w, r, parts[1])
		return
	}
	http.NotFound(w, r)
}

func publicAccount(db database.Database, a database.PersistedAccount) map[string]any {
	item := map[string]any{"id": a.ID, "name": a.Name, "provider": a.Provider, "enabled": accountEnabled(a.Config), "config": sanitizeJSON(a.Config), "created_at": a.CreatedAt, "updated_at": a.UpdatedAt}
	if db != nil {
		if id := credentialIDFromConfig(a.Config); id != "" {
			if raw, err := db.LoadCredential(id); err == nil {
				var credential any
				if json.Unmarshal(raw, &credential) == nil {
					item["credential"] = credential
				}
			}
		}
	}
	return item
}

func accountEnabled(raw json.RawMessage) bool {
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil {
		return true
	}
	value, ok := fields["enabled"]
	if !ok {
		return true
	}
	enabled, ok := value.(bool)
	if !ok {
		return true
	}
	return enabled
}

func (m *APIModule) createProvider(ctx ModuleContext, w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name     string          `json:"name"`
		Provider string          `json:"provider"`
		Config   json.RawMessage `json:"config"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Provider == "" || len(input.Config) == 0 || string(input.Config) == "null" {
		common.WriteError(w, 400, common.MessageProviderAndConfigRequired)
		return
	}
	if !json.Valid(input.Config) {
		common.WriteError(w, 400, common.MessageInvalidProviderConfig)
		return
	}
	config, credentialID, credentialRaw, err := normalizeProviderConfig(input.Config)
	if err != nil {
		common.WriteError(w, 400, common.MessageInvalidProviderConfig)
		return
	}
	if credentialID != "" {
		if _, err := ctx.Database().LoadCredential(credentialID); err != nil {
			common.WriteError(w, 400, common.MessageCredentialNotFound)
			return
		}
	}
	if credentialRaw != nil {
		credentialID, err = randomID(16)
		if err != nil {
			common.WriteError(w, 500, common.MessageCouldNotGenerateCredentialID)
			return
		}
		if err = ctx.Database().SaveCredential(credentialID, credentialRaw); err != nil {
			common.WriteError(w, 500, common.MessageCouldNotSaveCredential)
			return
		}
		config, _, _, _ = normalizeProviderConfigWithID(input.Config, credentialID)
	}
	id, err := randomID(16)
	if err != nil {
		if credentialID != "" && credentialRaw != nil {
			_ = ctx.Database().DeleteCredential(credentialID)
		}
		common.WriteError(w, 500, common.MessageCouldNotGenerateProviderID)
		return
	}
	now := time.Now().UTC()
	account := &database.PersistedAccount{ID: id, Name: input.Name, Provider: input.Provider, Config: config, CreatedAt: now, UpdatedAt: now}
	if _, err = ctx.Providers().Create(id, input.Provider, config); err != nil {
		if credentialID != "" && credentialRaw != nil {
			_ = ctx.Database().DeleteCredential(credentialID)
		}
		common.WriteError(w, 400, common.MessageInvalidProviderConfig)
		return
	}
	if err = ctx.Database().SaveAccount(account); err != nil {
		ctx.Providers().Remove(id)
		if credentialID != "" && credentialRaw != nil {
			_ = ctx.Database().DeleteCredential(credentialID)
		}
		common.WriteError(w, 500, common.MessageCouldNotSaveProvider)
		return
	}
	writeJSON(w, 201, publicAccount(ctx.Database(), *account))
}

func (m *APIModule) deleteProvider(ctx ModuleContext, w http.ResponseWriter, r *http.Request, id string) {
	accounts, err := ctx.Database().ListAccounts()
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotListProviders)
		return
	}
	var account *database.PersistedAccount
	for i := range accounts {
		if accounts[i].ID == id {
			account = &accounts[i]
			break
		}
	}
	if account == nil {
		common.WriteError(w, 404, common.MessageProviderNotFound)
		return
	}
	keys, err := ctx.Database().ListAPIKeys("")
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotInspectProviderRefs)
		return
	}
	for _, key := range keys {
		if key.AccountID == id {
			common.WriteError(w, 400, common.MessageProviderStillReferenced)
			return
		}
	}
	if err := ctx.Database().DeleteAccount(id); err != nil {
		common.WriteError(w, 500, common.MessageCouldNotDeleteProvider)
		return
	}
	ctx.Providers().Remove(id)
	if credentialID := credentialIDFromConfig(account.Config); credentialID != "" && !credentialReferenced(accounts, id, credentialID) {
		_ = ctx.Database().DeleteCredential(credentialID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *APIModule) updateProvider(ctx ModuleContext, w http.ResponseWriter, r *http.Request, id string) {
	var input struct {
		Name     string          `json:"name"`
		Provider string          `json:"provider"`
		Config   json.RawMessage `json:"config"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	accounts, err := ctx.Database().ListAccounts()
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotListProviders)
		return
	}
	var account *database.PersistedAccount
	for i := range accounts {
		if accounts[i].ID == id {
			account = &accounts[i]
			break
		}
	}
	if account == nil {
		common.WriteError(w, 404, common.MessageProviderNotFound)
		return
	}
	if input.Name != "" {
		account.Name = input.Name
	}
	if input.Provider != "" && input.Provider != account.Provider {
		common.WriteError(w, 400, common.MessageProviderTypeCannotChange)
		return
	}
	if len(input.Config) != 0 && string(input.Config) != "null" {
		if !json.Valid(input.Config) {
			common.WriteError(w, 400, common.MessageInvalidProviderConfig)
			return
		}
		config, credentialID, credentialRaw, err := normalizeProviderConfig(input.Config)
		if err != nil {
			common.WriteError(w, 400, common.MessageInvalidProviderConfig)
			return
		}
		if credentialID != "" {
			if _, err := ctx.Database().LoadCredential(credentialID); err != nil {
				common.WriteError(w, 400, common.MessageCredentialNotFound)
				return
			}
		}
		if credentialRaw != nil {
			credentialID, err = randomID(16)
			if err != nil {
				common.WriteError(w, 500, common.MessageCouldNotGenerateCredentialID)
				return
			}
			if err = ctx.Database().SaveCredential(credentialID, credentialRaw); err != nil {
				common.WriteError(w, 500, common.MessageCouldNotSaveCredential)
				return
			}
			config, _, _, _ = normalizeProviderConfigWithID(input.Config, credentialID)
		}
		if p, ok := ctx.Providers().Get(id); ok {
			if err := p.UpdateConfig(config); err != nil {
				if credentialRaw != nil {
					_ = ctx.Database().DeleteCredential(credentialID)
				}
				common.WriteError(w, 400, common.MessageInvalidProviderConfig)
				return
			}
		}
		oldCredentialID := credentialIDFromConfig(account.Config)
		account.Config = config
		if oldCredentialID != "" && oldCredentialID != credentialID && !credentialReferenced(accounts, id, oldCredentialID) {
			_ = ctx.Database().DeleteCredential(oldCredentialID)
		}
	}
	account.UpdatedAt = time.Now().UTC()
	if err := ctx.Database().SaveAccount(account); err != nil {
		common.WriteError(w, 500, common.MessageCouldNotSaveProvider)
		return
	}
	writeJSON(w, 200, publicAccount(ctx.Database(), *account))
}

func credentialReferenced(accounts []database.PersistedAccount, deletedID, credentialID string) bool {
	for _, a := range accounts {
		if a.ID != deletedID && credentialIDFromConfig(a.Config) == credentialID {
			return true
		}
	}
	return false
}

func (m *APIModule) keys(ctx ModuleContext, w http.ResponseWriter, r *http.Request, parts []string) {
	u, ok := currentUser(r)
	if !ok {
		common.WriteError(w, 401, common.MessageUnauthorized)
		return
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		m.listKeys(ctx, w, u.ID)
	case len(parts) == 1 && r.Method == http.MethodPost:
		m.createKey(ctx, w, r, u)
	case len(parts) == 2 && r.Method == http.MethodGet:
		m.getKey(ctx, w, u.ID, parts[1])
	case len(parts) == 2 && r.Method == http.MethodDelete:
		m.deleteKey(ctx, w, u.ID, parts[1])
	default:
		http.NotFound(w, r)
	}
}

func (m *APIModule) listKeys(ctx ModuleContext, w http.ResponseWriter, userID string) {
	keys, err := ctx.Database().ListAPIKeys(userID)
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotListAPIKeys)
		return
	}
	items := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		items = append(items, publicAPIKey(k))
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": len(items)})
}

func (m *APIModule) createKey(ctx ModuleContext, w http.ResponseWriter, r *http.Request, u *database.PersistedUser) {
	var input struct {
		Name         string `json:"name"`
		AccountID    string `json:"account_id"`
		ValidSeconds int64  `json:"valid_seconds"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		common.WriteError(w, 400, common.MessageAPIKeyNameRequired)
		return
	}
	if input.AccountID == "" {
		common.WriteError(w, 400, common.MessageAccountIDRequired)
		return
	}
	if input.ValidSeconds < 0 {
		common.WriteError(w, 400, common.MessageValidSecondsNegative)
		return
	}
	accounts, err := ctx.Database().ListAccounts()
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotListProviders)
		return
	}
	found := false
	for _, a := range accounts {
		if a.ID == input.AccountID {
			found = true
			break
		}
	}
	if !found {
		common.WriteError(w, 404, common.MessageProviderNotFound)
		return
	}
	id, err := randomID(16)
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotGenerateAPIKeyID)
		return
	}
	secret, err := randomID(32)
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotGenerateAPIKey)
		return
	}
	now := time.Now().UTC()
	key := &database.PersistedAPIKey{ID: id, Name: name, UserID: u.ID, AccountID: input.AccountID, Key: secret, ValidSeconds: input.ValidSeconds, CreatedAt: now, UpdatedAt: now}
	if err := ctx.Database().SaveAPIKey(key); err != nil {
		common.WriteError(w, 500, common.MessageCouldNotSaveAPIKey)
		return
	}
	writeJSON(w, 201, publicAPIKey(*key))
}

func (m *APIModule) getKey(ctx ModuleContext, w http.ResponseWriter, userID, id string) {
	key, ok := ownedAPIKey(ctx, w, userID, id)
	if !ok {
		return
	}
	writeJSON(w, 200, publicAPIKey(*key))
}

func (m *APIModule) deleteKey(ctx ModuleContext, w http.ResponseWriter, userID, id string) {
	key, ok := ownedAPIKey(ctx, w, userID, id)
	if !ok {
		return
	}
	if err := ctx.Database().DeleteAPIKey(key.ID); err != nil {
		common.WriteError(w, 500, common.MessageCouldNotDeleteAPIKey)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func ownedAPIKey(ctx ModuleContext, w http.ResponseWriter, userID, id string) (*database.PersistedAPIKey, bool) {
	keys, err := ctx.Database().ListAPIKeys(userID)
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotListAPIKeys)
		return nil, false
	}
	for i := range keys {
		if keys[i].ID == id {
			return &keys[i], true
		}
	}
	common.WriteError(w, 404, common.MessageAPIKeyNotFound)
	return nil, false
}

func publicAPIKey(k database.PersistedAPIKey) map[string]any {
	item := map[string]any{"id": k.ID, "name": k.Name, "account_id": k.AccountID, "key": k.Key, "valid_seconds": k.ValidSeconds, "created_at": k.CreatedAt, "updated_at": k.UpdatedAt}
	if k.ValidSeconds > 0 {
		item["expires_at"] = k.CreatedAt.Add(time.Duration(k.ValidSeconds) * time.Second)
	}
	return item
}

func (m *APIModule) calls(ctx ModuleContext, w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r)
	if !ok {
		common.WriteError(w, 401, common.MessageUnauthorized)
		return
	}
	page, pageSize := 1, 100
	if raw := r.URL.Query().Get("page"); raw != "" {
		if _, err := fmt.Sscan(raw, &page); err != nil || page < 1 {
			common.WriteError(w, 400, common.MessageInvalidPage)
			return
		}
	}
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		if _, err := fmt.Sscan(raw, &pageSize); err != nil || pageSize < 20 || pageSize > 100 {
			common.WriteError(w, 400, common.MessagePageSizeRange)
			return
		}
	}
	userName := ""
	if !isAdmin(r) {
		userName = u.Name
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	values := []string{}
	if q != "" {
		values = []string{q}
	}
	items, total, err := ctx.Database().QueryCallTraces(userName, "", nil, page, pageSize, nil, values...)
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotQueryCallRecords)
		return
	}
	for i := range items {
		items[i].APIKey = ""
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(value); err != nil {
		common.WriteError(w, 400, common.MessageInvalidJSONBody)
		return false
	}
	return true
}

func randomID(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func sanitizeJSON(raw json.RawMessage) any {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	sanitizeValue(value)
	return value
}
func sanitizeValue(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key := range v {
			lower := strings.ToLower(key)
			if lower == "access_token" || lower == "refresh_token" || lower == "raw" || lower == "credential" {
				delete(v, key)
			} else {
				sanitizeValue(v[key])
			}
		}
	case []any:
		for _, item := range v {
			sanitizeValue(item)
		}
	}
}
func normalizeProviderConfig(raw json.RawMessage) (json.RawMessage, string, json.RawMessage, error) {
	return normalizeProviderConfigWithID(raw, "")
}
func normalizeProviderConfigWithID(raw json.RawMessage, credentialID string) (json.RawMessage, string, json.RawMessage, error) {
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		var encoded string
		if string(raw) != "" && json.Unmarshal(raw, &encoded) == nil {
			if err := json.Unmarshal([]byte(encoded), &value); err == nil {
				raw, _ = json.Marshal(value)
			} else {
				return nil, "", nil, errors.New("provider config must be a JSON object")
			}
		} else {
			return nil, "", nil, errors.New("provider config must be a JSON object")
		}
	}
	if value == nil {
		return nil, "", nil, errors.New("provider config must be a JSON object")
	}
	if nested, ok := value["credential"].(map[string]any); ok {
		b, err := json.Marshal(nested)
		if err != nil {
			return nil, "", nil, errors.New("invalid credential")
		}
		delete(value, "credential")
		if credentialID == "" {
			return json.RawMessage(nil), "", b, nil
		}
		value["credential_id"] = credentialID
	}
	if id, ok := value["credential_id"].(string); ok && id != "" {
		credentialID = id
	}
	result, err := json.Marshal(value)
	if err != nil {
		return nil, "", nil, err
	}
	return result, credentialID, nil, nil
}
func credentialIDFromConfig(raw json.RawMessage) string {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	id, _ := value["credential_id"].(string)
	return id
}
