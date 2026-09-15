package aiprovider

import (
	"context"
	"net/http"
)

type responseWriterKey struct{}

// WithResponseWriter lets an HTTP caller receive headers, status and streaming
// bytes while retaining the existing AIProvider.Handle/recorder contract.
func WithResponseWriter(ctx context.Context, w http.ResponseWriter) context.Context {
	return context.WithValue(ctx, responseWriterKey{}, w)
}
func responseWriter(ctx context.Context) http.ResponseWriter {
	w, _ := ctx.Value(responseWriterKey{}).(http.ResponseWriter)
	return w
}
