package oauth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// FileCredentialStore stores exactly one opaque OAuth credential per file.
// Writes use a sibling temporary file followed by an atomic rename.
type FileCredentialStore struct{}

func (FileCredentialStore) LoadCredential(path string) (json.RawMessage, error) {
	if path == "" {
		return nil, errors.New("credential file path is empty")
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !json.Valid(value) {
		return nil, errors.New("credential file is invalid JSON")
	}
	return json.RawMessage(value), nil
}

func (FileCredentialStore) SaveCredential(path string, value json.RawMessage) error {
	if path == "" || len(value) == 0 || !json.Valid(value) {
		return errors.New("credential file path and valid JSON are required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".oauth-credential-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(value); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return nil
}

func (FileCredentialStore) DeleteCredential(path string) error {
	if path == "" {
		return errors.New("credential file path is empty")
	}
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
