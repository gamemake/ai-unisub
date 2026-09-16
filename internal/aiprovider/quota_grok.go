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
	if monthly {
		if !validGrokAmount(config["monthlyLimit"]) || !validGrokAmount(config["used"]) {
			return false
		}
	} else if !quotaPercent(config["creditUsagePercent"]) {
		return false
	}
	for _, key := range []string{"monthlyLimit", "used", "prepaidBalance", "onDemandCap", "onDemandUsed"} {
		if value := config[key]; presentQuotaValue(value) && !validGrokAmount(value) {
			return false
		}
	}
	if value := config["productUsage"]; presentQuotaValue(value) {
		var products []map[string]jsontext.Value
		if value.Kind() != '[' || json.Unmarshal(value, &products) != nil {
			return false
		}
		for _, product := range products {
			if _, ok := quotaString(product["product"]); !ok || !quotaPercent(product["quotaPercent"]) {
				return false
			}
		}
	}
	return true
}

func validGrokAmount(value jsontext.Value) bool {
	if value.Kind() == '{' {
		value = quotaObject(value)["val"]
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
