package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	mathrand "math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"ai-unisub/internal/database"
)

const defaultDatabaseURL = "sqlite://./data/ai-unisub.db"

var errUsage = errors.New("invalid command usage")

type usageError string

func (e usageError) Error() string        { return string(e) }
func (e usageError) Is(target error) bool { return target == errUsage }

func usageErrorf(format string, values ...any) error {
	return usageError(fmt.Sprintf(format, values...))
}

const usageText = `Usage:
  dummy <api-key> [--count <n>] [--database-url <url>]

Arguments:
  api-key       copied API key value (required)

Options:
  --count          number of call records to generate (default 10)
  --database-url   database URL (default: DATABASE_URL or ` + defaultDatabaseURL + `)
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stderr, usageText)
			return
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		if errors.Is(err, errUsage) {
			fmt.Fprint(os.Stderr, usageText)
		}
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		return usageErrorf("API key is required as the first argument")
	}
	apiKey := strings.TrimSpace(args[0])

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultDatabaseURL
	}

	flags := flag.NewFlagSet("dummy", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&databaseURL, "database-url", databaseURL, "database URL (default: DATABASE_URL or "+defaultDatabaseURL+")")
	count := 10
	flags.IntVar(&count, "count", count, "number of call records to generate")
	if args[0] == "-h" || args[0] == "--help" {
		return flags.Parse(args[:1])
	}
	if err := flags.Parse(args[1:]); err != nil {
		return usageErrorf("%v", err)
	}
	if count < 1 {
		return usageErrorf("--count must be greater than zero")
	}
	if flags.NArg() != 0 {
		return usageErrorf("unexpected argument %q", flags.Arg(0))
	}
	fmt.Fprintf(stdout, "parameters: api_key=%q database_url=%q count=%d\n", apiKey, databaseURL, count)

	db, err := database.NewDatabase(databaseURL)
	if err != nil {
		return fmt.Errorf("create database: %w", err)
	}
	if err := db.Open(); err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	key, account, err := findDummyAPIKey(db, apiKey)
	if err != nil {
		return err
	}
	rng := mathrand.New(mathrand.NewSource(time.Now().UnixNano()))
	for i := 0; i < count; i++ {
		if err := recordDummyCall(db, key, account, i, rng); err != nil {
			return fmt.Errorf("record %d: %w", i+1, err)
		}
	}
	fmt.Fprintf(stdout, "generated %d dummy call records for API key %q\n", count, key.Name)
	return nil
}

func findDummyAPIKey(db database.Database, value string) (*database.PersistedAPIKey, *database.PersistedAccount, error) {
	accounts, err := db.ListAccounts()
	if err != nil {
		return nil, nil, fmt.Errorf("list accounts: %w", err)
	}
	accountByID := make(map[string]*database.PersistedAccount, len(accounts))
	for i := range accounts {
		accountByID[accounts[i].ID] = &accounts[i]
	}

	var foundKey *database.PersistedAPIKey
	var foundAccount *database.PersistedAccount
	users, err := db.ListUsers()
	if err != nil {
		return nil, nil, fmt.Errorf("list users: %w", err)
	}
	for _, user := range users {
		keys, err := db.ListAPIKeys(user.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("list API keys: %w", err)
		}
		for i := range keys {
			if keys[i].Key != value {
				continue
			}
			account := accountByID[keys[i].AccountID]
			if account == nil {
				return nil, nil, fmt.Errorf("API key %q references missing account %q", value, keys[i].AccountID)
			}
			if account.Provider != "dummy" {
				return nil, nil, fmt.Errorf("API key %q is bound to provider %q, not dummy", value, account.Provider)
			}
			if foundKey != nil {
				return nil, nil, fmt.Errorf("API key %q is ambiguous", value)
			}
			foundKey = &keys[i]
			foundAccount = account
		}
	}
	if foundKey == nil {
		return nil, nil, fmt.Errorf("API key %q not found", value)
	}
	return foundKey, foundAccount, nil
}

func recordDummyCall(db database.Database, key *database.PersistedAPIKey, account *database.PersistedAccount, index int, rng *mathrand.Rand) error {
	id, err := randomID(16)
	if err != nil {
		return fmt.Errorf("generate record ID: %w", err)
	}
	requestID, err := randomID(8)
	if err != nil {
		return fmt.Errorf("generate request ID: %w", err)
	}
	models := []string{"dummy-model", "dummy-fast", "dummy-reasoning", "dummy-long-context"}
	paths := []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"}
	model := models[rng.Intn(len(models))]
	path := paths[rng.Intn(len(paths))]
	statusCodes := []int{http.StatusOK, http.StatusOK, http.StatusOK, http.StatusBadRequest, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusInternalServerError}
	status := statusCodes[rng.Intn(len(statusCodes))]
	started := time.Now().UTC().Add(-time.Duration(rng.Intn(7*24*60)) * time.Minute)
	finished := started.Add(time.Duration(50+rng.Intn(2950)) * time.Millisecond)
	inputTokens := 5 + rng.Intn(1800)
	outputTokens := 5 + rng.Intn(1200)
	prompts := []string{"帮我总结这段内容", "生成一个调试用的 JSON 示例", "解释这个请求的处理流程", "请给出三个可行方案", "写一段简短的测试说明"}
	body, err := json.Marshal(map[string]any{
		"model":  model,
		"prompt": fmt.Sprintf("%s（样本 %d）", prompts[rng.Intn(len(prompts))], index+1),
		"stream": rng.Intn(2) == 0,
	})
	if err != nil {
		return err
	}
	originalHeaders := http.Header{
		"Content-Type": []string{"application/json"},
		"User-Agent":   []string{"dummy-debug-client/1.0"},
		"X-Debug-Case": []string{fmt.Sprintf("case-%02d", 1+rng.Intn(99))},
		"X-Remove-Me":  []string{"removed-by-forwarder"},
	}
	outboundHeaders := originalHeaders.Clone()
	// Keep these three differences deterministic so every generated record
	// exercises the added, removed, and modified header display states.
	outboundHeaders.Set("User-Agent", "ai-unisub-forwarder/1.0")
	outboundHeaders.Del("X-Remove-Me")
	outboundHeaders.Set("X-Forwarded-By", "ai-unisub")
	responseBody := fmt.Sprintf(`{"id":"dummy-%s","model":"%s","choices":[{"message":{"content":"dummy response sample %d"}}]}`, requestID[:10], model, 1+rng.Intn(999))
	trace := &database.PersistedCallTrace{
		ID:                     id,
		APIKey:                 key.Key,
		ProviderType:           "dummy",
		AccountID:              account.ID,
		RequestID:              requestID,
		SourceIP:               "127.0.0.1",
		URL:                    path,
		HTTPErrorCode:          status,
		HTTPErrorInfo:          errorInfo(status),
		OriginalRequestHeaders: originalHeaders,
		OutboundRequestHeaders: outboundHeaders,
		RequestBody:            body,
		ResponseHeaders:        http.Header{"Content-Type": []string{"application/json"}},
		ResponseBody:           []byte(responseBody),
		Model:                  model,
		InputTokens:            inputTokens,
		OutputTokens:           outputTokens,
		StartedAt:              started,
		FinishedAt:             finished,
	}
	return db.RecordCallTrace(trace)
}

func errorInfo(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "dummy request validation failed"
	case http.StatusTooManyRequests:
		return "dummy rate limit reached"
	case http.StatusBadGateway:
		return "dummy upstream unavailable"
	case http.StatusInternalServerError:
		return "dummy internal error"
	default:
		return ""
	}
}

func randomID(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
