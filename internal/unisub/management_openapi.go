package unisub

import (
	"ai-unisub/internal/aiprovider"
	"ai-unisub/internal/database"
	"ai-unisub/internal/oauth"
	"ai-unisub/internal/proxy"
	"ai-unisub/internal/service"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

const (
	managementOpenAPIPath = "/api/openapi"
	managementDocsPath    = "/api/docs"
)

type managementDispatcher func(http.ResponseWriter, *http.Request)

// The types in this file are the management API wire contract. Huma reflects
// them into OpenAPI, and the frontend generates its TypeScript DTOs from that
// document. Domain packages remain independent of the HTTP contract.
type ErrorResponse struct {
	Error string `json:"error"`
}

type StatusResponse struct {
	Status string `json:"status" enum:"ok"`
}

type UserResponse struct {
	ID            int               `json:"id"`
	Name          string            `json:"name"`
	Labels        []string          `json:"labels,omitempty"`
	Role          database.UserRole `json:"role" enum:"admin,user"`
	Enabled       bool              `json:"enabled,omitzero"`
	ServerVersion string            `json:"server_version,omitempty"`
	CreatedAt     time.Time         `json:"created_at,omitzero"`
	UpdatedAt     time.Time         `json:"updated_at,omitzero"`
}

type LoginRequest struct {
	Username string `json:"username" minLength:"1"`
	Password string `json:"password" minLength:"1"`
}

type LoginResponse struct {
	Status string       `json:"status" enum:"ok"`
	User   UserResponse `json:"user"`
}

type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" minLength:"1"`
	NewPassword string `json:"new_password" minLength:"8"`
}

type CreateUserRequest struct {
	Name     string            `json:"name" minLength:"1"`
	Password string            `json:"password" minLength:"8"`
	Role     database.UserRole `json:"role" enum:"admin,user"`
}

type UpdateUserRequest struct {
	Role    database.UserRole `json:"role,omitempty" enum:"admin,user"`
	Enabled *bool             `json:"enabled,omitempty"`
}

type ResetUserPasswordRequest struct {
	Password string `json:"password" minLength:"8"`
}

type UserListResponse struct {
	Items []UserResponse `json:"items"`
	Total int            `json:"total" minimum:"0"`
}

type AccountConfig struct {
	Kind                     aiprovider.AccountKind   `json:"kind,omitempty" enum:"subscription,api,group"`
	Name                     string                   `json:"name,omitempty"`
	Labels                   []string                 `json:"labels,omitempty"`
	Supplier                 string                   `json:"supplier,omitempty"`
	ClientType               aiprovider.ClientType    `json:"client_type,omitempty" enum:"claude,codex,grok"`
	ProxyGroupID             int                      `json:"proxy_group_id,omitzero" minimum:"0"`
	Enabled                  *bool                    `json:"enabled,omitempty"`
	MaxConcurrentConnections int                      `json:"max_concurrent_connections,omitzero" minimum:"0"`
	QueueTimeoutSeconds      int                      `json:"queue_timeout_seconds,omitzero" minimum:"0"`
	SubscriptionPlan         string                   `json:"subscription_plan,omitempty"`
	OfficialOnly             bool                     `json:"official_only,omitzero"`
	Credential               *oauth.OAuthCredential   `json:"credential,omitempty"`
	APIEndpoint              string                   `json:"api_endpoint,omitempty" format:"uri"`
	APIKey                   string                   `json:"api_key,omitempty"`
	Members                  []aiprovider.GroupMember `json:"members,omitempty"`
}

type AccountResponse struct {
	ID      int                      `json:"id"`
	Name    string                   `json:"name"`
	Enabled bool                     `json:"enabled"`
	Config  AccountConfig            `json:"config"`
	Quota   *aiprovider.AccountQuota `json:"quota,omitempty"`
}

type AccountListResponse struct {
	Items []AccountResponse `json:"items"`
	Total int               `json:"total" minimum:"0"`
}

