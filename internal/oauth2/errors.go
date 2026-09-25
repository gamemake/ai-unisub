package oauth2

import "errors"

var (
	ErrSessionNotFound      = errors.New("oauth session not found")
	ErrSessionExpired       = errors.New("oauth session expired")
	ErrStateMismatch        = errors.New("oauth state mismatch")
	ErrUnsupportedFlow      = errors.New("unsupported oauth flow")
	ErrAuthorizationPending = errors.New("oauth authorization pending")
	ErrSlowDown             = errors.New("oauth authorization polling should slow down")

	errCredentialNil                      = errors.New("credential is nil")
	errCredentialNoRefreshToken           = errors.New("credential has no refresh token")
	errDeviceCodeRequired                 = errors.New("device code is required")
	errDummyDeviceCodeNotFound            = errors.New("dummy device code not found")
	errInvalidOAuthAdapter                = errors.New("invalid oauth adapter")
	errOAuthAdapterNoAuthorizationURL     = errors.New("oauth adapter returned no authorization URL")
	errOAuthAdapterNoDeviceCode           = errors.New("oauth adapter returned no device code")
	errOAuthDatabaseNotConfigured         = errors.New("oauth database is not configured")
	errOAuthResponseNoAccessToken         = errors.New("oauth response has no access token")
	errOAuthResponseTooLargeOrUnreadable  = errors.New("oauth response is too large or unreadable")
	errOAuthServiceRevokeUnsupported      = errors.New("oauth service does not support revoke")
	errProxyGroupNegative                 = errors.New("proxy group must not be negative")
	errProxyManagerNotConfigured          = errors.New("proxy manager is not configured")
	errRedirectURIRequiredForPKCE         = errors.New("redirect URI is required for PKCE authorization")
	errRequestNil                         = errors.New("request is nil")
	errXAIDeviceAuthorizationNoDeviceCode = errors.New("xAI device authorization returned no device code")
)
