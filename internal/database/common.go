package database

import (
	"encoding/json"
	"errors"
	"strings"
)

func validateAccount(value *PersistedAccount) error {
	if value == nil || value.ID < 0 {
		return errors.New("account and account ID are required")
	}
	return nil
}

func validateUser(value *PersistedUser) error {
	if value == nil || value.ID < 0 {
		return errors.New("user and user ID are required")
	}
	return nil
}

func validateAPIKey(value *PersistedAPIKey) error {
	if value == nil || value.ID < 0 {
		return errors.New("API key and key ID are required")
	}
	return nil
}

func validateModuleName(module string) error {
	if module == "" || strings.TrimSpace(module) != module {
		return errors.New("module name is required and must not have surrounding whitespace")
	}
	return nil
}

func cloneAccount(value PersistedAccount) PersistedAccount {
	value.Config = append([]byte(nil), value.Config...)
	value.State = append([]byte(nil), value.State...)
	value.Quota = append([]byte(nil), value.Quota...)
	return value
}

func cloneUser(value PersistedUser) PersistedUser {
	value.Labels = append([]string(nil), value.Labels...)
	return value
}

func cloneProxyGroup(value PersistedProxyGroup) PersistedProxyGroup {
	value.Config = append([]byte(nil), value.Config...)
	value.State = append([]byte(nil), value.State...)
	return value
}

func cloneJSON[T any](value T) T {
	raw, _ := json.Marshal(value)
	var clone T
	_ = json.Unmarshal(raw, &clone)
	return clone
}

func databaseID(id int) any {
	if id == 0 {
		return nil
	}
	return id
}
