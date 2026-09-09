package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/gin-gonic/gin"
)

type routeKind int

const (
	routeModels routeKind = iota
	routeMessages
	routeCountTokens
	routeResponses
)

var safePathSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func (s *Server) proxyHandler(expectedProvider string, kind routeKind, pathParam string) gin.HandlerFunc {
	return func(c *gin.Context) {
		rl := s.beginProxyRequestLog(c)
		defer s.finalizeProxyRequestLog(c, rl)

		account, credentials, ok := s.resolveProxyAccount(c, expectedProvider, kind, rl)
		if !ok {
			return
		}
		body, suffix, ok := s.readAndValidateProxyBody(c, kind, pathParam, rl)
		if !ok {
			return
		}
		upstreamURL, ok := s.resolveProxyUpstreamURL(c, account.Provider, kind, suffix)
		if !ok {
			return
		}

		release, err := s.acquire(c.Request.Context(), account.ID, account.ConcurrencyLimit, s.queueTimeout(account.Subscription))
		if err != nil {
			if errors.Is(err, errConcurrencyQueueTimeout) {
				apiError(c, http.StatusTooManyRequests, "concurrency_limited", "account concurrency queue wait timed out")
			}
			return
		}
		defer release()

		client, err := s.clientForSubscription(account.Subscription)
		if err != nil {
			apiError(c, 502, "proxy_error", err.Error())
			return
		}
		response, ok := s.forwardUpstreamWithRefresh(c, client, proxyForwardInput{
			account:                account,
			credentials:            credentials,
			kind:                   kind,
			upstreamURL:            upstreamURL,
			body:                   body,
			clientHadContentLength: strings.TrimSpace(c.Request.Header.Get("Content-Length")) != "",
			requestLog:             &rl.log,
		})
		if !ok {
			return
		}
		defer response.Body.Close()
		s.writeProxyUpstreamResponse(c, account, response, &rl.log)
	}
}

func (s *Server) queueTimeout(account model.Subscription) time.Duration {
	if account.ConcurrencyQueueTimeoutSeconds > 0 {
		return time.Duration(account.ConcurrencyQueueTimeoutSeconds) * time.Second
	}
	return s.cfg.ConcurrencyQueueTimeout
}

func (s *Server) upstreamURL(provider model.Provider, kind routeKind, suffix string) (string, error) {
	switch provider {
	case model.ProviderClaude:
		switch kind {
		case routeModels:
			return s.cfg.Providers.ClaudeModels, nil
		case routeMessages:
			return strings.TrimRight(s.cfg.Providers.ClaudeAPI, "/") + "/messages", nil
		case routeCountTokens:
			return strings.TrimRight(s.cfg.Providers.ClaudeAPI, "/") + "/messages/count_tokens", nil
		}
	case model.ProviderCodex:
		if kind == routeModels {
			return s.cfg.Providers.CodexModels, nil
		}
		if kind == routeResponses {
			return strings.TrimRight(s.cfg.Providers.CodexAPI, "/") + suffix, nil
		}
	case model.ProviderGrok:
		if kind == routeModels {
			return s.cfg.Providers.GrokModels, nil
		}
		if kind == routeResponses {
			return strings.TrimRight(s.cfg.Providers.GrokAPI, "/") + suffix, nil
		}
	}
	return "", fmt.Errorf("endpoint is not supported by %s", provider)
}

var errBodyTooLarge = errors.New("request body too large")

func readBody(request *http.Request, max int64) ([]byte, error) {
	if request.Body == nil {
		return nil, nil
	}
	reader := io.LimitReader(request.Body, max+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, errors.New("could not read request body")
	}
	if int64(len(body)) > max {
		return nil, errBodyTooLarge
	}
	return body, nil
}

func hasStringModel(body []byte) bool {
	var envelope struct {
		Model string `json:"model"`
	}
	return json.Unmarshal(body, &envelope) == nil && strings.TrimSpace(envelope.Model) != ""
}

func validateResponseSubpath(suffix, escapedPath string) error {
	if suffix == "" || !strings.HasPrefix(suffix, "/") || strings.Contains(escapedPath, "%") {
		return errors.New("invalid prefix")
	}
	segments := strings.Split(strings.TrimPrefix(suffix, "/"), "/")
	if len(segments) == 0 || len(segments) > 8 {
		return errors.New("invalid segment count")
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || len(segment) > 128 || !safePathSegment.MatchString(segment) {
			return errors.New("invalid path segment")
		}
	}
	return nil
}

func relaySSE(writer gin.ResponseWriter, source io.Reader) {
	reader := bufio.NewReader(source)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			_, _ = writer.Write(line)
			writer.Flush()
		}
		if err != nil {
			return
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func validateOfficialURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("invalid upstream URL")
	}
	return nil
}
