package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// fileCredentialStore stores exactly one opaque OAuth credential per file.
// Writes use a sibling temporary file followed by an atomic rename.
type fileCredentialStore struct{}

func (fileCredentialStore) LoadCredential(path string) (json.RawMessage, error) {
	if path == "" {
		return nil, errCredentialFilePathEmpty
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !json.Valid(value) {
		return nil, errCredentialFileInvalidJSON
	}
	return json.RawMessage(value), nil
}

func (fileCredentialStore) SaveCredential(path string, value json.RawMessage) error {
	if path == "" || len(value) == 0 || !json.Valid(value) {
		return errCredentialFileSaveInvalid
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

func (fileCredentialStore) DeleteCredential(path string) error {
	if path == "" {
		return errCredentialFilePathEmpty
	}
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
