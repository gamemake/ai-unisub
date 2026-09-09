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

	"github.com/ai-unisub/ai-unisub/internal/database"
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
	db *database.DB
}

type CreateSubscriptionParams struct {
	Name                           string
	Provider                       model.Provider
	Credentials                    model.Credentials
	Metadata                       json.RawMessage
	ConcurrencyLimit               int
	ConcurrencyQueueTimeoutSeconds int
	ProxyURL                       string
	TokenExpiresAt                 *time.Time
}

type UpdateSubscriptionParams struct {
	Name                           string
	Enabled                        bool
	ConcurrencyLimit               int
	ConcurrencyQueueTimeoutSeconds int
	ProxyURL                       *string
}

type CreateAPIKeyParams struct {
	SubscriptionID int64
	UserID         *int64
	Name           string
	RPMLimit       *int
	ExpiresAt      *time.Time
}

type UpdateAPIKeyParams struct {
	Name      string
	Enabled   bool
	RPMLimit  *int
	ExpiresAt *time.Time
}

func New(db *database.DB) *Repository {
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

func (r *Repository) SetUserPassword(ctx context.Context, id int64, password string) error {
	if _, err := r.GetUser(ctx, id); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE users SET password_hash=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, string(hash), id)
	if err != nil {
		return err
	}
	return requireAffected(result)
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
	id, err := r.db.InsertID(ctx, `INSERT INTO users(username, password_hash, role) VALUES(?,?,?)`, username, string(hash), role)
	if err != nil {
		if isUniqueViolation(err) {
			return model.User{}, ErrConflict
		}
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
	return database.IsUniqueViolation(err)
}

func (r *Repository) CreateSubscription(ctx context.Context, p CreateSubscriptionParams) (model.Subscription, error) {
	credentialJSON, err := json.Marshal(p.Credentials)
	if err != nil {
		return model.Subscription{}, err
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
	subscriptionID, err := r.db.InsertID(ctx, `INSERT INTO subscriptions
		(name, provider, credentials_json, metadata_json, proxy_url, concurrency_limit, concurrency_queue_timeout_seconds, token_expires_at)
		VALUES(?,?,?,?,?,?,?,?)`, p.Name, p.Provider, credentialJSON, string(metadata), p.ProxyURL, limit, p.ConcurrencyQueueTimeoutSeconds, expires)
	if err != nil {
		return model.Subscription{}, err
	}
	return r.GetSubscription(ctx, subscriptionID)
}

func (r *Repository) ListSubscriptions(ctx context.Context) ([]model.Subscription, error) {
	rows, err := r.db.QueryContext(ctx, subscriptionSelect+` ORDER BY a.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var subscriptions []model.Subscription
	for rows.Next() {
		subscription, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		subscriptions = append(subscriptions, subscription)
	}
	return subscriptions, rows.Err()
}

func (r *Repository) GetSubscription(ctx context.Context, id int64) (model.Subscription, error) {
	subscription, err := scanSubscription(r.db.QueryRowContext(ctx, subscriptionSelect+` WHERE a.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Subscription{}, ErrNotFound
	}
	return subscription, err
}

func (r *Repository) ResolveAPIKey(ctx context.Context, plaintext string) (model.ResolvedSubscription, error) {
	if !strings.HasPrefix(plaintext, "unisub_") || len(plaintext) < 32 {
		return model.ResolvedSubscription{}, ErrUnauthorized
	}
	hash := sha256.Sum256([]byte(plaintext))
	query := `SELECT ` + subscriptionBaseColumns + `, k.id, k.user_id, k.rpm_limit
		FROM api_keys k JOIN subscriptions a ON a.id=k.subscription_id
		WHERE k.key_hash=? AND k.enabled=1 AND a.enabled=1
		AND (k.expires_at IS NULL OR k.expires_at > ?)`
	row := r.db.QueryRowContext(ctx, query, hash[:], time.Now().UTC().Format(time.RFC3339Nano))
	subscription, apiKeyID, userID, rpmLimit, err := scanResolved(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ResolvedSubscription{}, ErrUnauthorized
	}
	if err != nil {
		return model.ResolvedSubscription{}, err
	}
	if subscription.Status != "active" {
		return model.ResolvedSubscription{}, ErrUnauthorized
	}
	return model.ResolvedSubscription{Subscription: subscription, APIKeyID: apiKeyID, UserID: userID, RPMLimit: rpmLimit}, nil
}

func (r *Repository) Credentials(ctx context.Context, subscription model.Subscription) (model.Credentials, error) {
	var credentials model.Credentials
	if err := json.Unmarshal(subscription.CredentialsJSON, &credentials); err != nil {
		return model.Credentials{}, errors.New("stored credentials are invalid")
	}
	return credentials, nil
}

func (r *Repository) UpdateSubscriptionCredentials(ctx context.Context, id int64, credentials model.Credentials, expiresAt *time.Time) error {
	credentialJSON, err := json.Marshal(credentials)
	if err != nil {
		return err
	}
	var expires any
	if expiresAt != nil {
		expires = expiresAt.UTC().Format(time.RFC3339Nano)
	}
	result, err := r.db.ExecContext(ctx, `UPDATE subscriptions
		SET credentials_json=?, token_expires_at=?, status='active', last_error=NULL, updated_at=CURRENT_TIMESTAMP
		WHERE id=?`, credentialJSON, expires, id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (r *Repository) UpdateSubscriptionQuota(ctx context.Context, id int64, quota json.RawMessage, checkedAt time.Time, quotaErr *string) error {
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
	result, err := r.db.ExecContext(ctx, `UPDATE subscriptions
		SET quota_json=?, quota_checked_at=?, quota_error=?, updated_at=CURRENT_TIMESTAMP
		WHERE id=?`, quotaValue, checkedAt.UTC().Format(time.RFC3339Nano), errorValue, id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (r *Repository) UpdateSubscriptionQuotaError(ctx context.Context, id int64, quotaErr string) error {
	quotaErr = strings.TrimSpace(quotaErr)
	var errorValue any
	if quotaErr != "" {
		errorValue = quotaErr
	}
	result, err := r.db.ExecContext(ctx, `UPDATE subscriptions SET quota_error=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, errorValue, id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (r *Repository) ProxyURL(subscription model.Subscription) (string, error) {
	return subscription.ProxyURL, nil
}

func (r *Repository) SetSubscriptionProxy(ctx context.Context, id int64, proxyURL string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE subscriptions SET proxy_url=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, proxyURL, id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (r *Repository) SetConcurrencyQueueTimeout(ctx context.Context, id int64, timeoutSeconds int) error {
	result, err := r.db.ExecContext(ctx, `UPDATE subscriptions SET concurrency_queue_timeout_seconds=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, timeoutSeconds, id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (r *Repository) UpdateSubscription(ctx context.Context, id int64, p UpdateSubscriptionParams) (model.Subscription, error) {
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
		return model.Subscription{}, err
	}
	defer tx.Rollback()

	var result sql.Result
	if p.ProxyURL != nil {
		result, err = tx.ExecContext(ctx, `UPDATE subscriptions
			SET name=?, enabled=?, concurrency_limit=?, concurrency_queue_timeout_seconds=?, proxy_url=?, updated_at=CURRENT_TIMESTAMP
			WHERE id=?`, p.Name, enabled, limit, p.ConcurrencyQueueTimeoutSeconds, *p.ProxyURL, id)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE subscriptions
			SET name=?, enabled=?, concurrency_limit=?, concurrency_queue_timeout_seconds=?, updated_at=CURRENT_TIMESTAMP
			WHERE id=?`, p.Name, enabled, limit, p.ConcurrencyQueueTimeoutSeconds, id)
	}
	if err != nil {
		return model.Subscription{}, err
	}
	if err := requireAffected(result); err != nil {
		return model.Subscription{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Subscription{}, err
	}
	return r.GetSubscription(ctx, id)
}

func (r *Repository) SetSubscriptionEnabled(ctx context.Context, id int64, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	result, err := r.db.ExecContext(ctx, `UPDATE subscriptions SET enabled=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, value, id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (r *Repository) DeleteSubscription(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM subscriptions WHERE id=?`, id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (r *Repository) CreateAPIKey(ctx context.Context, p CreateAPIKeyParams) (model.APIKey, string, error) {
	if _, err := r.GetSubscription(ctx, p.SubscriptionID); err != nil {
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
	id, err := r.db.InsertID(ctx, `INSERT INTO api_keys(subscription_id, user_id, name, key_hash, key_plaintext, key_prefix, rpm_limit, expires_at)
		VALUES(?,?,?,?,?,?,?,?)`, p.SubscriptionID, nullableInt64(p.UserID), name, hash[:], plaintext, prefix, rpm, expires)
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

func (r *Repository) UsageSummary(ctx context.Context, subscriptionID int64) (model.UsageSummary, error) {
	totals, err := r.usageTotalsFromRequestLogs(ctx, subscriptionID, time.Now().UTC().Add(-24*time.Hour), time.Now().UTC(), time.Local)
	if err != nil {
		return model.UsageSummary{}, err
	}
	return totals.As24HSummary(), nil
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
	user.CreatedAt, _ = parseLogTime(created)
	user.UpdatedAt, _ = parseLogTime(updated)
	return user, nil
}

const subscriptionBaseColumns = `a.id, a.name, a.provider, a.credentials_json,
	a.metadata_json, a.proxy_url, a.status, a.enabled, a.concurrency_limit, a.concurrency_queue_timeout_seconds, a.token_expires_at, a.quota_json,
	a.quota_checked_at, a.quota_error, a.last_used_at, a.last_error, a.created_at, a.updated_at`

const subscriptionSelect = `SELECT ` + subscriptionBaseColumns + `, (SELECT COUNT(*) FROM api_keys keys WHERE keys.subscription_id=a.id) FROM subscriptions a`

const apiKeySelect = `SELECT k.id, k.subscription_id, k.user_id, COALESCE(u.username,''), a.name, a.provider, k.name, k.key_prefix, k.enabled, k.rpm_limit, k.expires_at, k.created_at
	FROM api_keys k JOIN subscriptions a ON a.id=k.subscription_id LEFT JOIN users u ON u.id=k.user_id`

type scanner interface{ Scan(...any) error }

type subscriptionScan struct {
	subscription model.Subscription
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

func subscriptionScanDest(row *subscriptionScan) []any {
	return []any{
		&row.subscription.ID, &row.subscription.Name, &row.provider, &row.subscription.CredentialsJSON,
		&row.metadata, &row.subscription.ProxyURL, &row.subscription.Status, &row.enabled, &row.subscription.ConcurrencyLimit, &row.subscription.ConcurrencyQueueTimeoutSeconds, &row.tokenExpires, &row.quota,
		&row.quotaChecked, &row.quotaError, &row.lastUsed, &row.lastError, &row.created, &row.updated,
	}
}

func finishSubscription(row subscriptionScan) model.Subscription {
	a := row.subscription
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
	a.CreatedAt, _ = parseLogTime(row.created)
	a.UpdatedAt, _ = parseLogTime(row.updated)
	return a
}

func scanSubscription(s scanner) (model.Subscription, error) {
	var row subscriptionScan
	dest := append(subscriptionScanDest(&row), &row.subscription.APIKeyCount)
	if err := s.Scan(dest...); err != nil {
		return model.Subscription{}, err
	}
	return finishSubscription(row), nil
}

func scanResolved(s scanner) (model.Subscription, int64, *int64, *int, error) {
	var row subscriptionScan
	var apiKeyID int64
	var userID sql.NullInt64
	var rpm sql.NullInt64
	dest := append(subscriptionScanDest(&row), &apiKeyID, &userID, &rpm)
	if err := s.Scan(dest...); err != nil {
		return model.Subscription{}, 0, nil, nil, err
	}
	var limit *int
	if rpm.Valid {
		value := int(rpm.Int64)
		limit = &value
	}
	return finishSubscription(row), apiKeyID, nullInt64Ptr(userID), limit, nil
}

func scanAPIKey(s scanner) (model.APIKey, error) {
	var key model.APIKey
	var userID sql.NullInt64
	var provider string
	var enabled int
	var rpm sql.NullInt64
	var expires, created sql.NullString
	if err := s.Scan(&key.ID, &key.SubscriptionID, &userID, &key.Username, &key.SubscriptionName, &provider, &key.Name, &key.KeyPrefix, &enabled, &rpm, &expires, &created); err != nil {
		return model.APIKey{}, err
	}
	key.UserID = nullInt64Ptr(userID)
	key.Provider = model.Provider(provider)
	key.Enabled = enabled == 1
	if rpm.Valid {
		value := int(rpm.Int64)
		key.RPMLimit = &value
	}
	key.ExpiresAt = parseTime(expires)
	if created.Valid {
		if parsed, err := parseLogTime(created.String); err == nil {
			key.CreatedAt = parsed
		}
	}
	return key, nil
}

func parseTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed, err := parseLogTime(value.String)
	if err != nil {
		return nil
	}
	return &parsed
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
