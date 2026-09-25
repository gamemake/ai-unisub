package oauth2

import (
	"cmp"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxOAuthResponseBytes = 1 << 20

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	IDToken      string `json:"id_token"`
}

type oauthRequestError struct {
	Code        string
	Description string
	StatusCode  int
}

func (e *oauthRequestError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("oauth request failed: %s", e.Code)
	}
	return fmt.Sprintf("oauth request failed with status %d", e.StatusCode)
}

func pkceChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func selectHTTPClient(configured, supplied *http.Client) *http.Client {
	if supplied != nil {
		return supplied
	}
	if configured != nil {
		return configured
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func doOAuthRequest(client *http.Client, request *http.Request, output any) error {
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOAuthResponseBytes+1))
	if err != nil || len(body) > maxOAuthResponseBytes {
		return errOAuthResponseTooLargeOrUnreadable
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		failure := new(struct {
			Code        string `json:"error"`
			Description string `json:"error_description"`
		})
		_ = json.Unmarshal(body, failure)
		return &oauthRequestError{Code: failure.Code, Description: failure.Description, StatusCode: response.StatusCode}
	}
	if output == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, output)
}

func credentialFromToken(token tokenResponse) (*OAuthCredential, error) {
	if token.AccessToken == "" {
		return nil, errOAuthResponseNoAccessToken
	}
	credential := &OAuthCredential{
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		TokenType: cmp.Or(token.TokenType, "Bearer"),
	}
	if token.ExpiresIn > 0 {
		credential.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).UTC()
	}
	return credential, nil
}

func decodeJWTClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
	}
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	return claims
}

func claimString(claims map[string]any, key string) string {
	value, _ := claims[key].(string)
	return value
}
