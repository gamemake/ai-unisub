package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type adminClaims struct {
	Subject   string `json:"sub"`
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
}

type tokenSigner struct{ key []byte }

func (s tokenSigner) issue(subject string, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload, err := json.Marshal(adminClaims{Subject: subject, IssuedAt: now.Unix(), ExpiresAt: now.Add(ttl).Unix()})
	if err != nil {
		return "", err
	}
	unsigned := rawURL(header) + "." + rawURL(payload)
	signature := s.sign(unsigned)
	return unsigned + "." + rawURL(signature), nil
}

func (s tokenSigner) verify(token string) (adminClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return adminClaims{}, errors.New("invalid token")
	}
	unsigned := parts[0] + "." + parts[1]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(signature, s.sign(unsigned)) {
		return adminClaims{}, errors.New("invalid token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return adminClaims{}, errors.New("invalid token")
	}
	var claims adminClaims
	if json.Unmarshal(payload, &claims) != nil || claims.Subject == "" || time.Now().Unix() >= claims.ExpiresAt {
		return adminClaims{}, errors.New("expired token")
	}
	return claims, nil
}

func (s tokenSigner) sign(value string) []byte {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func rawURL(value []byte) string { return base64.RawURLEncoding.EncodeToString(value) }
