package aiprovider

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
)

// Each billing response has its own config. Validate useful quota signals but
// preserve the entire raw response, including unknown fields and numeric text.
func validGrokBilling(fields map[string]jsontext.Value, monthly bool) bool {
	config := quotaObject(fields["config"])
	if config == nil || presentQuotaValue(fields["error"]) || presentQuotaValue(config["error"]) {
		return false
	}
	if presentQuotaValue(config["currentPeriod"]) && quotaObject(config["currentPeriod"]) == nil {
		return false
	}
	if monthly {
		hasLimit := presentQuotaValue(config["monthlyLimit"])
		hasUsed := presentQuotaValue(config["used"])
		if hasLimit || hasUsed {
			if !validGrokAmount(config["monthlyLimit"]) || !validGrokAmount(config["used"]) {
				return false
			}
		} else if !validGrokCreditsSignal(config) {
			return false
		}
	} else if !validGrokCreditsSignal(config) {
		return false
	}
	for _, key := range []string{"monthlyLimit", "used", "prepaidBalance", "onDemandCap", "onDemandUsed"} {
		if value := config[key]; presentQuotaValue(value) && !validGrokAmount(value) {
			return false
		}
	}
	if value := config["productUsage"]; presentQuotaValue(value) && !validGrokProductUsage(value) {
		return false
	}
	return true
}

// proto3 JSON omits default zeros, so a weekly reset arrives without
// creditUsagePercent. A typed currentPeriod is then the 0% signal.
func validGrokCreditsSignal(config map[string]jsontext.Value) bool {
	if presentQuotaValue(config["creditUsagePercent"]) {
		return quotaPercent(config["creditUsagePercent"])
	}
	return quotaObject(config["currentPeriod"]) != nil
}

func validGrokProductUsage(value jsontext.Value) bool {
	var products []map[string]jsontext.Value
	if value.Kind() != '[' || json.Unmarshal(value, &products) != nil {
		return false
	}
	for _, product := range products {
		if product == nil {
			return false
		}
		if raw := product["product"]; presentQuotaValue(raw) {
			if _, ok := quotaString(raw); !ok && !quotaNumber(raw) {
				return false
			}
		}
		percent := product["quotaPercent"]
		if !presentQuotaValue(percent) {
			percent = product["usagePercent"]
		}
		if presentQuotaValue(percent) && !quotaPercent(percent) {
			return false
		}
	}
	return true
}

func validGrokAmount(value jsontext.Value) bool {
	if value.Kind() == '{' {
		obj := quotaObject(value)
		if obj == nil {
			return false
		}
		value = obj["val"]
		if len(value) == 0 {
			// proto3 omits zero scalars; {} is a $0 Cent.
			return true
		}
	}
	return quotaNumber(value) || quotaDecimalString(value)
}

func validateGrokBillingWindow(body []byte, monthly bool) error {
	var fields map[string]jsontext.Value
	if json.Unmarshal(body, &fields) != nil || !validGrokBilling(fields, monthly) {
		return ErrQuotaInvalidResponse
	}
	return nil
}
