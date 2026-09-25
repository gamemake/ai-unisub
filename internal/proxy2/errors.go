package proxy2

import "errors"

var (
	ErrProxyGroupNotFound = errors.New("proxy group not found")
	ErrNoAvailableProxy   = errors.New("proxy group has no available proxy")
	ErrManagerNotOpen     = errors.New("proxy manager is not open")
	ErrManagerClosed      = errors.New("proxy manager is closed")

	errApplicationDetectedProxyFailure  = errors.New("application detected a proxy failure")
	errDatabaseDidNotAssignProxyGroupID = errors.New("database did not assign a proxy group ID")
	errDuplicateProxyURL                = errors.New("duplicate proxy URL")
	errInvalidProxyURL                  = errors.New("invalid proxy URL")
	errNoProxies                        = errors.New("at least one proxy is required")
	errProxyDatabaseNil                 = errors.New("proxy database is nil")
	errProxyGroupNameRequired           = errors.New("proxy group name is required")
	errProxyURLHasQueryOrFragment       = errors.New("proxy URL must not contain a query or fragment")
	errRequestNil                       = errors.New("request is nil")
	errRequestURLNil                    = errors.New("request URL is nil")
	errUnsupportedProxyURLScheme        = errors.New("unsupported proxy URL scheme")
)
