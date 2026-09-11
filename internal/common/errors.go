package common

import (
	"encoding/json"
	"net/http"
)

// PublicErrorMessage is a stable, client-safe message used by Web handlers.
// Internal errors must be mapped to one of these messages before being sent
// to a client.
const (
	MessageUnauthorized                 = "unauthorized"
	MessageForbidden                    = "forbidden"
	MessageInternalServerError          = "internal server error"
	MessageInvalidPassword              = "invalid password"
	MessageOldPasswordIncorrect         = "old password is incorrect"
	MessageCouldNotSavePassword         = "could not save password"
	MessageCouldNotListUsers            = "could not list users"
	MessagePasswordTooShort             = "password must contain at least 8 characters"
	MessageCannotResetCurrentUser       = "cannot reset current user password"
	MessageCouldNotSaveUser             = "could not save user"
	MessageUserNotFound                 = "user not found"
	MessageInvalidUser                  = "invalid user"
	MessageUserNameExists               = "user name already exists"
	MessageCouldNotGenerateUserID       = "could not generate user ID"
	MessageInvalidRole                  = "invalid role"
	MessageCannotDemoteCurrentUser      = "cannot demote or disable current user"
	MessageCannotDeleteCurrentUser      = "cannot delete current user"
	MessageCouldNotDeleteUser           = "could not delete user"
	MessageCouldNotListProviders        = "could not list providers"
	MessageProviderAndConfigRequired    = "provider and config are required"
	MessageInvalidProviderConfig        = "invalid provider config"
	MessageCredentialNotFound           = "credential not found"
	MessageCouldNotGenerateCredentialID = "could not generate credential ID"
	MessageCouldNotSaveCredential       = "could not save credential"
	MessageCouldNotGenerateProviderID   = "could not generate provider ID"
	MessageCouldNotSaveProvider         = "could not save provider"
	MessageProviderNotFound             = "provider not found"
	MessageCouldNotInspectProviderRefs  = "could not inspect provider references"
	MessageProviderStillReferenced      = "provider is still referenced by an API key"
	MessageCouldNotDeleteProvider       = "could not delete provider"
	MessageProviderTypeCannotChange     = "provider type cannot be changed"
	MessageCouldNotListAPIKeys          = "could not list API keys"
	MessageAPIKeyNameRequired           = "name is required and must be at most 64 characters"
	MessageAccountIDRequired            = "account_id is required"
	MessageValidSecondsNegative         = "valid_seconds must not be negative"
	MessageCouldNotGenerateAPIKeyID     = "could not generate API key ID"
	MessageCouldNotGenerateAPIKey       = "could not generate API key"
	MessageCouldNotSaveAPIKey           = "could not save API key"
	MessageCouldNotDeleteAPIKey         = "could not delete API key"
	MessageAPIKeyNotFound               = "API key not found"
	MessageInvalidPage                  = "invalid page"
	MessagePageSizeRange                = "page_size must be between 10 and 100"
	MessageCouldNotQueryCallRecords     = "could not query call records"
	MessageInvalidJSONBody              = "invalid JSON body"
	MessageUnsupportedOAuthService      = "unsupported OAuth service"
	MessageInvalidOAuthSession          = "invalid OAuth session"
	MessageInvalidOAuthCredential       = "invalid OAuth credential"
	MessageCouldNotStoreOAuthResult     = "could not store OAuth result"
	MessageOAuthResultNotFound          = "oauth result not found"
	MessageOAuthSessionNotFound         = "oauth session not found"
	MessageInvalidOAuthState            = "invalid OAuth state"
	MessageUnsupportedOAuthFlow         = "unsupported OAuth flow"
	MessageOAuthUpstreamFailed          = "OAuth upstream request failed"
	MessageMethodNotAllowed             = "method not allowed"
	MessageUsernamePasswordRequired     = "username and password are required"
	MessageCouldNotAuthenticateUser     = "could not authenticate user"
	MessageCouldNotCreateSession        = "could not create session"
	MessageInvalidUsernameOrPassword    = "invalid username or password"
	MessageInvalidProxy                 = "invalid proxy URL"
	MessageOAuthAuthorizationFailed     = "OAuth authorization failed; please restart authorization"
)

// WriteError writes a client-safe error message as JSON.
func WriteError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: message})
}
