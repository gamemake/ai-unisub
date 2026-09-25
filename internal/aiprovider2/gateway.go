package aiprovider2

import "net/http"

type GatewayListener interface {
}

type Gateway interface {
	Handle(req *http.Request)
}

type gateway struct {
}

func newGateway() Gateway {
	return &gateway{}
}
