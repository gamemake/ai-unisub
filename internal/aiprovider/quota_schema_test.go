package aiprovider

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// Synthetic fixtures follow the documented wire types. They are deliberately
// not presented as live account captures or a complete private API schema.
func TestQuotaWireSchemasAndRawPreservation(t *testing.T) {
	for _, tc := range []struct{ name, supplier, body string }{
		{"moonshot-precision", "kimi", `{"code":0,"status":true,"scode":"0x0","data":{"available_balance":12345678901234567890.001,"cash_balance":-0.25,"voucher_balance":1.2300e-6}}`},
		{"claude-percent", "anthropic", `{"five_hour":{"utilization":25.000,"resets_at":"2030-01-01T08:00:00.123456+08:00"},"seven_day":null,"extra_usage":{"used_credits":123,"unverified_unit":"raw"}}`},
		{"claude-optional-reset", "anthropic", `{"five_hour":{"utilization":0,"resets_at":null},"seven_day_sonnet":{"utilization":125}}`},
		{"codex-weekly-primary", "openai", `{"rate_limit":{"primary_window":{"used_percent":25,"limit_window_seconds":604800,"reset_at":1893456000},"secondary_window":null},"credits":{"balance":"12.3400"}}`},
		{"codex-additional", "openai", `{"rate_limit":null,"additional_rate_limits":[{"limit_name":"other","rate_limit":{"secondary_window":{"used_percent":0,"reset_after_seconds":0}}}]}`},
		{"codex-null-additional", "openai", `{"rate_limit":{"primary_window":{"used_percent":0}},"additional_rate_limits":[{"limit_name":"optional","rate_limit":null}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items, err := parseSupplierQuota(tc.supplier, []byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			var raw map[string]jsontext.Value
			if err := json.Unmarshal([]byte(tc.body), &raw); err != nil {
				t.Fatal(err)
			}
			if len(items) != len(raw) {
				t.Fatal("dropped fields", items)
			}
			for _, item := range items {
				if item.Value != string(raw[item.Name]) {
					t.Fatalf("changed raw field %s: %s", item.Name, item.Value)
				}
			}
		})
	}
}

func TestDeepSeekBalanceInfosAreFlattened(t *testing.T) {
	body := `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"288.15","granted_balance":"0.00","topped_up_balance":"288.15"}]}`
	items, err := parseSupplierQuota("deepseek", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	want := []QuotaItem{
		{Name: "balance", Value: "288.15 CNY"},
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("got %+v; want %+v", items, want)
	}
}

func TestDeepSeekUnavailableBalance(t *testing.T) {
	body := `{"is_available":false,"balance_infos":[{"currency":"CNY","total_balance":"288.15","granted_balance":"0.00","topped_up_balance":"288.15"}]}`
	items, err := parseSupplierQuota("deepseek", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	want := []QuotaItem{{Name: "balance", Value: "not available"}}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("got %+v; want %+v", items, want)
	}
}

func TestQuotaRejectsInvalidWireData(t *testing.T) {
	for _, tc := range []struct{ supplier, body string }{
		{"deepseek", `{"is_available":true,"balance_infos":null}`},
		{"deepseek", `{"is_available":"false","balance_infos":[{"currency":"CNY","total_balance":"0","granted_balance":"0","topped_up_balance":"0"}]}`},
		{"deepseek", `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":1,"granted_balance":"0","topped_up_balance":"1"}]}`},
		{"deepseek", `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"NaN","granted_balance":"0","topped_up_balance":"1"}]}`},
		{"deepseek", `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"1"}]}`},
		{"kimi", `{"code":123,"status":true,"scode":"0x0","data":{"available_balance":1,"cash_balance":1,"voucher_balance":0}}`},
		{"kimi", `{"code":0,"status":false,"scode":"0x0","data":{"available_balance":1,"cash_balance":1,"voucher_balance":0}}`},
		{"kimi", `{"code":0,"status":true,"scode":"0x0","data":{"available_balance":"1","cash_balance":1,"voucher_balance":0}}`},
		{"kimi", `{"code":0,"status":true,"scode":"0x0","data":null}`},
		{"kimi", `{"status":true,"data":{"available_balance":1,"cash_balance":1,"voucher_balance":0}}`},
		{"anthropic", `{"five_hour":null,"seven_day":null}`},
		{"anthropic", `{"five_hour":{"utilization":"25"}}`},
		{"anthropic", `{"five_hour":{"utilization":-1}}`},
		{"anthropic", `{"five_hour":{"utilization":25,"resets_at":1893456000}}`},
		{"anthropic", `{"five_hour":{"utilization":25,"resets_at":"tomorrow"}}`},
		{"anthropic", `{"five_hour":{"utilization":25},"extra_usage":[]}`},
		{"anthropic", `{"five_hour":{"utilization":25},"error":{"message":"failed"}}`},
		{"openai", `{"rate_limit":null,"additional_rate_limits":[]}`},
		{"openai", `{"rate_limit":{"primary_window":{},"secondary_window":null}}`},
		{"openai", `{"rate_limit":{"primary_window":{"used_percent":-1}}}`},
		{"openai", `{"rate_limit":{"primary_window":{"used_percent":25,"limit_window_seconds":"300"}}}`},
		{"openai", `{"rate_limit":{"primary_window":{"used_percent":25,"reset_at":1.5}}}`},
		{"openai", `{"rate_limit":{"primary_window":{"used_percent":25}},"additional_rate_limits":[null]}`},
		{"openai", `{"result":{"rateLimits":{"primary":{"usedPercent":25}}}}`},
		{"anthropic", `{"five_hour":{"utilization":25,"utilization":50}}`},
		{"anthropic", `{"five_hour":{"utilization":25}} {}`},
	} {
		t.Run(tc.supplier+tc.body, func(t *testing.T) {
			items, err := parseSupplierQuota(tc.supplier, []byte(tc.body))
			if items != nil || !errors.Is(err, ErrQuotaInvalidResponse) {
				t.Fatalf("accepted invalid payload: %v, %v", items, err)
			}
		})
	}
}