type SaveAccountRequest struct {
	Name   string        `json:"name" minLength:"1"`
	Config AccountConfig `json:"config"`
}

type ModelInfo struct {
	ID string `json:"id"`
}

type ModelListResponse struct {
	Models []ModelInfo `json:"models"`
}

type ModelMappingResponse struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type SupplierResponse struct {
	ID                      string                  `json:"id"`
	Name                    string                  `json:"name"`
	ClaudeURL               string                  `json:"claude_url"`
	OpenAIURL               string                  `json:"openai_url"`
	Models                  []string                `json:"models"`
	ModelMappings           []ModelMappingResponse  `json:"model_mappings"`
	SupportedClients        []aiprovider.ClientType `json:"supported_clients"`
	SubscriptionPlanWeights map[string]int          `json:"subscription_plan_weights,omitempty"`
}

type SupplierListResponse struct {
	Suppliers        []SupplierResponse `json:"suppliers"`
	BuiltinSuppliers []SupplierResponse `json:"builtin_suppliers"`
}

type SaveSupplierRequest struct {
	ID                      string                 `json:"id,omitempty"`
	Name                    string                 `json:"name,omitempty"`
	Models                  []string               `json:"models,omitempty"`
	ModelMappings           []ModelMappingResponse `json:"model_mappings,omitempty"`
	SubscriptionPlanWeights map[string]int         `json:"subscription_plan_weights,omitempty"`
}

type AccountOption struct {
	ID          int                     `json:"id"`
	Name        string                  `json:"name"`
	Kind        aiprovider.AccountKind  `json:"kind" enum:"subscription,api,group"`
	Supplier    string                  `json:"supplier,omitempty"`
	Enabled     bool                    `json:"enabled"`
	ClientTypes []aiprovider.ClientType `json:"client_types"`
}

type AccountOptionListResponse struct {
	Items []AccountOption `json:"items"`
	Total int             `json:"total" minimum:"0"`
}

type APIKeyResponse struct {
	ID           int                     `json:"id"`
	Name         string                  `json:"name"`
	AccountID    int                     `json:"account_id"`
	Key          string                  `json:"key"`
	ValidSeconds int64                   `json:"valid_seconds" minimum:"0"`
	CreatedAt    time.Time               `json:"created_at"`
	UpdatedAt    time.Time               `json:"updated_at,omitzero"`
	ExpiresAt    time.Time               `json:"expires_at,omitzero"`
	ClientTypes  []aiprovider.ClientType `json:"client_types"`
}

type APIKeyListResponse struct {
	Items []APIKeyResponse `json:"items"`
	Total int              `json:"total" minimum:"0"`
}

type CreateAPIKeyRequest struct {
	Name         string `json:"name" minLength:"1" maxLength:"64"`
	AccountID    int    `json:"account_id" minimum:"1"`
	ValidSeconds int64  `json:"valid_seconds" minimum:"0"`
}

type CallListResponse struct {
	Items []database.PersistedCallTraceSummary `json:"items"`
	Total int                                  `json:"total" minimum:"0"`
}

type UsageItem struct {
	SubscriptionID   int                  `json:"subscription_id,omitzero"`
	SubscriptionName string               `json:"subscription_name,omitempty"`
	Provider         string               `json:"provider,omitempty"`
	UserID           int                  `json:"user_id,omitzero"`
	Username         string               `json:"username,omitempty"`
	Role             string               `json:"role,omitempty"`
	Enabled          bool                 `json:"enabled,omitzero"`
	Usage            database.UsageTotals `json:"usage"`
}

type UsageResponse struct {
	Data       []UsageItem          `json:"data"`
	Totals     database.UsageTotals `json:"totals"`
	HasRecords bool                 `json:"has_records"`
}

type OAuthStartRequest struct {
	ProxyGroupID int `json:"proxy_group_id,omitzero" minimum:"0"`
}

type OAuthCompleteRequest struct {
	Code  string `json:"code" minLength:"1"`
	State string `json:"state" minLength:"1"`
}

