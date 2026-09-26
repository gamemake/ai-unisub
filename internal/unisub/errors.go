package unisub

import (
	"encoding/json"
	"errors"
	"net/http"
)

const (
	MessageUnauthorized                  = "unauthorized"
	MessageForbidden                     = "forbidden"
	MessageInternalServerError           = "internal server error"
	MessageWebBuildUnavailable           = "web build unavailable"
	MessageMethodNotAllowed              = "method not allowed"
	MessageInvalidJSONBody               = "invalid JSON body"
	MessageInvalidPassword               = "invalid password"
	MessageOldPasswordIncorrect          = "old password is incorrect"
	MessageCouldNotSavePassword          = "could not save password"
	MessageCouldNotListUsers             = "could not list users"
	MessagePasswordTooShort              = "password must contain at least 8 characters"
	MessageCannotResetCurrentUser        = "cannot reset current user password"
	MessageCouldNotSaveUser              = "could not save user"
	MessageUserNotFound                  = "user not found"
	MessageInvalidUser                   = "invalid user"
	MessageUserNameExists                = "user name already exists"
	MessageInvalidRole                   = "invalid role"
	MessageCannotDemoteCurrentUser       = "cannot demote or disable current user"
	MessageCannotDeleteCurrentUser       = "cannot delete current user"
	MessageCouldNotDeleteUser            = "could not delete user"
	MessageCouldNotListAIProviders       = "could not list providers"
	MessageAIProviderAndConfigRequired   = "provider and config are required"
	MessageInvalidAccountConfig          = "invalid provider config"
	MessageAIProviderNotFound            = "provider not found"
	MessageCouldNotInspectAIProviderRefs = "could not inspect provider references"
	MessageAIProviderStillReferenced     = "provider is still referenced by an API key"
	MessageCouldNotDeleteAIProvider      = "could not delete provider"
	MessageAIProviderTypeCannotChange    = "provider type cannot be changed"
	MessageCouldNotListAPIKeys           = "could not list API keys"
	MessageAPIKeyNameRequired            = "name is required and must be at most 64 characters"
	MessageAccountIDRequired             = "account_id is required"
	MessageValidSecondsNegative          = "valid_seconds must not be negative"
	MessageCouldNotGenerateAPIKey        = "could not generate API key"
	MessageCouldNotSaveAPIKey            = "could not save API key"
	MessageCouldNotDeleteAPIKey          = "could not delete API key"
	MessageAPIKeyNotFound                = "API key not found"
	MessageInvalidPage                   = "invalid page"
	MessagePageSizeRange                 = "page_size must be between 10 and 100"
	MessageCouldNotQueryCallRecords      = "could not query call records"
	MessageUnsupportedOAuthService       = "unsupported OAuth service"
	MessageInvalidOAuthSession           = "invalid OAuth session"
	MessageInvalidOAuthCredential        = "invalid OAuth credential"
	MessageCouldNotStoreOAuthResult      = "could not store OAuth result"
	MessageOAuthResultNotFound           = "oauth result not found"
	MessageOAuthSessionNotFound          = "oauth session not found"
	MessageInvalidOAuthState             = "invalid OAuth state"
	MessageUnsupportedOAuthFlow          = "unsupported OAuth flow"
	MessageOAuthUpstreamFailed           = "OAuth upstream request failed"
	MessageOAuthAuthorizationFailed      = "OAuth authorization failed; please restart authorization"
	MessageUsernamePasswordRequired      = "username and password are required"
	MessageCouldNotAuthenticateUser      = "could not authenticate user"
	MessageInvalidUsernameOrPassword     = "invalid username or password"
)

var (
	errInvalidCredential         = errors.New("invalid credential")
	errInvalidUsageEndDate       = errors.New("invalid usage end date")
	errInvalidUsageRange         = errors.New("invalid usage range")
	errInvalidUsageStartDate     = errors.New("invalid usage start date")
	errProviderConfigObject      = errors.New("provider config must be a JSON object")
	errUnsupportedCCSwitchClient = errors.New("unsupported CC Switch client")
	errUsageRangeReversed        = errors.New("usage start date must not be after end date")
)

func WriteError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: message})
}
