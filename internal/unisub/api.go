package unisub

import (
	aiprovider "ai-unisub/internal/aiprovider"
	"ai-unisub/internal/common"
	"ai-unisub/internal/database"
	proxy "ai-unisub/internal/proxy"
	framework "ai-unisub/internal/service"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// APIModule exposes the authenticated management JSON API described by
// docs/service-api.md. AIProvider request forwarding remains the responsibility
// of the gateway module.
type APIModule struct{ mutations sync.Mutex }

func NewAPIModule() *APIModule    { return &APIModule{} }
func (m *APIModule) Name() string { return "api" }
func (m *APIModule) Close() error { return nil }

func (m *APIModule) Init(ctx framework.ModuleContext) error {
	apiMux := http.NewServeMux()
	newManagementAPI(apiMux, func(w http.ResponseWriter, r *http.Request) {
		m.handle(ctx, w, r)
	})
	// Keep the legacy dispatcher as a fallback while operations move to typed
	// Huma handlers. Registered method-aware routes win over this prefix.
	apiMux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		m.handle(ctx, w, r)
	})

	for _, path := range []string{"/api/login", "/api/logout"} {
		ctx.HandleFunc(path, framework.RouteOptions{Auth: framework.AuthNone, Name: "api"}, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", "POST")
				common.WriteError(w, http.StatusMethodNotAllowed, common.MessageMethodNotAllowed)
				return
			}
			if r.URL.Path == "/api/login" {
				m.login(ctx, w, r)
				return
			}
			token := ""
			if cookie, err := r.Cookie("session"); err == nil {
				token = cookie.Value
			}
			ctx.Auth().ClearSessionCookie(w, token)
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})
	}

	ctx.HandleFunc("/api/", framework.RouteOptions{Auth: framework.AuthSession, Name: "api"}, func(w http.ResponseWriter, r *http.Request) {
		if isManagementDocumentationPath(r.URL.Path) && !isAdmin(r) {
			common.WriteError(w, http.StatusForbidden, common.MessageForbidden)
			return
		}
		apiMux.ServeHTTP(w, r)
	})
	return nil
}

func isManagementDocumentationPath(path string) bool {
	return path == managementDocsPath || strings.HasPrefix(path, managementOpenAPIPath)
}

func (m *APIModule) handle(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/api")
	if path == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// Non-admin sessions may only access APIs used by the four personal pages.
	if !isAdmin(r) {
		switch parts[0] {
		case "me", "password", "keys", "calls":
		default:
			common.WriteError(w, http.StatusForbidden, common.MessageForbidden)
			return
		}
	}
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
	case "accounts":
		m.accounts(ctx, w, r, parts)
		return
	case "suppliers":
		m.suppliers(ctx, w, r, parts)
		return
	case "proxy-groups":
		m.proxyGroups(ctx, w, r, parts[1:])
		return
	case "keys":
		if len(parts) == 2 && parts[1] == "accounts" {
			if r.Method != http.MethodGet {
				methodNotAllowed(w)
				return
			}
			m.accountOptions(ctx, w)
			return
		}
		m.keys(ctx, w, r, parts)
		return
	case "calls":
		if len(parts) == 1 && r.Method == http.MethodGet {
			m.calls(ctx, w, r)
			return
		}
		if len(parts) == 3 && r.Method == http.MethodGet {
			id, _ := strconv.Atoi(parts[2])
			m.callDetail(ctx, w, r, parts[1], id)
			return
		}
	case "usage":
		if len(parts) == 2 && r.Method == http.MethodGet && isAdmin(r) {
			m.usage(ctx, w, r, parts[1])
			return
		}
	}
	http.NotFound(w, r)
}