type OAuthStatusResponse struct {
	Status          string    `json:"status" enum:"pending,complete"`
	ResultID        string    `json:"result_id,omitempty"`
	Error           string    `json:"error,omitempty"`
	IntervalSeconds int       `json:"interval_seconds,omitzero"`
	ExpiresAt       time.Time `json:"expires_at,omitzero"`
}

type OAuthResultIDResponse struct {
	ResultID string `json:"result_id"`
}

type OAuthResultResponse struct {
	Result oauth.OAuthCredential `json:"result"`
}

type managementOperation struct {
	Method       string
	Path         string
	OperationID  string
	Summary      string
	Tag          string
	Role         string
	Status       int
	RequestType  reflect.Type
	ResponseType reflect.Type
	Parameters   []*huma.Param
	Deprecated   bool
}

func newManagementAPI(mux *http.ServeMux, dispatch managementDispatcher) huma.API {
	config := huma.DefaultConfig("UniSub Management API", service.Version)
	config.Info.Description = "The session-authenticated API used by the UniSub dashboard. Go operation declarations and wire structs are the source of truth."
	config.OpenAPIPath = managementOpenAPIPath
	config.DocsPath = managementDocsPath
	config.DocsRenderer = huma.DocsRendererScalar
	config.SchemasPath = ""
	config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"sessionCookie": {
			Type:        "apiKey",
			In:          "cookie",
			Name:        "session",
			Description: "Browser session created by POST /api/login.",
		},
	}
	config.Security = []map[string][]string{{"sessionCookie": {}}}

	api := humago.New(mux, config)
	registerManagementOperations(api, dispatch)
	return api
}

// ManagementOpenAPI builds the generated document without application
// dependencies. It is used by cmd/openapi and contract tests.
func ManagementOpenAPI() *huma.OpenAPI {
	api := newManagementAPI(http.NewServeMux(), nil)
	return api.OpenAPI()
}

