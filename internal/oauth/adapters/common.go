package adapters

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"ai-unisub/internal/oauth"
)

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Expires      int    `json:"expires_in"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"`
}

func credential(token tokenResponse) (*oauth.OAuthCredential, error) {
	if token.AccessToken == "" {
		return nil, errors.New("oauth response has no access token")
	}
	result := &oauth.OAuthCredential{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, TokenType: token.TokenType}
	if result.TokenType == "" {
		result.TokenType = "Bearer"
	}
	if token.Expires > 0 {
		result.ExpiresAt = time.Now().Add(time.Duration(token.Expires) * time.Second).UTC()
	}
	return result, nil
}

func decodeClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		b, err = base64.URLEncoding.DecodeString(parts[1])
	}
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(b, &claims) != nil {
		return nil
	}
	return claims
}

func claimString(claims map[string]any, key string) string {
	if claims == nil {
		return ""
	}
	v, _ := claims[key].(string)
	return v
}
