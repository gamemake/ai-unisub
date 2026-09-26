package service

import (
	"encoding/json"
	"errors"
	"net/http"
)

const (
	MessageUnauthorized        = "unauthorized"
	MessageForbidden           = "forbidden"
	MessageInternalServerError = "internal server error"
)

var (
	errAdminCredentialsRequired  = errors.New("admin username and password are required")
	errDatabaseRequired          = errors.New("database is required")
	errDatabaseURLRequired       = errors.New("database URL is required")
	errDuplicateRoute            = errors.New("duplicate route")
	errInvalidRouteRegistration  = errors.New("invalid route registration")
	errOAuthResultIdentityNeeded = errors.New("subject and OAuth service are required")
	errOAuthResultNotFound       = errors.New("oauth result not found")
	errServiceClosed             = errors.New("service is closed")
	errServiceModuleNil          = errors.New("service module is nil")
	errUserNotFound              = errors.New("user not found")
	errUserRequired              = errors.New("user is required")
)

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: message})
}