func registerManagementOperations(api huma.API, dispatch managementDispatcher) {
	public := "public"
	admin := "admin"
	user := "user"

	operations := []managementOperation{
		{http.MethodPost, "/api/login", "login", "Log in", "Session", public, http.StatusOK, reflect.TypeFor[LoginRequest](), reflect.TypeFor[LoginResponse](), nil, false},
		{http.MethodPost, "/api/logout", "logout", "Log out", "Session", public, http.StatusOK, nil, reflect.TypeFor[StatusResponse](), nil, false},
		{http.MethodGet, "/api/me", "get-current-user", "Get current user", "Session", user, http.StatusOK, nil, reflect.TypeFor[UserResponse](), nil, false},
		{http.MethodPost, "/api/password", "change-password", "Change current password", "Session", user, http.StatusOK, reflect.TypeFor[ChangePasswordRequest](), reflect.TypeFor[StatusResponse](), nil, false},

		{http.MethodGet, "/api/users", "list-users", "List users", "Users", admin, http.StatusOK, nil, reflect.TypeFor[UserListResponse](), nil, false},
		{http.MethodPost, "/api/users", "create-user", "Create user", "Users", admin, http.StatusCreated, reflect.TypeFor[CreateUserRequest](), reflect.TypeFor[UserResponse](), nil, false},
		{http.MethodPut, "/api/users/{id}", "update-user", "Update user", "Users", admin, http.StatusOK, reflect.TypeFor[UpdateUserRequest](), reflect.TypeFor[UserResponse](), []*huma.Param{pathInteger("id")}, false},
		{http.MethodDelete, "/api/users/{id}", "delete-user", "Delete user", "Users", admin, http.StatusNoContent, nil, nil, []*huma.Param{pathInteger("id")}, false},
		{http.MethodPost, "/api/users/{id}/password", "reset-user-password", "Reset user password", "Users", admin, http.StatusOK, reflect.TypeFor[ResetUserPasswordRequest](), reflect.TypeFor[UserResponse](), []*huma.Param{pathInteger("id")}, false},

		{http.MethodGet, "/api/accounts", "list-accounts", "List accounts", "Accounts", admin, http.StatusOK, nil, reflect.TypeFor[AccountListResponse](), nil, false},
		{http.MethodPost, "/api/accounts", "create-account", "Create account", "Accounts", admin, http.StatusCreated, reflect.TypeFor[SaveAccountRequest](), reflect.TypeFor[AccountResponse](), nil, false},
		{http.MethodPut, "/api/accounts/{id}", "update-account", "Update account", "Accounts", admin, http.StatusOK, reflect.TypeFor[SaveAccountRequest](), reflect.TypeFor[AccountResponse](), []*huma.Param{pathInteger("id")}, false},
		{http.MethodDelete, "/api/accounts/{id}", "delete-account", "Delete account", "Accounts", admin, http.StatusNoContent, nil, nil, []*huma.Param{pathInteger("id")}, false},
		{http.MethodPost, "/api/accounts/{id}/refresh-quota", "refresh-account-quota", "Refresh account quota", "Accounts", admin, http.StatusOK, nil, reflect.TypeFor[aiprovider.AccountQuota](), []*huma.Param{pathInteger("id")}, false},
		{http.MethodPost, "/api/accounts/{id}/fetch-models", "fetch-account-models", "Fetch models from upstream", "Accounts", admin, http.StatusOK, nil, reflect.TypeFor[ModelListResponse](), []*huma.Param{pathInteger("id")}, false},
		{http.MethodGet, "/api/accounts/{id}/models", "list-account-models", "List configured models", "Accounts", admin, http.StatusOK, nil, reflect.TypeFor[ModelListResponse](), []*huma.Param{pathInteger("id")}, false},

		{http.MethodGet, "/api/suppliers", "list-suppliers", "List suppliers", "Suppliers", admin, http.StatusOK, nil, reflect.TypeFor[SupplierListResponse](), nil, false},
		{http.MethodGet, "/api/suppliers/{id}", "get-supplier", "Get supplier", "Suppliers", admin, http.StatusOK, nil, reflect.TypeFor[SupplierResponse](), []*huma.Param{pathString("id")}, false},
		{http.MethodPut, "/api/suppliers/{id}", "update-supplier", "Update supplier", "Suppliers", admin, http.StatusOK, reflect.TypeFor[SaveSupplierRequest](), reflect.TypeFor[SupplierResponse](), []*huma.Param{pathString("id")}, false},

		{http.MethodGet, "/api/proxy-groups", "list-proxy-groups", "List proxy groups", "Proxy Groups", admin, http.StatusOK, nil, reflect.TypeFor[[]proxy.ProxyGroup](), nil, false},
		{http.MethodPost, "/api/proxy-groups", "create-proxy-group", "Create proxy group", "Proxy Groups", admin, http.StatusCreated, reflect.TypeFor[proxy.ProxyGroupConfig](), reflect.TypeFor[proxy.ProxyGroup](), nil, false},
		{http.MethodGet, "/api/proxy-groups/{id}", "get-proxy-group", "Get proxy group", "Proxy Groups", admin, http.StatusOK, nil, reflect.TypeFor[proxy.ProxyGroup](), []*huma.Param{pathInteger("id")}, false},
		{http.MethodPut, "/api/proxy-groups/{id}", "update-proxy-group", "Update proxy group", "Proxy Groups", admin, http.StatusOK, reflect.TypeFor[proxy.ProxyGroupConfig](), reflect.TypeFor[proxy.ProxyGroup](), []*huma.Param{pathInteger("id")}, false},
		{http.MethodDelete, "/api/proxy-groups/{id}", "delete-proxy-group", "Delete proxy group", "Proxy Groups", admin, http.StatusNoContent, nil, nil, []*huma.Param{pathInteger("id")}, false},

		{http.MethodGet, "/api/keys/accounts", "list-key-account-options", "List account options", "API Keys", user, http.StatusOK, nil, reflect.TypeFor[AccountOptionListResponse](), nil, false},
		{http.MethodGet, "/api/keys", "list-api-keys", "List current user's API keys", "API Keys", user, http.StatusOK, nil, reflect.TypeFor[APIKeyListResponse](), nil, false},
		{http.MethodPost, "/api/keys", "create-api-key", "Create API key", "API Keys", user, http.StatusCreated, reflect.TypeFor[CreateAPIKeyRequest](), reflect.TypeFor[APIKeyResponse](), nil, false},
		{http.MethodGet, "/api/keys/{id}", "get-api-key", "Get API key", "API Keys", user, http.StatusOK, nil, reflect.TypeFor[APIKeyResponse](), []*huma.Param{pathInteger("id")}, false},
		{http.MethodDelete, "/api/keys/{id}", "delete-api-key", "Delete API key", "API Keys", user, http.StatusNoContent, nil, nil, []*huma.Param{pathInteger("id")}, false},
		{http.MethodGet, "/api/keys/{id}/config/{client}", "get-api-key-client-config", "Get client configuration", "API Keys", user, http.StatusOK, nil, reflect.TypeFor[CCSwitchClientConfig](), []*huma.Param{pathInteger("id"), pathString("client")}, false},
		{http.MethodPut, "/api/keys/{id}/config/{client}", "update-api-key-client-config", "Update client configuration", "API Keys", user, http.StatusOK, reflect.TypeFor[CCSwitchClientConfig](), reflect.TypeFor[CCSwitchClientConfig](), []*huma.Param{pathInteger("id"), pathString("client")}, false},

		{http.MethodGet, "/api/calls", "list-calls", "List call records", "Calls", user, http.StatusOK, nil, reflect.TypeFor[CallListResponse](), callQueryParameters(), false},
		{http.MethodGet, "/api/calls/{day}/{id}", "get-call", "Get call record", "Calls", user, http.StatusOK, nil, reflect.TypeFor[database.PersistedCallTrace](), []*huma.Param{pathString("day"), pathInteger("id")}, false},
		{http.MethodGet, "/api/usage/subscriptions", "get-subscription-usage", "Get usage by subscription", "Usage", admin, http.StatusOK, nil, reflect.TypeFor[UsageResponse](), usageQueryParameters(false), false},
		{http.MethodGet, "/api/usage/users", "get-user-usage", "Get usage by user", "Usage", admin, http.StatusOK, nil, reflect.TypeFor[UsageResponse](), usageQueryParameters(true), false},

		{http.MethodPost, "/api/oauth/{service}/start", "start-oauth", "Start OAuth flow", "OAuth", admin, http.StatusOK, reflect.TypeFor[OAuthStartRequest](), reflect.TypeFor[oauth.StartResult](), []*huma.Param{pathString("service")}, false},
		{http.MethodGet, "/api/oauth/{service}/status/{session}", "get-oauth-status", "Get OAuth flow status", "OAuth", admin, http.StatusOK, nil, reflect.TypeFor[OAuthStatusResponse](), []*huma.Param{pathString("service"), pathString("session")}, false},
		{http.MethodPost, "/api/oauth/{service}/complete/{session}", "complete-oauth", "Complete OAuth flow", "OAuth", admin, http.StatusOK, reflect.TypeFor[OAuthCompleteRequest](), reflect.TypeFor[OAuthResultIDResponse](), []*huma.Param{pathString("service"), pathString("session")}, false},
		{http.MethodPost, "/api/oauth/{service}/poll/{session}", "poll-oauth", "Poll OAuth device flow", "OAuth", admin, http.StatusOK, nil, reflect.TypeFor[OAuthStatusResponse](), []*huma.Param{pathString("service"), pathString("session")}, false},
		{http.MethodGet, "/api/oauth/results/{id}", "take-oauth-result", "Take OAuth result", "OAuth", admin, http.StatusOK, nil, reflect.TypeFor[OAuthResultResponse](), []*huma.Param{pathString("id")}, false},
	}

	for i := range operations {
		registerManagementOperation(api, &operations[i], dispatch)
	}
}

