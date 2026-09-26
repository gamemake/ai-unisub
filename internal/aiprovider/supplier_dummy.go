package aiprovider

import (
	"bytes"
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"time"
)

const dummyResponseBody = `{"model":"dummy-model","choices":[{"message":{"role":"assistant","content":"UniSub dummy response"}}]}`

// SupplierDummy is a local supplier for demos and development. It never
// contacts an upstream service: requests get a fixed response, quota is random
// demo data, and plans use the same IDs as Anthropic. Like Anthropic it only
// serves the Claude protocol.
type SupplierDummy struct{ SupplierData }

func newSupplierDummy(manager *providerManager) Supplier {
	return &SupplierDummy{SupplierData{
		manager: manager, id: "dummy", name: "Dummy",
		builtin: SupplierBuiltinConfig{
			ClaudeURL: "http://dummy.local/v1",
			Models:    []string{"dummy-fast", "dummy-long-context", "dummy-model", "dummy-reasoning"},
			Weights: []SubscriptionPlanWeight{
				{Name: "claude_pro", Weight: 1},
				{Name: "claude_max_5x", Weight: 5},
				{Name: "claude_max_20x", Weight: 20},
			},
		},
	}}
}

// GetAccess never refreshes or requires a credential: the stored access token
// is used when present, otherwise a fixed placeholder.
func (s *SupplierDummy) GetAccess(ctx context.Context, account *Account, req *http.Request) (string, string, error) {
	if ctx == nil || account == nil || req == nil {
		return "", "", errContextAccountAndRequestRequired
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	account.mu.RLock()
	token := account.Config.Credential.AccessToken
	account.mu.RUnlock()
	if token == "" {
		token = "dummy-access-token"
	}
	return token, s.GetConfig().ClaudeURL, nil
}

// DoRequest answers locally instead of sending the request upstream.
func (s *SupplierDummy) DoRequest(ctx context.Context, account *Account, req *http.Request) (*http.Response, error) {
	if ctx == nil || account == nil || req == nil {
		return nil, errContextAccountAndRequestRequired
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": {"application/json"}},
		Body:          io.NopCloser(bytes.NewReader([]byte(dummyResponseBody))),
		ContentLength: int64(len(dummyResponseBody)),
		Request:       req,
	}, nil
}

func (s *SupplierDummy) RefreshModel(ctx context.Context, account *Account) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if account == nil {
		return nil, errAccountRequired
	}
	return s.GetModels(), nil
}

// FetchQuota returns random usage in the same 5h/weekly windows as Claude.
func (s *SupplierDummy) FetchQuota(ctx context.Context, account *Account) (AccountQuota, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return AccountQuota{}, err
	}
	if account == nil {
		return AccountQuota{}, errAccountRequired
	}
	now := time.Now().UTC()
	return AccountQuota{
		Subscription: []SubscriptionQuotaItem{
			{TimeDimension: "5h", Usage: float64(rand.IntN(101)), ResetAt: now.Add(time.Duration(1+rand.IntN(300)) * time.Minute)},
			{TimeDimension: "weekly", Usage: float64(rand.IntN(101)), ResetAt: now.Add(time.Duration(1+rand.IntN(10080)) * time.Minute)},
		},
		CacheStatus: QuotaCacheFresh,
		UpdatedAt:   now,
	}, nil
}

func (s *SupplierDummy) ResetQuota(ctx context.Context, account *Account, _ string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if account == nil {
		return errAccountRequired
	}
	return nil
}