func (m *APIModule) proxyGroups(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, parts []string) {
	if !isAdmin(r) {
		common.WriteError(w, http.StatusForbidden, common.MessageForbidden)
		return
	}
	if len(parts) > 1 {
		http.NotFound(w, r)
		return
	}
	id := 0
	if len(parts) == 1 {
		id, _ = strconv.Atoi(parts[0])
	}
	if id == 0 && r.Method == http.MethodGet {
		groups := ctx.Proxy().List()
		if groups == nil {
			groups = []proxy.ProxyGroup{}
		}
		writeJSON(w, http.StatusOK, groups)
		return
	}
	if id == 0 && r.Method == http.MethodPost {
		var config proxy.ProxyGroupConfig
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&config) != nil {
			common.WriteError(w, http.StatusBadRequest, "invalid proxy group")
			return
		}
		createdID, err := ctx.Proxy().Create(config)
		if err != nil {
			common.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, findProxyGroup(ctx.Proxy().List(), createdID))
		return
	}
	if id == 0 {
		http.NotFound(w, r)
		return
	}
	current := findProxyGroup(ctx.Proxy().List(), id)
	if current == nil {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, current)
	case http.MethodPut:
		var config proxy.ProxyGroupConfig
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&config) != nil {
			common.WriteError(w, http.StatusBadRequest, "invalid proxy group")
			return
		}
		if err := ctx.Proxy().Update(id, config); err != nil {
			common.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, findProxyGroup(ctx.Proxy().List(), id))
	case http.MethodDelete:
		if err := ctx.Proxy().Delete(id); err != nil {
			common.WriteError(w, http.StatusInternalServerError, "could not delete proxy group")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}

func findProxyGroup(groups []proxy.ProxyGroup, id int) *proxy.ProxyGroup {
	for i := range groups {
		if groups[i].ID == id {
			return &groups[i]
		}
	}
	return nil
}

func usageTimeRange(r *http.Request) (database.TimeRange, error) {
	query := r.URL.Query()
	start, end := time.Now().UTC(), time.Now().UTC()
	switch query.Get("range") {
	case "1d", "":
		start = end.Add(-24 * time.Hour)
	case "1w":
		start = end.Add(-7 * 24 * time.Hour)
	case "1m":
		start = end.Add(-30 * 24 * time.Hour)
	case "custom":
		var err error
		start, err = time.ParseInLocation("2006-01-02", query.Get("from"), time.UTC)
		if err != nil {
			return database.TimeRange{}, errors.New("invalid usage start date")
		}
		endDate, err := time.ParseInLocation("2006-01-02", query.Get("to"), time.UTC)
		if err != nil {
			return database.TimeRange{}, errors.New("invalid usage end date")
		}
		end = endDate.Add(24*time.Hour - time.Nanosecond)
	default:
		return database.TimeRange{}, errors.New("invalid usage range")
	}
	if start.After(end) {
		return database.TimeRange{}, errors.New("usage start date must not be after end date")
	}
	return database.TimeRange{Start: start, End: end}, nil
}