func registerManagementOperation(api huma.API, contract *managementOperation, dispatch managementDispatcher) {
	roles := []string{contract.Role}
	if contract.Role == "user" {
		roles = []string{"admin", "user"}
	}
	op := &huma.Operation{
		Method:        contract.Method,
		Path:          contract.Path,
		OperationID:   contract.OperationID,
		Summary:       contract.Summary,
		Tags:          []string{contract.Tag},
		DefaultStatus: contract.Status,
		Parameters:    contract.Parameters,
		Deprecated:    contract.Deprecated,
		Responses:     managementResponses(api, contract.Status, contract.ResponseType),
		Extensions:    map[string]any{"x-unisub-roles": roles},
	}
	if contract.Role == "public" {
		op.Security = []map[string][]string{}
	}
	if contract.RequestType != nil {
		op.RequestBody = &huma.RequestBody{
			Required: true,
			Content: map[string]*huma.MediaType{
				"application/json": {Schema: schemaFor(api, contract.RequestType)},
			},
		}
	}
	if contract.OperationID == "poll-oauth" {
		op.Responses[strconv.Itoa(http.StatusAccepted)] = &huma.Response{
			Description: http.StatusText(http.StatusAccepted),
			Content: map[string]*huma.MediaType{
				"application/json": {Schema: schemaFor(api, contract.ResponseType)},
			},
		}
	}
	api.OpenAPI().AddOperation(op)
	if dispatch != nil {
		api.Adapter().Handle(op, func(ctx huma.Context) {
			req, writer := humago.Unwrap(ctx)
			dispatch(writer, req)
		})
	}
}

