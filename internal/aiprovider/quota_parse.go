package aiprovider

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Keep each field and its raw JSON value, including numeric precision, nulls
// and localized text. DeepSeek balances are normalized for display.
func parseSupplierQuota(supplier string, body []byte) ([]QuotaItem, error) {
	var fields map[string]jsontext.Value
	if json.Unmarshal(body, &fields) != nil || len(fields) == 0 {
		return nil, ErrQuotaInvalidResponse
	}
	if len(fields) > 128 {
		return nil, ErrQuotaInvalidResponse
	}
	if err := validateSupplierQuota(supplier, fields); err != nil {
		return nil, err
	}
	var deepSeekAvailable bool
	if supplier == "deepseek" {
		_ = json.Unmarshal(fields["is_available"], &deepSeekAvailable)
	}
	// Decode again to preserve the original field order and raw value bytes.
	decoder := jsontext.NewDecoder(bytes.NewReader(body))
	if _, err := decoder.ReadToken(); err != nil {
		return nil, ErrQuotaInvalidResponse
	}
	var items []QuotaItem
	for decoder.PeekKind() != '}' {
		name, err := decoder.ReadToken()
		if err != nil {
			return nil, ErrQuotaInvalidResponse
		}
		fieldName := name.String()
		value, err := decoder.ReadValue()
		if err != nil {
			return nil, ErrQuotaInvalidResponse
		}
		if supplier == "deepseek" && fieldName == "is_available" {
			continue
		}
		if supplier == "deepseek" && fieldName == "balance_infos" {
			balances, err := normalizeDeepSeekBalances(value, deepSeekAvailable)
			if err != nil {
				return nil, ErrQuotaInvalidResponse
			}
			items = append(items, balances...)
			continue
		}
		items = append(items, QuotaItem{Name: fieldName, Value: string(value)})
	}
	return items, nil
}

func normalizeDeepSeekBalances(raw jsontext.Value, available bool) ([]QuotaItem, error) {
	if !available {
		return []QuotaItem{{Name: "balance", Value: "not available"}}, nil
	}
	var balances []map[string]jsontext.Value
	if json.Unmarshal(raw, &balances) != nil {
		return nil, ErrQuotaInvalidResponse
	}
	items := make([]QuotaItem, 0, len(balances))
	for _, balance := range balances {
		currency, _ := quotaString(balance["currency"])
		total, _ := quotaString(balance["total_balance"])
		items = append(items, QuotaItem{Name: "balance", Value: total + " " + currency})
	}
	return items, nil
}

// Collect only known subscription quota headers, without converting their values.
// Header names retain the spelling supplied by net/http.
func subscriptionHeaderItems(c AIProviderConfig, service string, headers http.Header) []QuotaItem {
	if c.Kind == "api" || c.Kind == "" && c.AuthType == AuthTypeAPIKey {
		return nil
	}
	var names []string
	for name := range headers {
		key := strings.ToLower(name)
		known := false
		switch service {
		case "codex":
			for _, window := range []string{"primary", "secondary"} {
				for _, field := range []string{"used-percent", "window-minutes", "reset-at", "reset-after-seconds"} {
					known = known || key == "x-codex-"+window+"-"+field
				}
			}
			known = known || key == "x-codex-primary-over-secondary-limit-percent"
		case "claude":
			for _, window := range []string{"5h", "7d", "7d_oi"} {
				for _, field := range []string{"utilization", "reset"} {
					known = known || key == "anthropic-ratelimit-unified-"+window+"-"+field
				}
			}
		case "grok":
			for _, prefix := range []string{"x-ratelimit-", "x-rate-limit-"} {
				for _, dimension := range []string{"requests", "tokens"} {
					for _, field := range []string{"limit", "remaining", "reset"} {
						known = known || key == prefix+field+"-"+dimension
					}
				}
			}
		}
		if known {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	var items []QuotaItem
	for _, name := range names {
		values := headers[name]
		// Ambiguous or malformed quota values must not poison a valid cache.
		if len(values) == 1 && validSubscriptionHeader(name, values[0]) {
			items = append(items, QuotaItem{Name: name, Value: values[0]})
		}
	}
	return items
}

func validSubscriptionHeader(name, value string) bool {
	name = strings.ToLower(name)
	if strings.HasSuffix(name, "-utilization") || strings.HasSuffix(name, "-percent") {
		n, err := strconv.ParseFloat(value, 64)
		return err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0
	}
	if strings.HasPrefix(name, "x-ratelimit-reset-") || strings.HasPrefix(name, "x-rate-limit-reset-") {
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			return n >= 0
		}
		if d, err := time.ParseDuration(value); err == nil {
			return d > 0
		}
		_, err := time.Parse(time.RFC3339Nano, value)
		return err == nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n >= 0 && (!strings.HasSuffix(name, "-window-minutes") || n > 0)
}
