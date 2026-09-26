package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"ai-unisub/internal/common"
	"ai-unisub/internal/database"
	"ai-unisub/internal/oauth"
	"ai-unisub/internal/proxy"
)

var errUsage = errors.New("invalid command usage")

const usageText = `Usage:
  oauth login <provider> [--output <file>]
  oauth status --file <file>
  oauth refresh --file <file>
  oauth revoke --file <file>
  oauth logout --file <file>

Providers:
  grok       Use Grok Device OAuth
  codex      Use Codex PKCE OAuth
  claude     Use Claude PKCE OAuth

Examples:
  oauth login grok --output .\oauth\grok.json
  oauth status --file .\oauth\grok.json
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, errUsage) {
			fmt.Fprint(os.Stderr, usageText)
			os.Exit(2)
		}
		common.ModuleLogger("cmd/oauth").Error("command_failed", err.Error())
		os.Exit(1)
	}
}

// cliServices maps the CLI provider names to OAuth service identifiers.
var cliServices = map[string]string{
	"grok":   oauth.OAuthServiceXAI,
	"codex":  oauth.OAuthServiceOpenAI,
	"claude": oauth.OAuthServiceAnthropic,
}

// newManager creates an OAuth manager backed by an in-memory database and a
// direct (group 0) proxy manager. The returned close function releases both.
func newManager() (oauth.OAuthManager, func(), error) {
	db, err := database.NewDatabase("sqlite::memory:")
	if err != nil {
		return nil, nil, err
	}
	if err := db.Open(); err != nil {
		return nil, nil, err
	}
	proxies := proxy.NewManager(db)
	if err := proxies.Open(); err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	closeAll := func() {
		_ = proxies.Close()
		_ = db.Close()
	}
	return oauth.NewManager(db, proxies), closeAll, nil
}

// resolveService accepts either a CLI provider name or an OAuth service identifier.
func resolveService(name string) string {
	if service, ok := cliServices[name]; ok {
		return service
	}
	return name
}

func run(args []string, stdout, stderr io.Writer) error {
	if err := common.InitLogging(common.LogConfig{Output: stderr}); err != nil {
		return err
	}
	if len(args) < 1 {
		return errUsage
	}
	command := args[0]
	switch command {
	case "login":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "Missing provider for login.")
			return errUsage
		}
		provider := args[1]
		output := option(args[2:], "output")
		if output == "" {
			output = option(args[2:], "file")
		}
		return login(provider, output, stdout, stderr)
	case "status", "refresh", "revoke", "logout":
		path := option(args[1:], "file")
		if path == "" {
			fmt.Fprintf(stderr, "Missing --file for %s.\n", command)
			return errUsage
		}
		return operate(command, path, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "Unknown OAuth command %q.\n", command)
		return errUsage
	}
}

func option(args []string, name string) string {
	for i, value := range args {
		if value == "--"+name && i+1 < len(args) {
			return args[i+1]
		}
		if after, ok := strings.CutPrefix(value, "--"+name+"="); ok {
			return after
		}
	}
	return ""
}

func login(provider, output string, stdout, stderr io.Writer) error {
	service, ok := cliServices[provider]
	if !ok {
		return fmt.Errorf("unsupported provider %q", provider)
	}
	common.ModuleLogger("cmd/oauth").Info("login_started", fmt.Sprintf("starting OAuth login: provider=%s", provider))
	callbackPath := "/callback"
	if service == oauth.OAuthServiceOpenAI {
		callbackPath = "/auth/callback"
	}
	var listener net.Listener
	var err error
	redirect := ""
	if service != oauth.OAuthServiceXAI {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		defer listener.Close()
		redirect = "http://" + listener.Addr().String() + callbackPath
		common.ModuleLogger("cmd/oauth").Info("callback_listening", fmt.Sprintf("callback server reserved: %s", redirect))
	}
	manager, closeManager, err := newManager()
	if err != nil {
		return err
	}
	defer closeManager()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	common.ModuleLogger("cmd/oauth").Info("authorization_started", fmt.Sprintf("requesting authorization from %s", provider))
	start, err := manager.Start(ctx, service, "", redirect, 0)
	if err != nil {
		common.ModuleLogger("cmd/oauth").Error("authorization_failed", fmt.Sprintf("authorization request failed: %v", err))
		return err
	}
	common.ModuleLogger("cmd/oauth").Info("authorization_completed", "authorization request completed")
	fmt.Fprintf(stderr, "OAuth authorization URL: %s\n", start.AuthorizationURL)
	if start.UserCode != "" {
		fmt.Fprintf(stderr, "User code: %s\n", start.UserCode)
	}
	if err := openBrowser(start.AuthorizationURL); err != nil {
		fmt.Fprintf(stderr, "Open the URL manually: %v\n", err)
	}

	if service == oauth.OAuthServiceXAI {
		pollCount := 0
		for {
			pollCount++
			common.ModuleLogger("cmd/oauth").Info("device_poll_started", fmt.Sprintf("polling device authorization: attempt=%d", pollCount))
			credential, err := manager.Poll(ctx, start.SessionID)
			if err == nil {
				common.ModuleLogger("cmd/oauth").Info("device_authorized", "device authorization completed")
				return finishCredential(credential, provider, output, stdout, stderr)
			}
			if !errors.Is(err, oauth.ErrAuthorizationPending) && !errors.Is(err, oauth.ErrSlowDown) {
				common.ModuleLogger("cmd/oauth").Error("device_authorization_failed", fmt.Sprintf("device authorization failed: %v", err))
				return err
			}
			common.ModuleLogger("cmd/oauth").Info("authorization_pending", "authorization is still pending")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}
	}
	callback := make(chan struct {
		code, state string
		err         error
	}, 1)
	server := &http.Server{ErrorLog: common.ModuleLogger("cmd/oauth").StandardLogger(slog.LevelError, "http_server_error"), Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != callbackPath {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("error") != "" {
			callback <- struct {
				code, state string
				err         error
			}{err: fmt.Errorf("oauth authorization failed: %s", q.Get("error"))}
			return
		}
		callback <- struct {
			code, state string
			err         error
		}{code: q.Get("code"), state: q.Get("state")}
		_, _ = io.WriteString(w, "OAuth authorization completed. You may close this window.")
	})}
	go server.Serve(listener)
	common.ModuleLogger("cmd/oauth").Info("callback_waiting", "waiting for browser callback")
	defer server.Shutdown(context.Background())
	select {
	case result := <-callback:
		if result.err != nil {
			common.ModuleLogger("cmd/oauth").Error("callback_failed", fmt.Sprintf("browser callback failed: %v", result.err))
			return result.err
		}
		common.ModuleLogger("cmd/oauth").Info("callback_received", "browser callback received")
		credential, err := manager.Complete(ctx, start.SessionID, result.code, result.state)
		if err != nil {
			common.ModuleLogger("cmd/oauth").Error("token_exchange_failed", fmt.Sprintf("token exchange failed: %v", err))
			return err
		}
		common.ModuleLogger("cmd/oauth").Info("login_completed", "OAuth login completed")
		return finishCredential(credential, provider, output, stdout, stderr)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func finishCredential(credential *oauth.OAuthCredential, provider, output string, stdout, stderr io.Writer) error {
	payload, err := credentialPayload(credential, provider)
	if err != nil {
		return err
	}
	if output != "" {
		raw, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return err
		}
		if err := (fileCredentialStore{}).SaveCredential(output, raw); err != nil {
			return err
		}
		common.ModuleLogger("cmd/oauth").Info("credential_saved", fmt.Sprintf("OAuth credential saved to %s", output))
		return nil
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}

func credentialPayload(credential *oauth.OAuthCredential, provider string) (map[string]any, error) {
	raw, err := json.Marshal(credential)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if payload == nil {
		payload = map[string]any{}
	}
	if provider != "" {
		payload["provider"] = provider
	}
	return payload, nil
}

func credentialFileService(raw []byte) string {
	var extra struct {
		Provider string `json:"provider"`
		Service  string `json:"service"`
	}
	_ = json.Unmarshal(raw, &extra)
	if extra.Provider != "" {
		return extra.Provider
	}
	return extra.Service
}

func operate(command, path string, args []string, stdout, stderr io.Writer) error {
	store := fileCredentialStore{}
	raw, err := store.LoadCredential(path)
	if err != nil {
		return err
	}
	var credential oauth.OAuthCredential
	if err := json.Unmarshal(raw, &credential); err != nil {
		return err
	}
	service := option(args, "provider")
	if service == "" {
		service = credentialFileService(raw)
	}
	if command != "status" && service == "" {
		return errors.New("credential file has no provider; pass --provider")
	}
	if command == "status" {
		state := "valid"
		if !credential.ExpiresAt.IsZero() {
			if time.Now().After(credential.ExpiresAt) {
				state = "expired"
			} else if time.Until(credential.ExpiresAt) < time.Hour {
				state = "expiring"
			}
		}
		_, _ = fmt.Fprintf(stdout, "%s\n", state)
		return nil
	}
	service = resolveService(service)
	manager, closeManager, err := newManager()
	if err != nil {
		return err
	}
	defer closeManager()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch command {
	case "refresh":
		refreshed, err := manager.Refresh(ctx, service, &credential, 0)
		if err != nil {
			return err
		}
		value, _ := json.MarshalIndent(refreshed, "", "  ")
		return store.SaveCredential(path, value)
	case "revoke":
		if err := manager.Revoke(ctx, service, &credential, nil); err != nil {
			return err
		}
		return store.DeleteCredential(path)
	case "logout":
		if err := manager.Revoke(ctx, service, &credential, nil); err != nil && !strings.Contains(err.Error(), "does not support") {
			return err
		}
		return store.DeleteCredential(path)
	default:
		return fmt.Errorf("unknown oauth command %q", command)
	}
}

func openBrowser(address string) error {
	if _, err := exec.LookPath("rundll32"); err == nil {
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", address).Start()
	}
	if _, err := exec.LookPath("open"); err == nil {
		return exec.Command("open", address).Start()
	}
	if _, err := exec.LookPath("xdg-open"); err == nil {
		return exec.Command("xdg-open", address).Start()
	}
	return errors.New("no supported browser launcher found")
}
