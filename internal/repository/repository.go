package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/cryptox"
	"github.com/ai-unisub/ai-unisub/internal/model"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrUnauthorized = errors.New("unauthorized")
	ErrConflict     = errors.New("conflict")
)

type Repository struct {
	db     *sql.DB
	cipher *cryptox.Cipher
	keyID  string
}

type CreateAccountParams struct {
	Name             string
	Provider         model.Provider
	AuthType         string
	Credentials      model.Credentials
	Metadata         json.RawMessage
	ConcurrencyLimit int
	RPMLimit         *int
	TokenExpiresAt   *time.Time
}

func New(db *sql.DB, cipher *cryptox.Cipher, keyID string) *Repository {
	return &Repository{db: db, cipher: cipher, keyID: keyID}
}

func (r *Repository) BootstrapAdmin(ctx context.Context, username, password string) (bool, error) {
	if password == "" {
		return false, nil
	}
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin`).Scan(&count); err != nil {
		return false, err
	}
	if count != 0 {
		return false, nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO admin(id, username, password_hash) VALUES(1, ?, ?)`, username, string(hash))
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *Repository) AuthenticateAdmin(ctx context.Context, username, password string) error {
	var hash string
	err := r.db.QueryRowContext(ctx, `SELECT password_hash FROM admin WHERE id=1 AND username=?`, username).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return ErrUnauthorized
	}
	return nil
}

