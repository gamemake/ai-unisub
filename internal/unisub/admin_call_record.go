package unisub

import (
	"context"
	"net/http"
	"time"

	framework "ai-unisub/internal/service"
)

// aiprovider records quota and model-listing upstream calls itself. Keep the
// request context helper so handlers retain cancellation and deadlines.
func adminCallContext(_ framework.ModuleContext, r *http.Request, _ int, _ time.Time) context.Context {
	return r.Context()
}
