package unisub

import (
	"ai-unisub/internal/aiprovider"
	"ai-unisub/internal/common"
	"ai-unisub/internal/database"
	"ai-unisub/internal/proxy"
	proxyconfig "ai-unisub/internal/proxy"
	framework "ai-unisub/internal/service"
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
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
		m.handle(ctx, w, r)
	})
	return nil
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
	case "ai-providers", "providers": // Keep the old URL as a compatibility alias.
		m.aiProviders(ctx, w, r, parts)
		return
	case "ai-catalog":
		m.aiCatalog(ctx, w, r, parts)
		return
	case "proxy-groups":
		m.proxyGroups(ctx, w, r, parts[1:])
		return
	case "keys":
		if len(parts) == 2 && parts[1] == "providers" {
			if r.Method != http.MethodGet {
				methodNotAllowed(w)
				return
			}
			m.providerOptions(ctx, w)
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
	id := 0
	if len(parts) == 1 {
		id, _ = strconv.Atoi(parts[0])
	}
	if len(parts) == 1 && parts[0] == "errors" && r.Method == http.MethodGet {
		groupID, _ := strconv.Atoi(r.URL.Query().Get("group_id"))
		proxyID := strings.TrimSpace(r.URL.Query().Get("proxy_id"))
		if groupID == 0 || proxyID == "" {
			common.WriteError(w, http.StatusBadRequest, "group_id and proxy_id are required")
			return
		}
		value, err := ctx.Proxy().ErrorRecords(groupID, proxyID)
		if err != nil {
			common.WriteError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, value)
		return
	}
	if len(parts) == 1 && parts[0] == "test" && r.Method == http.MethodPost {
		var input struct {
			GroupID int    `json:"group_id"`
			ProxyID string `json:"proxy_id"`
			URL     string `json:"url"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input) != nil {
			common.WriteError(w, http.StatusBadRequest, "invalid proxy test request")
			return
		}
		var value *proxy.Entry
		var err error
		if input.GroupID != 0 && strings.TrimSpace(input.ProxyID) != "" {
			value, err = ctx.Proxy().Test(r.Context(), input.GroupID, input.ProxyID)
		} else if strings.TrimSpace(input.URL) != "" {
			if _, err := proxyconfig.NewEndpoint(input.URL); err != nil {
				common.WriteError(w, http.StatusBadRequest, common.MessageInvalidProxy)
				return
			}
			value, err = ctx.Proxy().TestURL(r.Context(), input.URL)
		} else {
			common.WriteError(w, http.StatusBadRequest, "group_id and proxy_id or url are required")
			return
		}
		if err != nil {
			common.WriteError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, value)
		return
	}
	if len(parts) == 2 && parts[1] == "test" && r.Method == http.MethodPost {
		var input struct {
			ProxyID string `json:"proxy_id"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input) != nil || input.ProxyID == "" {
			common.WriteError(w, http.StatusBadRequest, "proxy_id is required")
			return
		}
		value, err := ctx.Proxy().Test(r.Context(), id, input.ProxyID)
		if err != nil {
			common.WriteError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, value)
		return
	}
	if len(parts) > 1 {
		http.NotFound(w, r)
		return
	}
	if id == 0 && r.Method == http.MethodGet {
		value, err := ctx.Proxy().List()
		if err != nil {
			common.WriteError(w, http.StatusInternalServerError, "could not list proxy groups")
			return
		}
		stripProxyErrors(value)
		writeJSON(w, http.StatusOK, value)
		return
	}
	if id == 0 && r.Method == http.MethodPost {
		var value proxy.Group
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&value) != nil {
			common.WriteError(w, http.StatusBadRequest, "invalid proxy group")
			return
		}
		if err := validateProxyGroupURLs(&value); err != nil {
			common.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		value.ID = 0
		now := time.Now().UTC()
		value.CreatedAt = now
		value.UpdatedAt = now
		if err := ctx.Proxy().New(&value); err != nil {
			common.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		stripProxyErrors([]proxy.Group{value})
		writeJSON(w, http.StatusCreated, value)
		return
	}
	if id == 0 {
		http.NotFound(w, r)
		return
	}
	groups, err := ctx.Proxy().List()
	if err != nil {
		common.WriteError(w, http.StatusInternalServerError, "could not list proxy groups")
		return
	}
	var current *proxy.Group
	for i := range groups {
		if groups[i].ID == id {
			current = &groups[i]
			break
		}
	}
	if current == nil {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		stripProxyErrors([]proxy.Group{*current})
		writeJSON(w, http.StatusOK, current)
	case http.MethodPut:
		var value proxy.Group
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&value) != nil {
			common.WriteError(w, http.StatusBadRequest, "invalid proxy group")
			return
		}
		if err := validateProxyGroupURLs(&value); err != nil {
			common.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		value.ID = id
		value.CreatedAt = current.CreatedAt
		value.UpdatedAt = time.Now().UTC()
		if err := ctx.Proxy().Save(&value); err != nil {
			common.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		stripProxyErrors([]proxy.Group{value})
		writeJSON(w, http.StatusOK, value)
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

func validateProxyGroupURLs(group *proxy.Group) error {
	for _, proxy := range group.Proxies {
		if _, err := proxyconfig.NewEndpoint(proxy.URL); err != nil || strings.TrimSpace(proxy.URL) == "" {
			return errors.New(common.MessageInvalidProxy)
		}
	}
	return nil
}

func stripProxyErrors(groups []proxy.Group) {
	for gi := range groups {
		for pi := range groups[gi].Proxies {
			groups[gi].Proxies[pi].LastErrorAt = nil
			groups[gi].Proxies[pi].ErrorRecords = nil
		}
	}
}

type usageTotals struct {
	Requests            int `json:"requests"`
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`
	TotalTokens         int `json:"total_tokens"`
}

func (t *usageTotals) add(trace database.PersistedCallTraceSummary) {
	t.Requests++
	t.InputTokens += trace.InputTokens
	t.OutputTokens += trace.OutputTokens
	t.CacheCreationTokens += trace.CacheCreationTokens
	t.CacheReadTokens += trace.CacheReadTokens
	t.TotalTokens += trace.InputTokens + trace.OutputTokens + trace.CacheCreationTokens + trace.CacheReadTokens
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
	filter := database.CallTraceFilter{}
	filter.TimeRange = timeRange
	traces, _, err := ctx.Database().QueryCallTraces(filter, 1, 1000000)
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
	keys, err := ctx.Database().ListAPIKeys(0)
	if err != nil {
		common.WriteError(w, http.StatusInternalServerError, common.MessageCouldNotListAPIKeys)
		return
	}
	keyBySecret := make(map[string]database.PersistedAPIKey, len(keys))
	for _, key := range keys {
		keyBySecret[key.Key] = key
		keyBySecret[strconv.Itoa(key.ID)] = key
	}

	if kind == "subscriptions" {
		groups := map[int]*struct {
			SubscriptionID   int         `json:"subscription_id"`
			SubscriptionName string      `json:"subscription_name"`
			AIProvider       string      `json:"provider"`
			Usage            usageTotals `json:"usage"`
		}{}
		for _, trace := range traces {
			account, ok := accountByID[trace.AccountID]
			if !ok {
				continue
			}
			row := groups[account.ID]
			if row == nil {
				row = &struct {
					SubscriptionID   int         `json:"subscription_id"`
					SubscriptionName string      `json:"subscription_name"`
					AIProvider       string      `json:"provider"`
					Usage            usageTotals `json:"usage"`
				}{SubscriptionID: account.ID, SubscriptionName: account.Name, AIProvider: account.AIProvider}
				groups[account.ID] = row
			}
			row.Usage.add(trace)
		}
		items := make([]any, 0, len(groups))
		totals := usageTotals{}
		for _, row := range groups {
			totals.Requests += row.Usage.Requests
			totals.InputTokens += row.Usage.InputTokens
			totals.OutputTokens += row.Usage.OutputTokens
			totals.CacheCreationTokens += row.Usage.CacheCreationTokens
			totals.CacheReadTokens += row.Usage.CacheReadTokens
			totals.TotalTokens += row.Usage.TotalTokens
			items = append(items, row)
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items, "totals": totals, "has_records": len(traces) > 0})
		return
	}
	if kind != "users" {
		http.NotFound(w, r)
		return
	}
	if subscriptionID, err := strconv.Atoi(r.URL.Query().Get("subscription_id")); err == nil || subscriptionID != 0 {
		filtered := traces[:0]
		for _, trace := range traces {
			if trace.AccountID == subscriptionID {
				filtered = append(filtered, trace)
			}
		}
		traces = filtered
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
	type userUsageRow struct {
		UserID   int         `json:"user_id,omitempty"`
		Username string      `json:"username,omitempty"`
		Role     string      `json:"role,omitempty"`
		Enabled  bool        `json:"enabled"`
		Usage    usageTotals `json:"usage"`
	}
	groups := map[int]*userUsageRow{}
	for _, trace := range traces {
		key, ok := keyBySecret[trace.APIKey]
		id := 0
		name, role := "", ""
		if ok {
			id = key.UserID
		}
		enabled := true
		if user, found := userByID[id]; found {
			name, role, enabled = user.Name, string(user.Role), user.Enabled
		}
		groupID := cmp.Or(id, 0)
		row := groups[groupID]
		if row == nil {
			row = &userUsageRow{UserID: id, Username: name, Role: role, Enabled: enabled}
			groups[groupID] = row
		}
		row.Usage.add(trace)
	}
	items := make([]any, 0, len(groups))
	totals := usageTotals{}
	for _, row := range groups {
		totals.Requests += row.Usage.Requests
		totals.InputTokens += row.Usage.InputTokens
		totals.OutputTokens += row.Usage.OutputTokens
		totals.CacheCreationTokens += row.Usage.CacheCreationTokens
		totals.CacheReadTokens += row.Usage.CacheReadTokens
		totals.TotalTokens += row.Usage.TotalTokens
		items = append(items, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "totals": totals, "has_records": len(traces) > 0})
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

func (m *APIModule) aiProviders(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 3 && parts[2] == "refresh-quota" {
		id, _ := strconv.Atoi(parts[1])
		m.refreshAIProviderQuota(ctx, w, r, id)
		return
	}
	if r.Method != http.MethodGet {
		m.mutations.Lock()
		defer m.mutations.Unlock()
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		accounts, err := ctx.Database().ListAccounts()
		if err != nil {
			common.WriteError(w, 500, common.MessageCouldNotListAIProviders)
			return
		}
		items := make([]map[string]any, 0, len(accounts))
		for _, a := range accounts {
			item := publicAccount(nil, a)
			if isAdmin(r) {
				item = publicAccount(ctx.Database(), a)
				if a.AIProvider != "group" {
					if provider, ok := ctx.AIProviders().Get(a.ID); ok {
						if quota := provider.GetCachedQuota(); quota != nil {
							item["quota"] = quota
						}
					} else {
						item["quota"] = &aiprovider.Quota{CacheStatus: aiprovider.QuotaCacheMissing}
					}
				}
			}
			items = append(items, item)
		}
		writeJSON(w, 200, map[string]any{"items": items, "total": len(items)})
		return
	}
	if !isAdmin(r) {
		common.WriteError(w, 403, common.MessageForbidden)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPost {
		m.createAIProvider(ctx, w, r)
		return
	}
	id, _ := strconv.Atoi(parts[1])
	if len(parts) == 2 && r.Method == http.MethodDelete {
		m.deleteAIProvider(ctx, w, r, id)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodPut {
		m.updateAIProvider(ctx, w, r, id)
		return
	}
	http.NotFound(w, r)
}

func publicAccount(db database.Database, a database.PersistedAccount) map[string]any {
	item := map[string]any{"id": a.ID, "name": a.Name, "provider": a.AIProvider, "auth_type": aiProviderAuthType(a.Config), "enabled": accountEnabled(a.Config), "config": sanitizeJSON(a.Config), "created_at": a.CreatedAt, "updated_at": a.UpdatedAt}
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

func aiProviderAuthType(raw json.RawMessage) string {
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil {
		return "oauth"
	}
	if value, ok := fields["auth_type"].(string); ok && strings.TrimSpace(value) != "" {
		return strings.ToLower(strings.TrimSpace(value))
	}
	return "oauth"
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

func (m *APIModule) createAIProvider(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name       string          `json:"name"`
		AIProvider string          `json:"provider"`
		Config     json.RawMessage `json:"config"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.AIProvider == "" || len(input.Config) == 0 || string(input.Config) == "null" {
		common.WriteError(w, 400, common.MessageAIProviderAndConfigRequired)
		return
	}
	if !json.Valid(input.Config) {
		common.WriteError(w, 400, common.MessageInvalidAIProviderConfig)
		return
	}
	config, credentialID, credentialRaw, err := normalizeAIProviderConfig(input.Config)
	if err != nil {
		common.WriteError(w, 400, common.MessageInvalidAIProviderConfig)
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
		config, _, _, _ = normalizeAIProviderConfigWithID(input.Config, credentialID)
	}
	account := &database.PersistedAccount{ID: 0, Name: input.Name, AIProvider: input.AIProvider, Config: config}
	if err := ctx.Database().SaveAccount(account); err != nil {
		common.WriteError(w, 500, common.MessageCouldNotSaveAIProvider)
		return
	}

	if _, err = ctx.AIProviders().Create(account.ID, input.AIProvider, config, nil); err != nil {
		if credentialID != "" && credentialRaw != nil {
			_ = ctx.Database().DeleteCredential(credentialID)
		}
		_ = ctx.Database().DeleteAccount(account.ID)
		common.WriteError(w, 400, common.MessageInvalidAIProviderConfig)
		return
	}

	if p, ok := ctx.AIProviders().Get(account.ID); ok {
		account.Config = canonicalProviderConfig(config, p.Config())
	}
	applyExportedState(ctx, account)
	if err = ctx.Database().SaveAccount(account); err != nil {
		ctx.AIProviders().Remove(account.ID)
		if credentialID != "" && credentialRaw != nil {
			_ = ctx.Database().DeleteCredential(credentialID)
		}
		return
	}
	writeJSON(w, 201, publicAccount(ctx.Database(), *account))
}

func (m *APIModule) deleteAIProvider(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, id int) {
	if ctx.AIProviders().Referenced(id) {
		common.WriteError(w, 400, common.MessageAIProviderStillReferenced)
		return
	}
	accounts, err := ctx.Database().ListAccounts()
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotListAIProviders)
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
	if err := ctx.Database().DeleteAccount(id); err != nil {
		common.WriteError(w, 500, common.MessageCouldNotDeleteAIProvider)
		return
	}
	ctx.AIProviders().Remove(id)
	if credentialID := credentialIDFromConfig(account.Config); credentialID != "" && !credentialReferenced(accounts, id, credentialID) {
		_ = ctx.Database().DeleteCredential(credentialID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *APIModule) updateAIProvider(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, id int) {
	var input struct {
		Name       string          `json:"name"`
		AIProvider string          `json:"provider"`
		Config     json.RawMessage `json:"config"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	accounts, err := ctx.Database().ListAccounts()
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotListAIProviders)
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
		common.WriteError(w, 404, common.MessageAIProviderNotFound)
		return
	}
	previousConfig := append(json.RawMessage(nil), account.Config...)
	var createdCredential, retiredCredential string
	if input.Name != "" {
		account.Name = input.Name
	}
	if input.AIProvider != "" && input.AIProvider != account.AIProvider {
		common.WriteError(w, 400, common.MessageAIProviderTypeCannotChange)
		return
	}
	if len(input.Config) != 0 && string(input.Config) != "null" {
		if !json.Valid(input.Config) {
			common.WriteError(w, 400, common.MessageInvalidAIProviderConfig)
			return
		}
		// An omitted API key in an edit retains the stored secret. Never send it
		// back to the browser just to round-trip an otherwise public config.
		var next, previous map[string]any
		if json.Unmarshal(input.Config, &next) == nil && next != nil && json.Unmarshal(account.Config, &previous) == nil {
			if next["auth_type"] == "api_key" && previous["auth_type"] == "api_key" {
				if _, supplied := next["api_key"]; !supplied {
					next["api_key"] = previous["api_key"]
				}
			}
			input.Config, _ = json.Marshal(next)
		}
		config, credentialID, credentialRaw, err := normalizeAIProviderConfig(input.Config)
		if err != nil {
			common.WriteError(w, 400, common.MessageInvalidAIProviderConfig)
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
			config, _, _, _ = normalizeAIProviderConfigWithID(input.Config, credentialID)
			createdCredential = credentialID
		}
		if p, ok := ctx.AIProviders().Get(id); ok {
			_ = p
			if err := ctx.AIProviders().UpdateConfig(id, config); err != nil {
				if credentialRaw != nil {
					_ = ctx.Database().DeleteCredential(credentialID)
				}
				common.WriteError(w, 400, common.MessageInvalidAIProviderConfig)
				return
			}
		}
		oldCredentialID := credentialIDFromConfig(account.Config)
		account.Config = config
		if p, ok := ctx.AIProviders().Get(id); ok {
			account.Config = canonicalProviderConfig(config, p.Config())
		}
		if oldCredentialID != "" && oldCredentialID != credentialID && !credentialReferenced(accounts, id, oldCredentialID) {
			retiredCredential = oldCredentialID
		}
	}
	account.UpdatedAt = time.Now().UTC()
	applyExportedState(ctx, account)
	if err := ctx.Database().SaveAccount(account); err != nil {
		_ = ctx.AIProviders().UpdateConfig(id, previousConfig)
		if createdCredential != "" {
			_ = ctx.Database().DeleteCredential(createdCredential)
		}
		common.WriteError(w, 500, common.MessageCouldNotSaveAIProvider)
		return
	}
	if retiredCredential != "" {
		_ = ctx.Database().DeleteCredential(retiredCredential)
	}
	writeJSON(w, 200, publicAccount(ctx.Database(), *account))
}

func applyExportedState(ctx framework.ModuleContext, account *database.PersistedAccount) {
	if account == nil {
		return
	}
	if data, ok := ctx.AIProviders().Export(account.ID); ok {
		account.State = data.State
	}
}

func canonicalProviderConfig(raw json.RawMessage, c aiprovider.AIProviderConfig) json.RawMessage {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	delete(fields, "client_types")
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	normalized, _ := json.Marshal(c)
	var values map[string]json.RawMessage
	_ = json.Unmarshal(normalized, &values)
	maps.Copy(fields, values)
	out, _ := json.Marshal(fields)
	return out
}

func credentialReferenced(accounts []database.PersistedAccount, deletedID int, credentialID string) bool {
	for _, a := range accounts {
		if a.ID != deletedID && credentialIDFromConfig(a.Config) == credentialID {
			return true
		}
	}
	return false
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
	default:
		http.NotFound(w, r)
	}
}

func (m *APIModule) listKeys(ctx framework.ModuleContext, w http.ResponseWriter, userID int) {
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
	writeJSON(w, 201, publicAPIKey(*key))
}

func (m *APIModule) getKey(ctx framework.ModuleContext, w http.ResponseWriter, userID, id int) {
	key, ok := ownedAPIKey(ctx, w, userID, id)
	if !ok {
		return
	}
	writeJSON(w, 200, publicAPIKey(*key))
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
	item := map[string]any{"id": k.ID, "name": k.Name, "account_id": k.AccountID, "key": k.Key, "valid_seconds": k.ValidSeconds, "created_at": k.CreatedAt, "updated_at": k.UpdatedAt}
	if k.ValidSeconds > 0 {
		item["expires_at"] = k.CreatedAt.Add(time.Duration(k.ValidSeconds) * time.Second)
	}
	return item
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
	userName := ""
	if !isAdmin(r) || r.URL.Query().Get("mine") == "1" {
		userName = u.Name
	}
	accountID, _ := strconv.Atoi(r.URL.Query().Get("account_id"))
	filter := database.CallTraceFilter{UserName: userName, AccountID: accountID, Search: strings.TrimSpace(r.URL.Query().Get("q")), SearchUsernames: isAdmin(r)}
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
			if lower == "access_token" || lower == "refresh_token" || lower == "api_key" || lower == "raw" || lower == "credential" {
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
func credentialIDFromConfig(raw json.RawMessage) string {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	id, _ := value["credential_id"].(string)
	return id
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