func (r *Repository) ChangeAdminPassword(ctx context.Context, username, current, next string) error {
	if err := r.AuthenticateAdmin(ctx, username, current); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE admin SET password_hash=?, updated_at=CURRENT_TIMESTAMP WHERE id=1`, string(hash))
	return err
}

func (r *Repository) CreateAccount(ctx context.Context, p CreateAccountParams) (model.Account, string, error) {
	credentialJSON, err := json.Marshal(p.Credentials)
	if err != nil {
		return model.Account{}, "", err
	}
	encrypted, err := r.cipher.Encrypt(credentialJSON, []byte(r.keyID))
	if err != nil {
		return model.Account{}, "", err
	}
	metadata := p.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	plaintextKey, keyHash, prefix, err := generateAPIKey()
	if err != nil {
		return model.Account{}, "", err
	}
	limit := p.ConcurrencyLimit
	if limit <= 0 {
		limit = 1
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Account{}, "", err
	}
	defer tx.Rollback()
	var expires any
	if p.TokenExpiresAt != nil {
		expires = p.TokenExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO accounts
		(name, provider, auth_type, credentials_encrypted, credential_key_id, metadata_json, concurrency_limit, token_expires_at)
		VALUES(?,?,?,?,?,?,?,?)`, p.Name, p.Provider, p.AuthType, encrypted, r.keyID, string(metadata), limit, expires)
	if err != nil {
		return model.Account{}, "", err
	}
	accountID, err := result.LastInsertId()
	if err != nil {
		return model.Account{}, "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO api_keys(account_id, name, key_hash, key_prefix, rpm_limit, concurrency)
		VALUES(?,?,?,?,?,?)`, accountID, p.Name, keyHash[:], prefix, p.RPMLimit, limit)
	if err != nil {
		return model.Account{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return model.Account{}, "", err
	}
	account, err := r.GetAccount(ctx, accountID)
	return account, plaintextKey, err
}

func (r *Repository) ListAccounts(ctx context.Context) ([]model.Account, error) {
	rows, err := r.db.QueryContext(ctx, accountSelect+` ORDER BY a.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accounts []model.Account
	for rows.Next() {
		account, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (r *Repository) GetAccount(ctx context.Context, id int64) (model.Account, error) {
	account, err := scanAccount(r.db.QueryRowContext(ctx, accountSelect+` WHERE a.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Account{}, ErrNotFound
	}
	return account, err
}

func (r *Repository) ResolveAPIKey(ctx context.Context, plaintext string) (model.ResolvedAccount, error) {
	if !strings.HasPrefix(plaintext, "unisub_") || len(plaintext) < 32 {
		return model.ResolvedAccount{}, ErrUnauthorized
	}
	hash := sha256.Sum256([]byte(plaintext))
	query := `SELECT ` + accountColumns + `, k.id, k.rpm_limit
		FROM api_keys k JOIN accounts a ON a.id=k.account_id
		WHERE k.key_hash=? AND k.enabled=1 AND a.enabled=1
		AND (k.expires_at IS NULL OR k.expires_at > ?)`
	row := r.db.QueryRowContext(ctx, query, hash[:], time.Now().UTC().Format(time.RFC3339Nano))
	account, apiKeyID, rpmLimit, err := scanResolved(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ResolvedAccount{}, ErrUnauthorized
	}
	if err != nil {
		return model.ResolvedAccount{}, err
	}
	if account.Status != "active" {
		return model.ResolvedAccount{}, ErrUnauthorized
	}
	return model.ResolvedAccount{Account: account, APIKeyID: apiKeyID, RPMLimit: rpmLimit}, nil
}

func (r *Repository) Credentials(ctx context.Context, account model.Account) (model.Credentials, error) {
	plaintext, err := r.cipher.Decrypt(account.CredentialsEnc, []byte(account.CredentialKeyID))
	if err != nil {
		return model.Credentials{}, err
	}
	var credentials model.Credentials
	if err := json.Unmarshal(plaintext, &credentials); err != nil {
		return model.Credentials{}, errors.New("stored credentials are invalid")
	}
	return credentials, nil
}

func (r *Repository) SetAccountEnabled(ctx context.Context, id int64, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	result, err := r.db.ExecContext(ctx, `UPDATE accounts SET enabled=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, value, id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (r *Repository) DeleteAccount(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM accounts WHERE id=?`, id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (r *Repository) ResetAPIKey(ctx context.Context, id int64) (string, error) {
	plaintext, hash, prefix, err := generateAPIKey()
	if err != nil {
		return "", err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE api_keys SET key_hash=?, key_prefix=?, created_at=CURRENT_TIMESTAMP WHERE account_id=?`, hash[:], prefix, id)
	if err != nil {
		return "", err
	}
	if err := requireAffected(result); err != nil {
		return "", err
	}
	return plaintext, nil
}

func (r *Repository) RecordUsage(ctx context.Context, accountID int64, provider model.Provider, endpoint string, status int, started time.Time, requestID string) {
	finished := time.Now().UTC()
	_, _ = r.db.ExecContext(ctx, `INSERT INTO usage_logs(account_id, provider, endpoint, status_code, started_at, finished_at, request_id)
		VALUES(?,?,?,?,?,?,?)`, accountID, provider, endpoint, status, started.UTC().Format(time.RFC3339Nano), finished.Format(time.RFC3339Nano), requestID)
	_, _ = r.db.ExecContext(ctx, `UPDATE accounts SET last_used_at=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, finished.Format(time.RFC3339Nano), accountID)
}

func (r *Repository) UsageSummary(ctx context.Context, accountID int64) (model.UsageSummary, error) {
	var summary model.UsageSummary
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
		FROM usage_logs WHERE account_id=? AND started_at>=?`, accountID, time.Now().UTC().Add(-24*time.Hour).Format(time.RFC3339Nano)).
		Scan(&summary.Requests24H, &summary.InputTokens24H, &summary.OutputTokens24H)
	return summary, err
}

const accountColumns = `a.id, a.name, a.provider, a.auth_type, a.credentials_encrypted, a.credential_key_id,
	a.metadata_json, a.status, a.enabled, a.concurrency_limit, a.token_expires_at, a.quota_json,
	a.quota_checked_at, a.quota_error, a.last_used_at, a.last_error, k.key_prefix, a.created_at, a.updated_at`

const accountSelect = `SELECT ` + accountColumns + ` FROM accounts a JOIN api_keys k ON k.account_id=a.id`

type scanner interface{ Scan(...any) error }

func scanAccount(s scanner) (model.Account, error) {
	var a model.Account
	var provider, metadata, created, updated string
	var enabled int
	var tokenExpires, quota, quotaChecked, quotaError, lastUsed, lastError sql.NullString
	err := s.Scan(&a.ID, &a.Name, &provider, &a.AuthType, &a.CredentialsEnc, &a.CredentialKeyID,
		&metadata, &a.Status, &enabled, &a.ConcurrencyLimit, &tokenExpires, &quota,
		&quotaChecked, &quotaError, &lastUsed, &lastError, &a.APIKeyPrefix, &created, &updated)
	if err != nil {
		return model.Account{}, err
	}
	a.Provider = model.Provider(provider)
	a.Metadata = json.RawMessage(metadata)
	a.Enabled = enabled == 1
	a.TokenExpiresAt = parseTime(tokenExpires)
	a.QuotaCheckedAt = parseTime(quotaChecked)
	a.LastUsedAt = parseTime(lastUsed)
	if quota.Valid {
		a.Quota = json.RawMessage(quota.String)
	}
	if quotaError.Valid {
		a.QuotaError = &quotaError.String
	}
	if lastError.Valid {
		a.LastError = &lastError.String
	}
	a.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", created)
	a.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updated)
	return a, nil
}

func scanResolved(s scanner) (model.Account, int64, *int, error) {
	a, err := scanAccountWithTail(s)
	return a.account, a.apiKeyID, a.rpmLimit, err
}

type accountWithTail struct {
	account  model.Account
	apiKeyID int64
	rpmLimit *int
}

func scanAccountWithTail(s scanner) (accountWithTail, error) {
	var a model.Account
	var provider, metadata, created, updated string
	var enabled int
	var tokenExpires, quota, quotaChecked, quotaError, lastUsed, lastError sql.NullString
	var apiKeyID int64
	var rpmLimit sql.NullInt64
	err := s.Scan(&a.ID, &a.Name, &provider, &a.AuthType, &a.CredentialsEnc, &a.CredentialKeyID,
		&metadata, &a.Status, &enabled, &a.ConcurrencyLimit, &tokenExpires, &quota,
		&quotaChecked, &quotaError, &lastUsed, &lastError, &a.APIKeyPrefix, &created, &updated, &apiKeyID, &rpmLimit)
	if err != nil {
		return accountWithTail{}, err
	}
	a.Provider = model.Provider(provider)
	a.Metadata = json.RawMessage(metadata)
	a.Enabled = enabled == 1
	a.TokenExpiresAt = parseTime(tokenExpires)
	a.QuotaCheckedAt = parseTime(quotaChecked)
	a.LastUsedAt = parseTime(lastUsed)
	if quota.Valid {
		a.Quota = json.RawMessage(quota.String)
	}
	if quotaError.Valid {
		a.QuotaError = &quotaError.String
	}
	if lastError.Valid {
		a.LastError = &lastError.String
	}
	a.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", created)
	a.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updated)
	var limit *int
	if rpmLimit.Valid {
		value := int(rpmLimit.Int64)
		limit = &value
	}
	return accountWithTail{account: a, apiKeyID: apiKeyID, rpmLimit: limit}, nil
}

func parseTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value.String); err == nil {
			return &parsed
		}
	}
	return nil
}

func generateAPIKey() (string, [32]byte, string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", [32]byte{}, "", err
	}
	plaintext := "unisub_" + base64.RawURLEncoding.EncodeToString(random)
	hash := sha256.Sum256([]byte(plaintext))
	return plaintext, hash, plaintext[:15], nil
}

func requireAffected(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) Close() error { return r.db.Close() }

func (r *Repository) String() string { return fmt.Sprintf("Repository(key_id=%s)", r.keyID) }
