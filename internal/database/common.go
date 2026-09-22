package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

// rowSource is the read side of a SQL store; *sql.DB and *sql.Tx satisfy it.
type rowSource interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// The query* helpers read the small records that MemoryDatabase caches. They
// take no filter arguments, so the same statements run on every SQL store.

func queryAccounts(source rowSource) ([]PersistedAccount, error) {
	rows, err := source.Query(`SELECT id, provider, name, config, state, quota, created_at, updated_at FROM accounts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]PersistedAccount, 0)
	for rows.Next() {
		var value PersistedAccount
		var config, state, quota []byte
		if err := rows.Scan(&value.ID, &value.AIProvider, &value.Name, &config, &state, &quota, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, err
		}
		value.Config, value.State, value.Quota = slices.Clone(config), slices.Clone(state), slices.Clone(quota)
		result = append(result, value)
	}
	return result, rows.Err()
}

func queryUsers(source rowSource) ([]PersistedUser, error) {
	rows, err := source.Query(`SELECT id, name, labels, role, enabled, password_hash, created_at, updated_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]PersistedUser, 0)
	for rows.Next() {
		var value PersistedUser
		var labels []byte
		if err := rows.Scan(&value.ID, &value.Name, &labels, &value.Role, &value.Enabled, &value.PasswordHash, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(labels, &value.Labels); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func queryAPIKeys(source rowSource) ([]PersistedAPIKey, error) {
	rows, err := source.Query(`SELECT id, user_id, account_id, name, key_value, config, valid_seconds, created_at, updated_at FROM api_keys ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]PersistedAPIKey, 0)
	for rows.Next() {
		var value PersistedAPIKey
		var config []byte
		if err := rows.Scan(&value.ID, &value.UserID, &value.AccountID, &value.Name, &value.Key, &config, &value.ValidSeconds, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, err
		}
		value.Config = slices.Clone(config)
		result = append(result, value)
	}
	return result, rows.Err()
}

func queryProxyGroups(source rowSource) ([]PersistedProxyGroup, error) {
	rows, err := source.Query(`SELECT id, config, state, created_at, updated_at FROM proxy_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]PersistedProxyGroup, 0)
	for rows.Next() {
		var value PersistedProxyGroup
		var config, state []byte
		if err := rows.Scan(&value.ID, &config, &state, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, err
		}
		value.Config, value.State = slices.Clone(config), slices.Clone(state)
		result = append(result, value)
	}
	return result, rows.Err()
}

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

func validateConfig(value *PersistedConfig) error {
	if value == nil || value.ID < 0 {
		return errors.New("config and config ID are required")
	}
	if err := validateConfigType(value.Type); err != nil {
		return err
	}
	if err := validateConfigName(value.Name); err != nil {
		return err
	}
	if len(value.Value) == 0 || !json.Valid(value.Value) {
		return errors.New("config value must be valid JSON")
	}
	return nil
}

func validateConfigType(configType string) error {
	if configType == "" || strings.TrimSpace(configType) != configType {
		return errors.New("config type is required and must not have surrounding whitespace")
	}
	return nil
}

func validateConfigName(name string) error {
	if name == "" || strings.TrimSpace(name) != name {
		return errors.New("config name is required and must not have surrounding whitespace")
	}
	return nil
}

func cloneConfig(value PersistedConfig) PersistedConfig {
	value.Value = append([]byte(nil), value.Value...)
	return value
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

func cloneAPIKey(value PersistedAPIKey) PersistedAPIKey {
	value.Config = append([]byte(nil), value.Config...)
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
