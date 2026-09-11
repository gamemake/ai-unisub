package service

import (
	"ai-unisub/internal/common"
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

type route struct {
	pattern string
	options RouteOptions
	handler http.Handler
}
type router struct {
	routes  []route
	problem error
}
type requestIDKey struct{}

func RequestIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(requestIDKey{}).(string)
	return v, ok
}

func newRouter() *router { return &router{} }
func (r *router) add(p string, o RouteOptions, h http.Handler) {
	if r.problem != nil {
		return
	}
	if p == "" || !strings.HasPrefix(p, "/") || h == nil {
		r.problem = errors.New("invalid route registration")
		return
	}
	for _, x := range r.routes {
		if x.pattern == p {
			r.problem = errors.New("duplicate route: " + p)
			return
		}
	}
	r.routes = append(r.routes, route{p, o, h})
}
func (r *router) err() error { return r.problem }
func (r *router) truncate(n int) {
	if n < len(r.routes) {
		r.routes = r.routes[:n]
	}
	r.problem = nil
}
func (r *router) handler(s *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		var best *route
		for i := range r.routes {
			x := &r.routes[i]
			ok := q.URL.Path == x.pattern || (strings.HasSuffix(x.pattern, "/") && strings.HasPrefix(q.URL.Path, x.pattern))
			if ok && (best == nil || len(x.pattern) > len(best.pattern)) {
				best = x
			}
		}
		if best == nil {
			http.NotFound(w, q)
			return
		}
		h := s.authSvc.middleware(best.options.Auth, best.handler)
		next := h
		h = http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
			started := time.Now()
			statusWriter := &responseWriter{ResponseWriter: w}
			defer func() {
				if recovered := recover(); recovered != nil {
					log.Printf("service request panic route=%s method=%s path=%s panic=%v", best.options.Name, q.Method, q.URL.Path, recovered)
					if statusWriter.status == 0 {
						common.WriteError(statusWriter, http.StatusInternalServerError, common.MessageInternalServerError)
					}
				}
				if statusWriter.status == 0 {
					statusWriter.status = http.StatusOK
				}
				log.Printf("service request route=%s method=%s path=%s status=%d duration=%s", best.options.Name, q.Method, q.URL.Path, statusWriter.status, time.Since(started))
			}()
			requestID := q.Header.Get("X-Request-ID")
			if requestID == "" {
				var raw [16]byte
				if _, err := rand.Read(raw[:]); err == nil {
					requestID = hex.EncodeToString(raw[:])
				} else {
					requestID = time.Now().UTC().Format("20060102150405.000000000")
				}
			}
			w.Header().Set("X-Request-ID", requestID)
			next.ServeHTTP(statusWriter, q.WithContext(context.WithValue(q.Context(), requestIDKey{}, requestID)))
		})
		h.ServeHTTP(w, q)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		if w.status == 0 {
			w.WriteHeader(http.StatusOK)
		}
		f.Flush()
	}
}
func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}
func (w *responseWriter) ReadFrom(r io.Reader) (int64, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return f.ReadFrom(r)
	}
	return io.Copy(w.ResponseWriter, r)
}
func (w *responseWriter) Push(target string, opts *http.PushOptions) error {
	if p, ok := w.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *responseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseWriter) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(value)
}