func TestInvalidQuotaRefreshPreservesCache(t *testing.T) {
	p := &oauthAIProvider{config: AIProviderConfig{Kind: "api", Supplier: "kimi", APIKey: "inference"}}
	body := `{"code":0,"status":true,"scode":"0x0","data":{"available_balance":0,"cash_balance":0,"voucher_balance":0}}`
	p.client = &http.Client{Transport: quotaTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	before, err := p.quota(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		`{"code":123,"status":true,"scode":"0x0","data":{"available_balance":99,"cash_balance":99,"voucher_balance":0}}`,
		`{"code":0,"status":true,"scode":"0x0","data":null}`,
	} {
		body = invalid
		if _, err := p.quota(t.Context(), ""); !errors.Is(err, ErrQuotaInvalidResponse) {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, p.GetCachedQuota()) {
			t.Fatal("invalid refresh changed cached values or freshness")
		}
	}
}

func TestPassiveQuotaUnitsAndInvalidValues(t *testing.T) {
	for _, tc := range []struct{ service, name, valid string }{
		{"claude", "anthropic-ratelimit-unified-5h-utilization", "0.2500"},
		{"codex", "x-codex-primary-used-percent", "25.00"},
		{"codex", "x-codex-primary-window-minutes", "10080"},
		{"claude", "anthropic-ratelimit-unified-7d-reset", "1893456000"},
		{"claude", "anthropic-ratelimit-unified-7d_oi-utilization", "0.12300"},
		{"claude", "anthropic-ratelimit-unified-7d_oi-reset", "1893456000000"},
		{"codex", "x-codex-primary-reset-after-seconds", "0"},
		{"codex", "x-codex-secondary-reset-after-seconds", "3600"},
		{"codex", "x-codex-primary-over-secondary-limit-percent", "12.500"},
		{"grok", "x-ratelimit-limit-requests", "100"},
		{"grok", "x-ratelimit-remaining-requests", "0"},
		{"grok", "x-ratelimit-limit-tokens", "10000"},
		{"grok", "x-ratelimit-remaining-tokens", "12"},
		{"grok", "x-ratelimit-reset-requests", "1893456000"},
		{"grok", "x-ratelimit-reset-tokens", "1m30s"},
		{"grok", "x-rate-limit-reset-tokens", "2030-01-01T00:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := AIProviderConfig{Kind: "subscription", AuthType: AuthTypeAPIKey}
			headers := http.Header{}
			headers.Set(tc.name, tc.valid)
			items := subscriptionHeaderItems(config, tc.service, headers)
			if len(items) != 1 || items[0].Value != tc.valid {
				t.Fatal("converted raw units or dispatched by auth instead of kind", items)
			}
			for _, bad := range []string{"NaN", "Inf", "-1", "", "null", "25%", "1e999"} {
				headers.Set(tc.name, bad)
				if got := subscriptionHeaderItems(config, tc.service, headers); len(got) != 0 {
					t.Fatal("accepted invalid header", got)
				}
			}
			headers[http.CanonicalHeaderKey(tc.name)] = []string{tc.valid, tc.valid}
			if got := subscriptionHeaderItems(config, tc.service, headers); len(got) != 0 {
				t.Fatal("accepted ambiguous header", got)
			}
		})
	}
}

func TestUnsupportedQuotaQueriesNeverSendCredentials(t *testing.T) {
	for _, tc := range []struct{ supplier, kind string }{
		{"grok", "api"}, {"zhipu", "subscription"}, {"zhipu", "api"},
		{"openai", "api"}, {"anthropic", "api"}, {"kimi", "subscription"}, {"deepseek", "subscription"},
	} {
		t.Run(tc.supplier+"/"+tc.kind, func(t *testing.T) {
			p := &oauthAIProvider{config: AIProviderConfig{Supplier: tc.supplier, Kind: tc.kind, APIKey: "must-not-be-sent"}}
			p.client = &http.Client{Transport: quotaTransport(func(*http.Request) (*http.Response, error) {
				t.Fatal("unsupported query made a network request")
				return nil, ErrQuotaUnsupported
			})}
			if result, err := p.quota(t.Context(), ""); result != nil || !errors.Is(err, ErrQuotaUnsupported) {
				t.Fatal(result, err)
			}
			if p.GetCachedQuota().CacheStatus != QuotaCacheMissing {
				t.Fatal("unsupported query fabricated a cache")
			}
		})
	}
	for _, supplier := range []string{"zhipu"} {
		if _, err := parseSupplierQuota(supplier, []byte(`{"data":{"limits":[]},"total":{"val":"-1000"}}`)); !errors.Is(err, ErrQuotaUnsupported) {
			t.Fatal("unsupported supplier accepted a guessed schema", supplier, err)
		}
	}
}
