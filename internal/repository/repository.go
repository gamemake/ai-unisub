package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrUnauthorized = errors.New("unauthorized")
	ErrConflict     = errors.New("conflict")
	ErrLastAdmin    = errors.New("cannot remove the last admin")
)

type Repository struct {
	db *sql.DB
}

type CreateAccountParams struct {
	Name                           string
	Provider                       model.Provider
	AuthType                       string
	Credentials                    model.Credentials
	Metadata                       json.RawMessage
	ConcurrencyLimit               int
	ConcurrencyQueueTimeoutSeconds int
	ProxyURL                       string
	TokenExpiresAt                 *time.Time
}

type UpdateAccountParams struct {
	Name                           string
	Enabled                        bool
	ConcurrencyLimit               int
	ConcurrencyQueueTimeoutSeconds int
	ProxyURL                       *string
}

type CreateAPIKeyParams struct {
	AccountID int64
	Name      string
	RPMLimit  *int
	ExpiresAt *time.Time
}

type UpdateAPIKeyParams struct {
	Name      string
	Enabled   bool
	RPMLimit  *int
	ExpiresAt *time.Time
}

func New(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) BootstrapAdmin(ctx context.Context, username, password string) (bool, error) {
	if password == "" {
		return false, nil
	}
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, err
	}
	if count != 0 {
		return false, nil
	}
	_, err := r.CreateUser(ctx, strings.TrimSpace(username), password, model.RoleAdmin)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *Repository) AuthenticateAdmin(ctx context.Context, username, password string) error {
	_, err := r.AuthenticateUser(ctx, username, password)
	return err
}

func (r *Repository) AuthenticateUser(ctx context.Context, username, password string) (model.User, error) {
	user, err := r.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return model.User{}, ErrUnauthorized
		}
		return model.User{}, err
	}
	if !user.Enabled || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return model.User{}, ErrUnauthorized
	}
	return user, nil
}

func (r *Repository) ChangeAdminPassword(ctx context.Context, username, current, next string) error {
	if _, err := r.AuthenticateUser(ctx, username, current); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE users SET password_hash=?, updated_at=CURRENT_TIMESTAMP WHERE username=?`, string(hash), username)
	return err
}

func (r *Repository) ListUsers(ctx context.Context) ([]model.User, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []model.User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (r *Repository) GetUserByUsername(ctx context.Context, username string) (model.User, error) {
	user, err := scanUser(r.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE username=?`, username))
	if errors.Is(err, sql.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	return user, err
}

