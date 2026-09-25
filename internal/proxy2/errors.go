package proxy2

import "errors"

var (
	ErrProxyGroupNotFound = errors.New("proxy group not found")
	ErrNoAvailableProxy   = errors.New("proxy group has no available proxy")
	ErrManagerNotOpen     = errors.New("proxy manager is not open")
	ErrManagerClosed      = errors.New("proxy manager is closed")
)
