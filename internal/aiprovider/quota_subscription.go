package aiprovider

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"math"
	"strconv"
	"strings"
	"time"
)

// key identifies an upstream window across body and header observations.
// Presence flags prevent missing fields from being mistaken for real zero usage.
type subscriptionUpdate struct {
	key                string
	item               SubscriptionQuotaItem
	hasUsage, hasReset bool
}

func quotaNumberValue(raw jsontext.Value) (float64, bool) {
	var n float64
	err := json.Unmarshal(raw, &n)
	return n, err == nil && raw.Kind() == '0' && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0
}

func quotaResetString(raw jsontext.Value) time.Time {
	s, _ := quotaString(raw)
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t.UTC()
}

func quotaUnix(n int64) time.Time {
	if n < 0 || n >= 253402300800 { // Beyond RFC3339's four-digit year range.
		return time.Time{}
	}
	return time.Unix(n, 0).UTC()
}

func quotaWindowName(seconds int64, fallback string) string {
	switch {
	case seconds == 604800:
		return "weekly"
	case seconds > 0 && seconds%3600 == 0:
		return strconv.FormatInt(seconds/3600, 10) + "h"
	case seconds > 0 && seconds%60 == 0:
		return strconv.FormatInt(seconds/60, 10) + "m"
	case seconds > 0:
		return strconv.FormatInt(seconds, 10) + "s"
	default:
		return fallback
	}
}

func subscriptionBodyUpdates(supplier string, items []QuotaItem, observed time.Time) ([]subscriptionUpdate, error) {
	var updates []subscriptionUpdate
	for _, item := range items {
		value := jsontext.Value(item.Value)
		switch supplier {
		case "anthropic":
			if item.Name != "five_hour" && item.Name != "seven_day" && !strings.HasPrefix(item.Name, "seven_day_") {
				continue
			}
			if !presentQuotaValue(value) {
				continue
			}
			window := quotaObject(value)
			usage, ok := quotaNumberValue(window["utilization"])
			if !ok {
				return nil, ErrQuotaInvalidResponse
			}
			name := "weekly"
			if item.Name == "five_hour" {
				name = "5h"
			} else if suffix, ok := strings.CutPrefix(item.Name, "seven_day_"); ok {
				name += " (" + suffix + ")"
			}
			updates = append(updates, subscriptionUpdate{key: item.Name, item: SubscriptionQuotaItem{name, usage, quotaResetString(window["resets_at"])}, hasUsage: true, hasReset: true})
		case "openai":
			limits := []struct {
				key, label string
				value      jsontext.Value
			}{}
			if item.Name == "rate_limit" {
				limits = append(limits, struct {
					key, label string
					value      jsontext.Value
				}{value: value})
			} else if item.Name == "additional_rate_limits" {
				var extra []map[string]jsontext.Value
				if json.Unmarshal(value, &extra) != nil {
					return nil, ErrQuotaInvalidResponse
				}
				for i, limit := range extra {
					label, _ := quotaString(limit["limit_name"])
					if label == "" {
						label, _ = quotaString(limit["metered_feature"])
					}
					if label == "" {
						label = "additional-" + strconv.Itoa(i+1)
					}
					limits = append(limits, struct {
						key, label string
						value      jsontext.Value
					}{label + "/", label, limit["rate_limit"]})
				}
			}
			for _, limit := range limits {
				fields := quotaObject(limit.value)
				for _, slot := range []string{"primary", "secondary"} {
					window := quotaObject(fields[slot+"_window"])
					if window == nil {
						continue
					}
					usage, ok := quotaNumberValue(window["used_percent"])
					if !ok {
						return nil, ErrQuotaInvalidResponse
					}
					var seconds, reset int64
					_ = json.Unmarshal(window["limit_window_seconds"], &seconds)
					resetAt := time.Time{}
					if json.Unmarshal(window["reset_at"], &reset) == nil && window["reset_at"].Kind() == '0' {
						resetAt = quotaUnix(reset)
					} else if json.Unmarshal(window["reset_after_seconds"], &reset) == nil && window["reset_after_seconds"].Kind() == '0' && reset >= 0 && reset < 253402300800-observed.Unix() {
						resetAt = quotaUnix(observed.Unix() + reset)
					}
					name := quotaWindowName(seconds, slot)
					if limit.label != "" {
						name += " (" + limit.label + ")"
					}
					updates = append(updates, subscriptionUpdate{key: limit.key + slot, item: SubscriptionQuotaItem{name, usage, resetAt}, hasUsage: true, hasReset: true})
				}
			}
		case "grok":
			if item.Name != "config" {
				continue
			}
			config := quotaObject(value)
			if item.Source == "billing?format=credits" {
				usage, ok := quotaNumberValue(config["creditUsagePercent"])
				if !ok {
					return nil, ErrQuotaInvalidResponse
				}
				updates = append(updates, subscriptionUpdate{key: "weekly", item: SubscriptionQuotaItem{"weekly", usage, quotaResetString(quotaObject(config["currentPeriod"])["end"])}, hasUsage: true, hasReset: true})
			} else if item.Source == "billing" {
				limit, ok := grokAmountNumber(config["monthlyLimit"])
				used, usedOK := grokAmountNumber(config["used"])
				// A zero allowance has no meaningful percentage; it is not 0% used.
				if !ok || !usedOK {
					return nil, ErrQuotaInvalidResponse
				}
				if limit == 0 {
					continue
				}
				usage := used / limit * 100
				if math.IsInf(usage, 0) || math.IsNaN(usage) {
					return nil, ErrQuotaInvalidResponse
				}
				updates = append(updates, subscriptionUpdate{key: "monthly", item: SubscriptionQuotaItem{"monthly", usage, quotaResetString(config["billingPeriodEnd"])}, hasUsage: true, hasReset: true})
			}
		}
	}
	if len(updates) == 0 || len(updates) > 128 {
		return nil, ErrQuotaInvalidResponse
	}
	return updates, nil
}