func (r *Repository) GetUser(ctx context.Context, id int64) (model.User, error) {
	user, err := scanUser(r.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	return user, err
}

func (r *Repository) CreateUser(ctx context.Context, username, password string, role model.UserRole) (model.User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return model.User{}, errors.New("username is required")
	}
	if !role.Valid() {
		role = model.RoleUser
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return model.User{}, err
	}
	result, err := r.db.ExecContext(ctx, `INSERT INTO users(username, password_hash, role) VALUES(?,?,?)`, username, string(hash), role)
	if err != nil {
		if isUniqueViolation(err) {
			return model.User{}, ErrConflict
		}
		return model.User{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.User{}, err
	}
	return r.GetUser(ctx, id)
}

func (r *Repository) UpdateUser(ctx context.Context, id int64, role model.UserRole, enabled bool) (model.User, error) {
	user, err := r.GetUser(ctx, id)
	if err != nil {
		return model.User{}, err
	}
	if !role.Valid() {
		return model.User{}, errors.New("invalid role")
	}
	if user.Role == model.RoleAdmin && (role != model.RoleAdmin || !enabled) {
		if err := r.ensureRemainingAdmin(ctx, id); err != nil {
			return model.User{}, err
		}
	}
	value := 0
	if enabled {
		value = 1
	}
	result, err := r.db.ExecContext(ctx, `UPDATE users SET role=?, enabled=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, role, value, id)
	if err != nil {
		return model.User{}, err
	}
	if err := requireAffected(result); err != nil {
		return model.User{}, err
	}
	return r.GetUser(ctx, id)
}

func (r *Repository) DeleteUser(ctx context.Context, id int64) error {
	user, err := r.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if user.Role == model.RoleAdmin {
		if err := r.ensureRemainingAdmin(ctx, id); err != nil {
			return err
		}
	}
	result, err := r.db.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (r *Repository) ensureRemainingAdmin(ctx context.Context, exceptID int64) error {
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role='admin' AND enabled=1 AND id!=?`, exceptID).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return ErrLastAdmin
	}
	return nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

func (r *Repository) CreateAccount(ctx context.Context, p CreateAccountParams) (model.Account, error) {
	credentialJSON, err := json.Marshal(p.Credentials)
	if err != nil {
		return model.Account{}, err
	}
	metadata := p.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	limit := p.ConcurrencyLimit
	if limit <= 0 {
		limit = 1
	}
	var expires any
	if p.TokenExpiresAt != nil {
		expires = p.TokenExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	result, err := r.db.ExecContext(ctx, `INSERT INTO accounts
		(name, provider, auth_type, credentials_json, metadata_json, proxy_url, concurrency_limit, concurrency_queue_timeout_seconds, token_expires_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, p.Name, p.Provider, p.AuthType, credentialJSON, string(metadata), p.ProxyURL, limit, p.ConcurrencyQueueTimeoutSeconds, expires)
	if err != nil {
		return model.Account{}, err
	}
	accountID, err := result.LastInsertId()
	if err != nil {
		return model.Account{}, err
	}
	return r.GetAccount(ctx, accountID)
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
	query := `SELECT ` + accountBaseColumns + `, k.id, k.rpm_limit
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
	var credentials model.Credentials
	if err := json.Unmarshal(account.CredentialsJSON, &credentials); err != nil {
		return model.Credentials{}, errors.New("stored credentials are invalid")
	}
	return credentials, nil
}

func (r *Repository) UpdateAccountCredentials(ctx context.Context, id int64, credentials model.Credentials, expiresAt *time.Time) error {
	credentialJSON, err := json.Marshal(credentials)
	if err != nil {
		return err
	}
	var expires any
	if expiresAt != nil {
		expires = expiresAt.UTC().Format(time.RFC3339Nano)
	}
	result, err := r.db.ExecContext(ctx, `UPDATE accounts
		SET credentials_json=?, token_expires_at=?, status='active', last_error=NULL, updated_at=CURRENT_TIMESTAMP
		WHERE id=?`, credentialJSON, expires, id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (r *Repository) UpdateAccountQuota(ctx context.Context, id int64, quota json.RawMessage, checkedAt time.Time, quotaErr *string) error {
	var quotaValue any
	if len(quota) > 0 {
		if !json.Valid(quota) {
			return errors.New("quota must be valid JSON")
		}
		quotaValue = string(quota)
	}
	var errorValue any
	if quotaErr != nil && strings.TrimSpace(*quotaErr) != "" {
		errorValue = strings.TrimSpace(*quotaErr)
	}
	result, err := r.db.ExecContext(ctx, `UPDATE accounts
		SET quota_json=?, quota_checked_at=?, quota_error=?, updated_at=CURRENT_TIMESTAMP
		WHERE id=?`, quotaValue, checkedAt.UTC().Format(time.RFC3339Nano), errorValue, id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (r *Repository) UpdateAccountQuotaError(ctx context.Context, id int64, quotaErr string) error {
	quotaErr = strings.TrimSpace(quotaErr)
	var errorValue any
	if quotaErr != "" {
		errorValue = quotaErr
	}
	result, err := r.db.ExecContext(ctx, `UPDATE accounts SET quota_error=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, errorValue, id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (r *Repository) ProxyURL(account model.Account) (string, error) {
	return account.ProxyURL, nil
}

func (r *Repository) SetAccountProxy(ctx context.Context, id int64, proxyURL string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE accounts SET proxy_url=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, proxyURL, id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (r *Repository) SetConcurrencyQueueTimeout(ctx context.Context, id int64, timeoutSeconds int) error {
	result, err := r.db.ExecContext(ctx, `UPDATE accounts SET concurrency_queue_timeout_seconds=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, timeoutSeconds, id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (r *Repository) UpdateAccount(ctx context.Context, id int64, p UpdateAccountParams) (model.Account, error) {
	limit := p.ConcurrencyLimit
	if limit <= 0 {
		limit = 1
	}
	enabled := 0
	if p.Enabled {
		enabled = 1
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Account{}, err
	}
	defer tx.Rollback()

	var result sql.Result
	if p.ProxyURL != nil {
		result, err = tx.ExecContext(ctx, `UPDATE accounts
			SET name=?, enabled=?, concurrency_limit=?, concurrency_queue_timeout_seconds=?, proxy_url=?, updated_at=CURRENT_TIMESTAMP
			WHERE id=?`, p.Name, enabled, limit, p.ConcurrencyQueueTimeoutSeconds, *p.ProxyURL, id)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE accounts
			SET name=?, enabled=?, concurrency_limit=?, concurrency_queue_timeout_seconds=?, updated_at=CURRENT_TIMESTAMP
			WHERE id=?`, p.Name, enabled, limit, p.ConcurrencyQueueTimeoutSeconds, id)
	}
	if err != nil {
		return model.Account{}, err
	}
	if err := requireAffected(result); err != nil {
		return model.Account{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Account{}, err
	}
	return r.GetAccount(ctx, id)
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

func (r *Repository) CreateAPIKey(ctx context.Context, p CreateAPIKeyParams) (model.APIKey, string, error) {
	if _, err := r.GetAccount(ctx, p.AccountID); err != nil {
		return model.APIKey{}, "", err
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = "default"
	}
	plaintext, hash, prefix, err := generateAPIKey()
	if err != nil {
		return model.APIKey{}, "", err
	}
	var rpm any
	if p.RPMLimit != nil {
		rpm = *p.RPMLimit
	}
	var expires any
	if p.ExpiresAt != nil {
		expires = p.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	result, err := r.db.ExecContext(ctx, `INSERT INTO api_keys(account_id, name, key_hash, key_plaintext, key_prefix, rpm_limit, expires_at)
		VALUES(?,?,?,?,?,?,?)`, p.AccountID, name, hash[:], plaintext, prefix, rpm, expires)
	if err != nil {
		return model.APIKey{}, "", err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.APIKey{}, "", err
	}
	key, err := r.GetAPIKey(ctx, id)
	return key, plaintext, err
}

func (r *Repository) ListAPIKeys(ctx context.Context) ([]model.APIKey, error) {
	rows, err := r.db.QueryContext(ctx, apiKeySelect+` ORDER BY k.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []model.APIKey
	for rows.Next() {
		key, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (r *Repository) GetAPIKey(ctx context.Context, id int64) (model.APIKey, error) {
	key, err := scanAPIKey(r.db.QueryRowContext(ctx, apiKeySelect+` WHERE k.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.APIKey{}, ErrNotFound
	}
	if err != nil {
		return model.APIKey{}, err
	}
	var plaintext sql.NullString
	if err := r.db.QueryRowContext(ctx, `SELECT key_plaintext FROM api_keys WHERE id=?`, id).Scan(&plaintext); err != nil {
		return model.APIKey{}, err
	}
	key.APIKey = plaintext.String
	return key, nil
}

func (r *Repository) UpdateAPIKey(ctx context.Context, id int64, p UpdateAPIKeyParams) (model.APIKey, error) {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return model.APIKey{}, errors.New("name is required")
	}
	enabled := 0
	if p.Enabled {
		enabled = 1
	}
	var rpm any
	if p.RPMLimit != nil {
		rpm = *p.RPMLimit
	}
	var expires any
	if p.ExpiresAt != nil {
		expires = p.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	result, err := r.db.ExecContext(ctx, `UPDATE api_keys SET name=?, enabled=?, rpm_limit=?, expires_at=? WHERE id=?`, name, enabled, rpm, expires, id)
	if err != nil {
		return model.APIKey{}, err
	}
	if err := requireAffected(result); err != nil {
		return model.APIKey{}, err
	}
	return r.GetAPIKey(ctx, id)
}

func (r *Repository) ResetAPIKey(ctx context.Context, id int64) (string, error) {
	plaintext, hash, prefix, err := generateAPIKey()
	if err != nil {
		return "", err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE api_keys SET key_hash=?, key_plaintext=?, key_prefix=?, created_at=CURRENT_TIMESTAMP WHERE id=?`, hash[:], plaintext, prefix, id)
	if err != nil {
		return "", err
	}
	if err := requireAffected(result); err != nil {
		return "", err
	}
	return plaintext, nil
}

func (r *Repository) DeleteAPIKey(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id=?`, id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (r *Repository) RecordUsage(ctx context.Context, entry UsageLog) {
	finished := time.Now().UTC()
	_, _ = r.db.ExecContext(ctx, `INSERT INTO usage_logs(account_id, provider, endpoint, model, status_code, input_tokens, output_tokens, started_at, finished_at, request_id)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, entry.AccountID, entry.Provider, entry.Endpoint, nullableString(entry.Model), entry.StatusCode,
		nullableInt64(entry.InputTokens), nullableInt64(entry.OutputTokens),
		entry.StartedAt.UTC().Format(time.RFC3339Nano), finished.Format(time.RFC3339Nano), entry.RequestID)
	_, _ = r.db.ExecContext(ctx, `UPDATE accounts SET last_used_at=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, finished.Format(time.RFC3339Nano), entry.AccountID)
}

func (r *Repository) UsageSummary(ctx context.Context, accountID int64) (model.UsageSummary, error) {
	var summary model.UsageSummary
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
		FROM usage_logs WHERE account_id=? AND started_at>=?`, accountID, time.Now().UTC().Add(-24*time.Hour).Format(time.RFC3339Nano)).
		Scan(&summary.Requests24H, &summary.InputTokens24H, &summary.OutputTokens24H)
	return summary, err
}

const userColumns = `id, username, password_hash, role, enabled, created_at, updated_at`

func scanUser(s scanner) (model.User, error) {
	var user model.User
	var role string
	var enabled int
	var created, updated string
	if err := s.Scan(&user.ID, &user.Username, &user.PasswordHash, &role, &enabled, &created, &updated); err != nil {
		return model.User{}, err
	}
	user.Role = model.UserRole(role)
	user.Enabled = enabled == 1
	user.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", created)
	user.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updated)
	return user, nil
}

const accountBaseColumns = `a.id, a.name, a.provider, a.auth_type, a.credentials_json,
	a.metadata_json, a.proxy_url, a.status, a.enabled, a.concurrency_limit, a.concurrency_queue_timeout_seconds, a.token_expires_at, a.quota_json,
	a.quota_checked_at, a.quota_error, a.last_used_at, a.last_error, a.created_at, a.updated_at`

const accountSelect = `SELECT ` + accountBaseColumns + `, (SELECT COUNT(*) FROM api_keys keys WHERE keys.account_id=a.id) FROM accounts a`

const apiKeySelect = `SELECT k.id, k.account_id, a.name, a.provider, k.name, k.key_prefix, k.enabled, k.rpm_limit, k.expires_at, k.created_at
	FROM api_keys k JOIN accounts a ON a.id=k.account_id`

type scanner interface{ Scan(...any) error }

type accountScan struct {
	account      model.Account
	provider     string
	metadata     string
	created      string
	updated      string
	enabled      int
	tokenExpires sql.NullString
	quota        sql.NullString
	quotaChecked sql.NullString
	quotaError   sql.NullString
	lastUsed     sql.NullString
	lastError    sql.NullString
}

func accountScanDest(row *accountScan) []any {
	return []any{
		&row.account.ID, &row.account.Name, &row.provider, &row.account.AuthType, &row.account.CredentialsJSON,
		&row.metadata, &row.account.ProxyURL, &row.account.Status, &row.enabled, &row.account.ConcurrencyLimit, &row.account.ConcurrencyQueueTimeoutSeconds, &row.tokenExpires, &row.quota,
		&row.quotaChecked, &row.quotaError, &row.lastUsed, &row.lastError, &row.created, &row.updated,
	}
}

func finishAccount(row accountScan) model.Account {
	a := row.account
	a.Provider = model.Provider(row.provider)
	a.Metadata = json.RawMessage(row.metadata)
	a.Enabled = row.enabled == 1
	a.ProxyConfigured = a.ProxyURL != ""
	a.TokenExpiresAt = parseTime(row.tokenExpires)
	a.QuotaCheckedAt = parseTime(row.quotaChecked)
	a.LastUsedAt = parseTime(row.lastUsed)
	if row.quota.Valid {
		a.Quota = json.RawMessage(row.quota.String)
	}
	if row.quotaError.Valid {
		a.QuotaError = &row.quotaError.String
	}
	if row.lastError.Valid {
		a.LastError = &row.lastError.String
	}
	a.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", row.created)
	a.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", row.updated)
	return a
}

func scanAccount(s scanner) (model.Account, error) {
	var row accountScan
	dest := append(accountScanDest(&row), &row.account.APIKeyCount)
	if err := s.Scan(dest...); err != nil {
		return model.Account{}, err
	}
	return finishAccount(row), nil
}

func scanResolved(s scanner) (model.Account, int64, *int, error) {
	var row accountScan
	var apiKeyID int64
	var rpm sql.NullInt64
	dest := append(accountScanDest(&row), &apiKeyID, &rpm)
	if err := s.Scan(dest...); err != nil {
		return model.Account{}, 0, nil, err
	}
	var limit *int
	if rpm.Valid {
		value := int(rpm.Int64)
		limit = &value
	}
	return finishAccount(row), apiKeyID, limit, nil
}

func scanAPIKey(s scanner) (model.APIKey, error) {
	var key model.APIKey
	var provider string
	var enabled int
	var rpm sql.NullInt64
	var expires, created sql.NullString
	if err := s.Scan(&key.ID, &key.AccountID, &key.AccountName, &provider, &key.Name, &key.KeyPrefix, &enabled, &rpm, &expires, &created); err != nil {
		return model.APIKey{}, err
	}
	key.Provider = model.Provider(provider)
	key.Enabled = enabled == 1
	if rpm.Valid {
		value := int(rpm.Int64)
		key.RPMLimit = &value
	}
	key.ExpiresAt = parseTime(expires)
	if created.Valid {
		if parsed, err := time.Parse("2006-01-02 15:04:05", created.String); err == nil {
			key.CreatedAt = parsed
		} else if parsed, err := time.Parse(time.RFC3339Nano, created.String); err == nil {
			key.CreatedAt = parsed
		}
	}
	return key, nil
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

func (r *Repository) String() string { return "Repository" }