func managementResponses(api huma.API, status int, responseType reflect.Type) map[string]*huma.Response {
	responses := map[string]*huma.Response{}
	success := &huma.Response{Description: http.StatusText(status)}
	if responseType != nil {
		success.Content = map[string]*huma.MediaType{
			"application/json": {Schema: schemaFor(api, responseType)},
		}
	}
	responses[strconv.Itoa(status)] = success
	errorSchema := schemaFor(api, reflect.TypeFor[ErrorResponse]())
	for _, code := range []int{
		http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
		http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusRequestEntityTooLarge,
		http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusNotImplemented, http.StatusBadGateway,
	} {
		responses[strconv.Itoa(code)] = &huma.Response{
			Description: http.StatusText(code),
			Content: map[string]*huma.MediaType{
				"application/json": {Schema: errorSchema},
			},
		}
	}
	return responses
}

func schemaFor(api huma.API, typ reflect.Type) *huma.Schema {
	return api.OpenAPI().Components.Schemas.Schema(typ, true, typ.Name())
}

func pathInteger(name string) *huma.Param {
	return &huma.Param{Name: name, In: "path", Required: true, Schema: &huma.Schema{Type: huma.TypeInteger, Minimum: new(float64(1))}}
}

func pathString(name string) *huma.Param {
	return &huma.Param{Name: name, In: "path", Required: true, Schema: &huma.Schema{Type: huma.TypeString, MinLength: new(1)}}
}

func queryString(name string) *huma.Param {
	return &huma.Param{Name: name, In: "query", Schema: &huma.Schema{Type: huma.TypeString}}
}

func queryInteger(name string) *huma.Param {
	return &huma.Param{Name: name, In: "query", Schema: &huma.Schema{Type: huma.TypeInteger}}
}

func usageQueryParameters(includeSubscription bool) []*huma.Param {
	params := []*huma.Param{queryString("range"), queryString("from"), queryString("to")}
	if includeSubscription {
		params = append(params, queryInteger("subscription_id"))
	}
	return params
}

func callQueryParameters() []*huma.Param {
	return []*huma.Param{
		queryString("q"), queryInteger("account_id"), queryInteger("code"),
		queryString("range"), queryString("from"), queryString("to"),
		queryInteger("page"), queryInteger("page_size"), queryInteger("mine"),
	}
}
