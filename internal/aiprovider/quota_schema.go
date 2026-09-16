package aiprovider

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"regexp"
	"strings"
	"time"
)

// Validation never re-encodes the payload: display values still come from the
// original JSON bytes. Unknown fields are retained for forward compatibility.
func validateSupplierQuota(supplier string, fields map[string]jsontext.Value) error {
	valid := false
	switch supplier {
	case "deepseek":
		valid = validDeepSeekBalance(fields)
	case "kimi":
		valid = validMoonshotBalance(fields)
	case "anthropic":
		valid = validClaudeQuota(fields)
	case "openai":
		valid = validCodexQuota(fields)
	case "grok":
		valid = validGrokBilling(fields, false) || validGrokBilling(fields, true)
	default:
		// No guessed schemas for unverified supplier queries.
		return ErrQuotaUnsupported
	}
	if !valid || presentQuotaValue(fields["error"]) {
		return ErrQuotaInvalidResponse
	}
	return nil
}

func quotaObject(value jsontext.Value) map[string]jsontext.Value {
	var fields map[string]jsontext.Value
	if value.Kind() != '{' || json.Unmarshal(value, &fields) != nil {
		return nil
	}
	return fields
}

func presentQuotaValue(value jsontext.Value) bool { return len(value) != 0 && value.Kind() != 'n' }
func quotaBool(value jsontext.Value) bool         { return value.Kind() == 't' || value.Kind() == 'f' }
func quotaNumber(value jsontext.Value) bool       { return value.Kind() == '0' }

func quotaPercent(value jsontext.Value) bool {
	// Do not clamp at 100: overage can exceed the included quota. Validation
	// does not convert percent units or round the raw number through float64.
	return quotaNumber(value) && !strings.HasPrefix(strings.TrimSpace(string(value)), "-")
}

func quotaString(value jsontext.Value) (string, bool) {
	var s string
	err := json.Unmarshal(value, &s)
	return s, value.Kind() == '"' && err == nil
}

var quotaDecimal = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)

func quotaDecimalString(value jsontext.Value) bool {
	s, ok := quotaString(value)
	return ok && quotaDecimal.MatchString(s)
}

func validDeepSeekBalance(fields map[string]jsontext.Value) bool {
	var available bool
	if !quotaBool(fields["is_available"]) || json.Unmarshal(fields["is_available"], &available) != nil {
		return false
	}
	if !available {
		return true
	}
	var balances []map[string]jsontext.Value
	if fields["balance_infos"].Kind() != '[' ||
		json.Unmarshal(fields["balance_infos"], &balances) != nil || len(balances) == 0 {
		return false
	}
	for _, balance := range balances {
		currency, ok := quotaString(balance["currency"])
		if !ok || currency != "CNY" && currency != "USD" {
			return false
		}
		for _, key := range []string{"total_balance", "granted_balance", "topped_up_balance"} {
			if !quotaDecimalString(balance[key]) {
				return false
			}
		}
	}
	return true
}

func validMoonshotBalance(fields map[string]jsontext.Value) bool {
	var code int64
	scode, ok := quotaString(fields["scode"])
	if fields["status"].Kind() != 't' || !quotaNumber(fields["code"]) ||
		json.Unmarshal(fields["code"], &code) != nil || code != 0 || !ok || scode == "" {
		return false
	}
	data := quotaObject(fields["data"])
	for _, key := range []string{"available_balance", "voucher_balance", "cash_balance"} {
		if !quotaNumber(data[key]) {
			return false
		}
	}
	return true
}

func validClaudeQuota(fields map[string]jsontext.Value) bool {
	found := false
	for key, value := range fields {
		if key != "five_hour" && key != "seven_day" && !strings.HasPrefix(key, "seven_day_") {
			continue
		}
		if !presentQuotaValue(value) {
			continue
		}
		window := quotaObject(value)
		// OAuth body utilization is already percent, unlike response headers.
		if !quotaPercent(window["utilization"]) {
			return false
		}
		if reset := window["resets_at"]; presentQuotaValue(reset) {
			s, ok := quotaString(reset)
			if !ok {
				return false
			}
			if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
				return false
			}
		}
		found = true
	}
	// Extra spending is not an API cash balance. Its unverified units and
	// optional fields remain untouched; only the container shape is checked.
	if extra := fields["extra_usage"]; presentQuotaValue(extra) && quotaObject(extra) == nil {
		return false
	}
	return found
}

func validCodexQuota(fields map[string]jsontext.Value) bool {
	found := false
	if limit := fields["rate_limit"]; presentQuotaValue(limit) {
		var valid bool
		found, valid = validCodexLimit(limit)
		if !valid {
			return false
		}
	}
	if extra := fields["additional_rate_limits"]; presentQuotaValue(extra) {
		var limits []map[string]jsontext.Value
		if extra.Kind() != '[' || json.Unmarshal(extra, &limits) != nil {
			return false
		}
		for _, limit := range limits {
			if limit == nil {
				return false
			}
			hasWindow, valid := validCodexLimit(limit["rate_limit"])
			if !valid {
				return false
			}
			found = found || hasWindow
		}
	}
	return found
}

func validCodexLimit(value jsontext.Value) (found, valid bool) {
	if !presentQuotaValue(value) {
		return false, true
	}
	limit := quotaObject(value)
	if limit == nil {
		return false, false
	}
	for _, key := range []string{"primary_window", "secondary_window"} {
		if !presentQuotaValue(limit[key]) {
			continue
		}
		window := quotaObject(limit[key])
		if !quotaPercent(window["used_percent"]) {
			return false, false
		}
		// Missing optional timestamps are not zero; seconds are not minutes.
		for _, field := range []string{"limit_window_seconds", "reset_at", "reset_after_seconds"} {
			if raw := window[field]; presentQuotaValue(raw) {
				var n int64
				if !quotaNumber(raw) || json.Unmarshal(raw, &n) != nil || n < 0 || field == "limit_window_seconds" && n == 0 {
					return false, false
				}
			}
		}
		found = true
	}
	return found, true
}