func (m *APIModule) usage(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, kind string) {
	timeRange, err := usageTimeRange(r)
	if err != nil {
		common.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if kind == "subscriptions" {
		rows, totals, err := ctx.Database().QueryAccountUsage(timeRange)
		if err != nil {
			common.WriteError(w, http.StatusInternalServerError, common.MessageCouldNotQueryCallRecords)
			return
		}
		accounts, err := ctx.Database().ListAccounts()
		if err != nil {
			common.WriteError(w, http.StatusInternalServerError, common.MessageCouldNotListAIProviders)
			return
		}
		accountByID := make(map[int]database.PersistedAccount, len(accounts))
		for _, account := range accounts {
			accountByID[account.ID] = account
		}
		items := make([]any, 0, len(rows))
		for _, row := range rows {
			account, ok := accountByID[row.AccountID]
			if !ok {
				continue
			}
			items = append(items, map[string]any{
				"subscription_id":   account.ID,
				"subscription_name": account.Name,
				"provider":          account.AIProvider,
				"usage":             row.Usage,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items, "totals": totals, "has_records": totals.Requests > 0})
		return
	}
	if kind != "users" {
		http.NotFound(w, r)
		return
	}
	accountID := 0
	if raw := r.URL.Query().Get("subscription_id"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil || id < 0 {
			common.WriteError(w, http.StatusBadRequest, "invalid subscription_id")
			return
		}
		accountID = id
	}
	rows, totals, err := ctx.Database().QueryUserUsage(timeRange, accountID)
	if err != nil {
		common.WriteError(w, http.StatusInternalServerError, common.MessageCouldNotQueryCallRecords)
		return
	}
	users, err := ctx.Database().ListUsers()
	if err != nil {
		common.WriteError(w, http.StatusInternalServerError, common.MessageCouldNotListUsers)
		return
	}
	userByID := make(map[int]database.PersistedUser, len(users))
	for _, user := range users {
		userByID[user.ID] = user
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		item := map[string]any{"usage": row.Usage, "enabled": true}
		if row.UserID != 0 {
			item["user_id"] = row.UserID
			if user, ok := userByID[row.UserID]; ok {
				item["username"] = user.Name
				item["role"] = string(user.Role)
				item["enabled"] = user.Enabled
			}
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "totals": totals, "has_records": totals.Requests > 0})
}

func currentUser(r *http.Request) (*database.PersistedUser, bool) {
	p, ok := framework.PrincipalFromContext(r.Context())
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
	writeJSON(w, http.StatusOK, map[string]any{"id": u.ID, "name": u.Name, "role": u.Role, "server_version": framework.Version})
}

func (m *APIModule) password(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request) {
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
	if !framework.VerifyPassword(u.PasswordHash, input.OldPassword) {
		common.WriteError(w, http.StatusBadRequest, common.MessageOldPasswordIncorrect)
		return
	}
	u.PasswordHash = framework.HashPassword(input.NewPassword)
	u.UpdatedAt = time.Now().UTC()
	if err := ctx.Database().SaveUser(u); err != nil {
		common.WriteError(w, http.StatusInternalServerError, common.MessageCouldNotSavePassword)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (m *APIModule) users(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, parts []string) {
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
	userID, _ := strconv.Atoi(parts[1])
	if len(parts) != 2 || userID == 0 {
		if len(parts) == 3 && parts[1] != "" && parts[2] == "password" && r.Method == http.MethodPost {
			userID, _ := strconv.Atoi(parts[1])
			m.resetUserPassword(ctx, w, r, userID)
			return
		}
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPut {
		m.updateUser(ctx, w, r, userID)
		return
	}
	if r.Method == http.MethodDelete {
		m.deleteUser(ctx, w, r, userID)
		return
	}
	http.NotFound(w, r)
}

func (m *APIModule) resetUserPassword(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, id int) {
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
		users[i].PasswordHash = framework.HashPassword(input.Password)
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

func (m *APIModule) createUser(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request) {
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
	now := time.Now().UTC()
	user := &database.PersistedUser{ID: 0, Name: input.Name, Role: input.Role, Enabled: true, PasswordHash: framework.HashPassword(input.Password), CreatedAt: now, UpdatedAt: now}
	if err := ctx.Database().SaveUser(user); err != nil {
		common.WriteError(w, 500, common.MessageCouldNotSaveUser)
		return
	}
	writeJSON(w, 201, publicUser(*user))
}

func (m *APIModule) updateUser(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, id int) {
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

func (m *APIModule) deleteUser(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, id int) {
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

func (m *APIModule) accounts(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 3 && parts[2] == "refresh-quota" {
		id, _ := strconv.Atoi(parts[1])
		m.refreshAccountQuota(ctx, w, r, id)
		return
	}
	if len(parts) == 3 && parts[2] == "fetch-models" {
		id, _ := strconv.Atoi(parts[1])
		m.fetchAccountModels(ctx, w, r, id)
		return
	}
	if len(parts) == 3 && parts[2] == "models" {
		id, _ := strconv.Atoi(parts[1])
		m.listAccountModels(ctx, w, r, id)
		return
	}
	if r.Method != http.MethodGet {
		m.mutations.Lock()
		defer m.mutations.Unlock()
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		accounts := ctx.AIProviders().ListAccounts()
		items := make([]map[string]any, 0, len(accounts))
		for _, account := range accounts {
			items = append(items, publicAccount(account, isAdmin(r)))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
		return
	}
	if !isAdmin(r) {
		common.WriteError(w, 403, common.MessageForbidden)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPost {
		m.createAccount(ctx, w, r)
		return
	}
	id, _ := strconv.Atoi(parts[1])
	if len(parts) == 2 && r.Method == http.MethodDelete {
		m.deleteAccount(ctx, w, r, id)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodPut {
		m.updateAccount(ctx, w, r, id)
		return
	}
	http.NotFound(w, r)
}

func publicAccount(account *aiprovider.Account, admin bool) map[string]any {
	item := map[string]any{
		"id": account.ID, "name": account.Config.Name, "enabled": account.Config.Enabled,
	}
	if admin {
		// Secrets never leave the server; updates treat an empty secret as "keep the stored one".
		config := account.Config
		config.APIKey = ""
		config.Credential.AccessToken = ""
		config.Credential.RefreshToken = ""
		item["config"] = config
		if account.Config.Kind != aiprovider.AccountGroup {
			quota := account.Quota
			if quota.CacheStatus == "" {
				// Never queried: the pages show "unknown" for missing.
				quota.CacheStatus = aiprovider.QuotaCacheMissing
			}
			item["quota"] = quota
		}
	}
	return item
}

func (m *APIModule) createAccount(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name   string          `json:"name"`
		Config json.RawMessage `json:"config"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.Config) == 0 || string(input.Config) == "null" {
		common.WriteError(w, 400, common.MessageAIProviderAndConfigRequired)
		return
	}
	config, err := decodeAccountConfig(input.Name, input.Config)
	if err != nil {
		common.WriteError(w, http.StatusBadRequest, common.MessageInvalidAccountConfig)
		return
	}
	raw, err := json.Marshal(config)
	if err != nil {
		common.WriteError(w, http.StatusBadRequest, common.MessageInvalidAccountConfig)
		return
	}
	account, err := ctx.AIProviders().NewAccount(raw)
	if err != nil {
		common.WriteError(w, http.StatusBadRequest, common.MessageInvalidAccountConfig)
		return
	}
	writeJSON(w, http.StatusCreated, publicAccount(account, true))
}

func (m *APIModule) deleteAccount(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, id int) {
	if providerAccount(ctx, id) == nil {
		common.WriteError(w, 404, common.MessageAIProviderNotFound)
		return
	}
	keys, err := ctx.Database().ListAPIKeys(0)
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotInspectAIProviderRefs)
		return
	}
	for _, key := range keys {
		if key.AccountID == id {
			common.WriteError(w, 400, common.MessageAIProviderStillReferenced)
			return
		}
	}
	for _, account := range ctx.AIProviders().ListAccounts() {
		if account.Config.Kind != aiprovider.AccountGroup {
			continue
		}
		for _, member := range account.Config.Members {
			if member.ID == id {
				common.WriteError(w, http.StatusBadRequest, common.MessageAIProviderStillReferenced)
				return
			}
		}
	}
	if err := ctx.AIProviders().DelAccount(id); err != nil {
		common.WriteError(w, 500, common.MessageCouldNotDeleteAIProvider)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *APIModule) updateAccount(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, id int) {
	var input struct {
		Name   string          `json:"name"`
		Config json.RawMessage `json:"config"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	account := providerAccount(ctx, id)
	if account == nil {
		common.WriteError(w, 404, common.MessageAIProviderNotFound)
		return
	}
	current := account.Config
	if len(input.Config) == 0 || string(input.Config) == "null" {
		input.Config, _ = json.Marshal(current)
	}
	config, err := decodeAccountConfig(input.Name, input.Config)
	if err != nil {
		common.WriteError(w, http.StatusBadRequest, common.MessageInvalidAccountConfig)
		return
	}
	if config.Kind == "" {
		config.Kind = current.Kind
	}
	if config.Kind != current.Kind {
		common.WriteError(w, 400, common.MessageAIProviderTypeCannotChange)
		return
	}
	// Responses mask secrets, so an empty secret keeps the stored one.
	if config.Kind == aiprovider.AccountAPI && config.APIKey == "" {
		config.APIKey = current.APIKey
	}
	if config.Kind == aiprovider.AccountSubscription && config.Credential.AccessToken == "" {
		config.Credential = current.Credential
	}
	raw, err := json.Marshal(config)
	if err != nil || ctx.AIProviders().SetAccountConfig(r.Context(), id, raw) != nil {
		common.WriteError(w, http.StatusBadRequest, common.MessageInvalidAccountConfig)
		return
	}
	writeJSON(w, http.StatusOK, publicAccount(providerAccount(ctx, id), true))
}

func decodeAccountConfig(name string, raw json.RawMessage) (aiprovider.AccountConfig, error) {
	var config aiprovider.AccountConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return aiprovider.AccountConfig{}, err
	}
	if name != "" {
		config.Name = name
	}
	return config, nil
}

func (m *APIModule) keys(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, parts []string) {
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
		id, _ := strconv.Atoi(parts[1])
		m.getKey(ctx, w, u.ID, id)
	case len(parts) == 2 && r.Method == http.MethodDelete:
		id, _ := strconv.Atoi(parts[1])
		m.deleteKey(ctx, w, u.ID, id)
	case len(parts) == 4 && parts[2] == "config" && r.Method == http.MethodGet:
		id, _ := strconv.Atoi(parts[1])
		m.getKeyConfig(ctx, w, u.ID, id, parts[3])
	case len(parts) == 4 && parts[2] == "config" && r.Method == http.MethodPut:
		id, _ := strconv.Atoi(parts[1])
		m.updateKeyConfig(ctx, w, r, u.ID, id, parts[3])
	default:
		http.NotFound(w, r)
	}
}

func (m *APIModule) getKeyConfig(ctx framework.ModuleContext, w http.ResponseWriter, userID, id int, clientName string) {
	key, ok := ownedAPIKey(ctx, w, userID, id)
	if !ok {
		return
	}
	client, err := parseCCSwitchClient(clientName)
	if err != nil {
		common.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, parseAPIKeyConfig(key.Config).CCSwitch[client])
}

func (m *APIModule) updateKeyConfig(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, userID, id int, clientName string) {
	key, ok := ownedAPIKey(ctx, w, userID, id)
	if !ok {
		return
	}
	client, err := parseCCSwitchClient(clientName)
	if err != nil {
		common.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	var input CCSwitchClientConfig
	if !decodeJSON(w, r, &input) {
		return
	}
	config := parseAPIKeyConfig(key.Config)
	config.CCSwitch[client] = input
	encoded, err := json.Marshal(config)
	if err != nil {
		common.WriteError(w, http.StatusBadRequest, "invalid API key config")
		return
	}
	key.Config = encoded
	key.UpdatedAt = time.Now().UTC()
	if err := ctx.Database().SaveAPIKey(key); err != nil {
		common.WriteError(w, http.StatusInternalServerError, common.MessageCouldNotSaveAPIKey)
		return
	}
	writeJSON(w, http.StatusOK, input)
}

func (m *APIModule) listKeys(ctx framework.ModuleContext, w http.ResponseWriter, userID int) {
	keys, err := ctx.Database().ListAPIKeys(userID)
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotListAPIKeys)
		return
	}
	items := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		items = append(items, publicAPIKeyWithClients(k, accountClients(ctx, k.AccountID)))
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": len(items)})
}

func (m *APIModule) createKey(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, u *database.PersistedUser) {
	var input struct {
		Name         string `json:"name"`
		AccountID    int    `json:"account_id"`
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
	if input.AccountID == 0 {
		common.WriteError(w, 400, common.MessageAccountIDRequired)
		return
	}
	if input.ValidSeconds < 0 {
		common.WriteError(w, 400, common.MessageValidSecondsNegative)
		return
	}
	accounts, err := ctx.Database().ListAccounts()
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotListAIProviders)
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
		common.WriteError(w, 404, common.MessageAIProviderNotFound)
		return
	}
	secret, err := randomID(32)
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotGenerateAPIKey)
		return
	}
	now := time.Now().UTC()
	key := &database.PersistedAPIKey{ID: 0, Name: name, UserID: u.ID, AccountID: input.AccountID, Key: secret, ValidSeconds: input.ValidSeconds, CreatedAt: now, UpdatedAt: now}
	if err := ctx.Database().SaveAPIKey(key); err != nil {
		common.WriteError(w, 500, common.MessageCouldNotSaveAPIKey)
		return
	}
	writeJSON(w, 201, publicAPIKeyWithClients(*key, accountClients(ctx, key.AccountID)))
}

func (m *APIModule) getKey(ctx framework.ModuleContext, w http.ResponseWriter, userID, id int) {
	key, ok := ownedAPIKey(ctx, w, userID, id)
	if !ok {
		return
	}
	writeJSON(w, 200, publicAPIKeyWithClients(*key, accountClients(ctx, key.AccountID)))
}

func (m *APIModule) deleteKey(ctx framework.ModuleContext, w http.ResponseWriter, userID, id int) {
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

func ownedAPIKey(ctx framework.ModuleContext, w http.ResponseWriter, userID, id int) (*database.PersistedAPIKey, bool) {
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
	item := map[string]any{"id": k.ID, "name": k.Name, "account_id": k.AccountID, "key": k.Key, "valid_seconds": k.ValidSeconds, "created_at": k.CreatedAt, "updated_at": k.UpdatedAt, "client_types": []string{}}
	if k.ValidSeconds > 0 {
		item["expires_at"] = k.CreatedAt.Add(time.Duration(k.ValidSeconds) * time.Second)
	}
	return item
}

func publicAPIKeyWithClients(k database.PersistedAPIKey, clients []aiprovider.ClientType) map[string]any {
	item := publicAPIKey(k)
	item["client_types"] = clientTypesJSON(clients)
	return item
}

func accountClients(ctx framework.ModuleContext, accountID int) []aiprovider.ClientType {
	account := providerAccount(ctx, accountID)
	if account == nil {
		return nil
	}
	clients, err := account.SupportedClients()
	if err != nil {
		return nil
	}
	return clients
}

func clientTypesJSON(clients []aiprovider.ClientType) []string {
	result := make([]string, len(clients))
	for i, client := range clients {
		result[i] = string(client)
	}
	return result
}

func (m *APIModule) calls(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request) {
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
		if _, err := fmt.Sscan(raw, &pageSize); err != nil || pageSize < 10 || pageSize > 100 {
			common.WriteError(w, 400, common.MessagePageSizeRange)
			return
		}
	}
	admin := isAdmin(r)
	var userID *int
	if !admin || r.URL.Query().Get("mine") == "1" {
		userID = &u.ID
	}
	accountID, _ := strconv.Atoi(r.URL.Query().Get("account_id"))
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	filter := database.CallTraceFilter{UserID: userID, AccountID: accountID, Search: search, SearchUsernames: admin}
	if admin && strings.EqualFold(search, "none") {
		filter.UserID = new(int)
		filter.Search = ""
	}
	if raw := r.URL.Query().Get("code"); raw != "" {
		code, err := strconv.Atoi(raw)
		if err != nil || (code != 0 && (code < 100 || code > 599)) {
			common.WriteError(w, 400, "invalid call code")
			return
		}
		filter.Code = &code
	}
	timeRange, err := usageTimeRange(r)
	if err != nil {
		common.WriteError(w, 400, err.Error())
		return
	}
	filter.TimeRange = timeRange
	items, total, err := ctx.Database().QueryCallTraces(filter, page, pageSize)
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotQueryCallRecords)
		return
	}
	for i := range items {
		items[i].APIKey = ""
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total})
}

func (m *APIModule) callDetail(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, day string, id int) {
	startedAt, err := time.ParseInLocation("20060102", day, time.UTC)
	if err != nil || id == 0 {
		common.WriteError(w, http.StatusBadRequest, "invalid call record")
		return
	}
	trace, err := ctx.Database().GetCallTrace(startedAt, id)
	if errors.Is(err, database.ErrCallTraceNotFound) {
		common.WriteError(w, http.StatusNotFound, "call trace not found")
		return
	}
	if err != nil {
		common.WriteError(w, http.StatusInternalServerError, "could not get call record")
		return
	}
	if !isAdmin(r) {
		u, ok := currentUser(r)
		if !ok || !apiKeyBelongsToUser(ctx, u.ID, trace.APIKey) {
			common.WriteError(w, http.StatusNotFound, "call trace not found")
			return
		}
	}
	// The key value is only needed for ownership checks and must not be sent
	// back to the browser as part of the detail response.
	trace.APIKey = ""
	writeJSON(w, http.StatusOK, trace)
}

func apiKeyBelongsToUser(ctx framework.ModuleContext, userID int, value string) bool {
	keys, err := ctx.Database().ListAPIKeys(userID)
	if err != nil {
		return false
	}
	for _, key := range keys {
		if key.Key == value || strconv.Itoa(key.ID) == value {
			return true
		}
	}
	return false
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		common.WriteError(w, 400, common.MessageInvalidJSONBody)
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
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

func normalizeAIProviderConfig(raw json.RawMessage) (json.RawMessage, string, json.RawMessage, error) {
	return normalizeAIProviderConfigWithID(raw, "")
}
func normalizeAIProviderConfigWithID(raw json.RawMessage, credentialID string) (json.RawMessage, string, json.RawMessage, error) {
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

func (m *APIModule) login(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if decoder.Decode(&input) != nil || strings.TrimSpace(input.Username) == "" || input.Password == "" {
		common.WriteError(w, http.StatusBadRequest, common.MessageUsernamePasswordRequired)
		return
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		common.WriteError(w, http.StatusBadRequest, common.MessageUsernamePasswordRequired)
		return
	}
	users, err := ctx.Database().ListUsers()
	if err != nil {
		common.WriteError(w, http.StatusInternalServerError, common.MessageCouldNotAuthenticateUser)
		return
	}
	for _, user := range users {
		if user.Name != input.Username || !user.Enabled || !framework.VerifyPassword(user.PasswordHash, input.Password) {
			continue
		}
		token, err := ctx.Auth().CreateSession(&user)
		if err != nil {
			common.WriteError(w, http.StatusInternalServerError, common.MessageCouldNotAuthenticateUser)
			return
		}
		ctx.Auth().SetSessionCookie(w, token)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "user": publicUser(user)})
		return
	}
	common.WriteError(w, http.StatusUnauthorized, common.MessageInvalidUsernameOrPassword)
}
