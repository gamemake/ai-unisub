package oauth2

import "errors"

var (
	ErrSessionNotFound      = errors.New("oauth session not found")
	ErrSessionExpired       = errors.New("oauth session expired")
	ErrStateMismatch        = errors.New("oauth state mismatch")
	ErrUnsupportedFlow      = errors.New("unsupported oauth flow")
	ErrAuthorizationPending = errors.New("oauth authorization pending")
	ErrSlowDown             = errors.New("oauth authorization polling should slow down")
)
