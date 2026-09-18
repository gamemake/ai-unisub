package proxy

import (
	"ai-unisub/internal/common"
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var logURL = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s"'<>]+`)
var logSecret = regexp.MustCompile(`(?i)(["']?(?:authorization|proxy-authorization|cookie|set-cookie|username|password|passwd|access_token|refresh_token|id_token|api[_-]?key|token)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\r\n,;]+)`)

func safeAddress(address string) string {
	if address == "" {
		return ""
	}
	u, err := url.Parse(address)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "<invalid>"
	}
	return u.Scheme + "://" + u.Host
}

// Do not log request/response bodies or headers. URL paths and queries can also
// carry tokens; retain only the authority, without userinfo.
func safeLogText(value string) string {
	value = logURL.ReplaceAllStringFunc(value, safeAddress)
	return logSecret.ReplaceAllString(value, "${1}<redacted>")
}

func safeError(err error, address string) string {
	if err == nil {
		return ""
	}
	// net/http's wrapper contains the entire target URL. Keep the operation and
	// underlying cause, not its URL (including potentially sensitive queries).
	text := err.Error()
	if ue, ok := errors.AsType[*url.Error](err); ok {
		text = ue.Op + ": " + ue.Err.Error()
	}
	if u, parseErr := url.Parse(address); parseErr == nil && u.User != nil {
		secrets := []string{u.User.String(), u.User.Username()}
		if password, ok := u.User.Password(); ok {
			secrets = append(secrets, password)
		}
		for _, secret := range secrets {
			if secret != "" {
				text = strings.ReplaceAll(text, secret, "<redacted>")
			}
		}
	}
	return safeLogText(text)
}

// LogError records a failure at its owning boundary. Callers must not log the
// same propagated error again. It does not update proxy health or statistics.
func LogError(operation string, e *Endpoint, app string, err error) {
	logOperationError(operation, e.String(), 0, app, err)
}

func logOperationError(operation, address string, group int, app string, err error, details ...string) {
	if err == nil {
		return
	}
	event := "operation_failed"
	if errors.Is(err, context.Canceled) {
		event = "operation_canceled"
	} else if errors.Is(err, ErrClosed) {
		event = "manager_closed"
	}
	msg := fmt.Sprintf("operation=%q proxy=%q group=%d app=%q error=%q details=%q", operation, safeAddress(address), group, safeLogText(app), safeError(err, address), safeLogText(strings.Join(details, " ")))
	if event == "operation_failed" {
		common.ModuleLogger("proxy").Error(event, msg)
	} else {
		common.ModuleLogger("proxy").Info(event, msg)
	}
}

func logResult(e *Endpoint, app string, class ErrorClass, cause error, status int) {
	if e == nil || (class == Success && cause == nil && status < 400) {
		return
	}
	event := "proxy_error"
	if class == Canceled {
		event = "request_canceled"
	}
	msg := fmt.Sprintf("proxy=%q app=%q class=%q status=%d error=%q", safeAddress(e.String()), safeLogText(app), safeLogText(string(class)), status, safeError(cause, e.String()))
	if class == Canceled {
		common.ModuleLogger("proxy").Info(event, msg)
	} else {
		common.ModuleLogger("proxy").Warn(event, msg)
	}
}

// ReportResult uses detailed reporting when supported, retaining compatibility
// with resolvers that implement only the original ReportProxy interface.
func ReportResult(reporter interface {
	ReportProxy(*Endpoint, string, ErrorClass) error
}, e *Endpoint, app string, class ErrorClass, cause error, status int) error {
	if detailed, ok := reporter.(interface {
		ReportProxyResult(*Endpoint, string, ErrorClass, error, int) error
	}); ok {
		return detailed.ReportProxyResult(e, app, class, cause, status)
	}
	logResult(e, app, class, cause, status)
	err := reporter.ReportProxy(e, app, class)
	LogError("report_result", e, app, err)
	return err
}

func logState(k stateKey, before State, after *State, source, reason string) {
	event := "state_changed"
	if before.Status == after.Status {
		if before.CooldownUntil.Equal(after.CooldownUntil) {
			return
		}
		event = "cooldown_updated"
	}
	scope := "network"
	if k.app != "" {
		scope = "application"
	}
	common.ModuleLogger("proxy").Info(event, fmt.Sprintf("proxy=%q scope=%s app=%q from=%s to=%s source=%s reason=%s consecutive_failures=%d recovery_successes=%d cooldown_until=%q backoff=%d in_flight=%d", safeAddress(k.address), scope, safeLogText(k.app), before.Status, after.Status, source, reason, after.ConsecutiveFailures, after.RecoverySuccesses, after.CooldownUntil, after.Backoff, after.InFlight))
}
