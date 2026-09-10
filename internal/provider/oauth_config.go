package provider

// OAuthConfig is the only authentication-specific part shared by the three
// provider configurations. The credential itself lives in the credential
// store; provider instances keep only its opaque reference.
type OAuthConfig struct {
	CredentialID string `json:"credential_id"`
}
