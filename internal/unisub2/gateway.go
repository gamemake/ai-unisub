package unisub

import (
	"net/http"

	"ai-unisub/internal/service2"
)

// GatewayModule exposes the gateway owned by aiprovider2. Authentication,
// account selection, upstream dispatch, proxying, and call recording all stay
// inside that package so the application layer does not duplicate them.
type GatewayModule struct{}

func NewGatewayModule() *GatewayModule { return &GatewayModule{} }
func (m *GatewayModule) Name() string  { return "gateway" }
func (m *GatewayModule) Close() error  { return nil }

func (m *GatewayModule) Init(ctx service.ModuleContext) error {
	handler := ctx.AIProviders().Handler()
	ctx.Handle("/v1/", service.RouteOptions{Auth: service.AuthNone, Name: "gateway"}, handler)
	return nil
}

func isVideoGenerationRequest(r *http.Request) bool {
	path := r.URL.Path
	return path == "/v1/videos" || path == "/v1/video/generations"
}