func grokAmountNumber(raw jsontext.Value) (float64, bool) {
	if raw.Kind() == '{' {
		raw = quotaObject(raw)["val"]
	}
	if raw.Kind() == '"' {
		s, _ := quotaString(raw)
		n, err := strconv.ParseFloat(s, 64)
		return n, err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) && n >= 0
	}
	return quotaNumberValue(raw)
}

// Header values are validated first, then converted into partial window updates.
func subscriptionHeaderUpdates(c AIProviderConfig, service string, headers map[string][]string, observed time.Time) []subscriptionUpdate {
	values := make(map[string]string)
	for _, item := range subscriptionHeaderItems(c, service, headers) {
		values[strings.ToLower(item.Name)] = item.Value
	}
	var updates []subscriptionUpdate
	add := func(key, name, usageText, resetText string, scale float64, relative bool) {
		u := subscriptionUpdate{key: key, item: SubscriptionQuotaItem{TimeDimension: name}}
		if usage, err := strconv.ParseFloat(usageText, 64); err == nil && !math.IsInf(usage*scale, 0) {
			u.item.Usage, u.hasUsage = usage*scale, true
		}
		if resetText != "" {
			u.item.ResetAt = subscriptionReset(resetText, observed, relative, service == "grok")
			u.hasReset = !u.item.ResetAt.IsZero()
		}
		if u.hasUsage || u.hasReset {
			updates = append(updates, u)
		}
	}
	switch service {
	case "claude":
		for _, w := range []struct{ header, key, label string }{{"5h", "five_hour", "5h"}, {"7d", "seven_day", "weekly"}, {"7d_oi", "seven_day_overage_included", "weekly (overage_included)"}} {
			prefix := "anthropic-ratelimit-unified-" + w.header + "-"
			add(w.key, w.label, values[prefix+"utilization"], values[prefix+"reset"], 100, false)
		}
	case "codex":
		for _, slot := range []string{"primary", "secondary"} {
			prefix := "x-codex-" + slot + "-"
			minutes, _ := strconv.ParseInt(values[prefix+"window-minutes"], 10, 64)
			name := ""
			if minutes > 0 && minutes <= math.MaxInt64/60 {
				name = quotaWindowName(minutes*60, slot)
			}
			reset, relative := values[prefix+"reset-at"], false
			if reset == "" {
				reset, relative = values[prefix+"reset-after-seconds"], true
			}
			add(slot, name, values[prefix+"used-percent"], reset, 1, relative)
		}
	case "grok":
		for _, dimension := range []string{"requests", "tokens"} {
			get := func(field string) string {
				if v := values["x-ratelimit-"+field+"-"+dimension]; v != "" {
					return v
				}
				return values["x-rate-limit-"+field+"-"+dimension]
			}
			limit, e1 := strconv.ParseFloat(get("limit"), 64)
			remaining, e2 := strconv.ParseFloat(get("remaining"), 64)
			usage := ""
			if e1 == nil && e2 == nil && limit > 0 && remaining <= limit {
				usage = strconv.FormatFloat((limit-remaining)/limit*100, 'g', -1, 64)
			}
			// These are rate-limit windows, not weekly/monthly billing quotas.
			add(dimension, dimension, usage, get("reset"), 1, false)
		}
	}
	return updates
}

func subscriptionReset(value string, observed time.Time, relative, grok bool) time.Time {
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		if n < 0 {
			return time.Time{}
		}
		if !relative && n >= 1_000_000_000_000 {
			n /= 1000
		}
		if relative || grok && n < 1_000_000_000 {
			if n >= 253402300800-observed.Unix() {
				return time.Time{}
			}
			n += observed.Unix()
		}
		return quotaUnix(n)
	}
	if grok {
		if d, err := time.ParseDuration(value); err == nil && d > 0 {
			return observed.Add(d).UTC()
		}
		t, _ := time.Parse(time.RFC3339Nano, value)
		return t.UTC()
	}
	return time.Time{}
}
