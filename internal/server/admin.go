package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/ai-unisub/ai-unisub/internal/repository"
	"github.com/gin-gonic/gin"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) login(c *gin.Context) {
	if !s.allowRate("login:"+clientIP(c.Request), 5, time.Minute) {
		apiError(c, http.StatusTooManyRequests, "rate_limited", "too many login attempts")
		return
	}
	var request loginRequest
	if c.ShouldBindJSON(&request) != nil || request.Username == "" || request.Password == "" {
		apiError(c, http.StatusBadRequest, "invalid_request", "username and password are required")
		return
	}
	if err := s.repo.AuthenticateAdmin(c.Request.Context(), request.Username, request.Password); err != nil {
		apiError(c, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	token, err := s.signer.issue(request.Username, s.cfg.AdminTokenTTL)
	if err != nil {
		apiError(c, 500, "internal_error", "could not issue admin token")
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token, "token_type": "Bearer", "expires_in": int64(s.cfg.AdminTokenTTL.Seconds())})
}

func (s *Server) changePassword(c *gin.Context) {
	var request struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if c.ShouldBindJSON(&request) != nil || len(request.NewPassword) < 12 {
		apiError(c, 400, "invalid_request", "new_password must contain at least 12 characters")
		return
	}
	username, _ := c.Get("admin_username")
	if err := s.repo.ChangeAdminPassword(c.Request.Context(), username.(string), request.CurrentPassword, request.NewPassword); err != nil {
		apiError(c, 401, "invalid_credentials", "current password is invalid")
		return
	}
	c.Status(http.StatusNoContent)
}

type createAccountRequest struct {
	Name             string            `json:"name"`
	Provider         model.Provider    `json:"provider"`
	AuthType         string            `json:"auth_type"`
	Credentials      model.Credentials `json:"credentials"`
	Metadata         json.RawMessage   `json:"metadata"`
	ConcurrencyLimit int               `json:"concurrency_limit"`
	RPMLimit         *int              `json:"rpm_limit"`
	TokenExpiresAt   *time.Time        `json:"token_expires_at"`
}

func (s *Server) createAccount(c *gin.Context) {
	var request createAccountRequest
	if c.ShouldBindJSON(&request) != nil || strings.TrimSpace(request.Name) == "" || !request.Provider.Valid() {
		apiError(c, 400, "invalid_request", "name and a valid provider are required")
		return
	}
	if request.AuthType == "" {
		request.AuthType = "oauth"
	}
	if request.AuthType != "oauth" && request.AuthType != "api_key" {
		apiError(c, 400, "invalid_request", "auth_type must be oauth or api_key")
		return
	}
	if request.Credentials.Bearer() == "" {
		apiError(c, 400, "invalid_request", "credentials must include access_token or api_key")
		return
	}
	if request.Provider == model.ProviderCodex && request.AuthType == "oauth" && request.Credentials.ChatGPTAccountID == "" {
		apiError(c, 400, "invalid_request", "Codex OAuth credentials require chatgpt_account_id")
		return
	}
	if len(request.Metadata) > 0 && !json.Valid(request.Metadata) {
		apiError(c, 400, "invalid_request", "metadata must be valid JSON")
		return
	}
	account, key, err := s.repo.CreateAccount(c.Request.Context(), repository.CreateAccountParams{
		Name: strings.TrimSpace(request.Name), Provider: request.Provider, AuthType: request.AuthType,
		Credentials: request.Credentials, Metadata: request.Metadata, ConcurrencyLimit: request.ConcurrencyLimit,
		RPMLimit: request.RPMLimit, TokenExpiresAt: request.TokenExpiresAt,
	})
	if err != nil {
		apiError(c, 500, "internal_error", "could not create account")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"account": account, "api_key": key, "warning": "This API key is shown only once."})
}

func (s *Server) listAccounts(c *gin.Context) {
	accounts, err := s.repo.ListAccounts(c.Request.Context())
	if err != nil {
		apiError(c, 500, "internal_error", "could not list accounts")
		return
	}
	if accounts == nil {
		accounts = []model.Account{}
	}
	c.JSON(200, gin.H{"data": accounts})
}

func (s *Server) getAccount(c *gin.Context) {
	account, ok := s.accountByParam(c)
	if !ok {
		return
	}
	usage, _ := s.repo.UsageSummary(c.Request.Context(), account.ID)
	c.JSON(200, gin.H{"account": account, "local_usage": usage})
}

func (s *Server) deleteAccount(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := s.repo.DeleteAccount(c.Request.Context(), id); err != nil {
		handleRepoError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) enableAccount(c *gin.Context)  { s.setEnabled(c, true) }
func (s *Server) disableAccount(c *gin.Context) { s.setEnabled(c, false) }

func (s *Server) setEnabled(c *gin.Context, enabled bool) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := s.repo.SetAccountEnabled(c.Request.Context(), id, enabled); err != nil {
		handleRepoError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) resetAPIKey(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	key, err := s.repo.ResetAPIKey(c.Request.Context(), id)
	if err != nil {
		handleRepoError(c, err)
		return
	}
	c.JSON(200, gin.H{"api_key": key, "warning": "The old key is invalid. This new key is shown only once."})
}

func (s *Server) accountUsage(c *gin.Context) {
	account, ok := s.accountByParam(c)
	if !ok {
		return
	}
	usage, err := s.repo.UsageSummary(c.Request.Context(), account.ID)
	if err != nil {
		apiError(c, 500, "internal_error", "could not query usage")
		return
	}
	status := "unknown"
	source := "unavailable"
	if len(account.Quota) > 0 {
		status = "available"
		source = "upstream"
	}
	c.JSON(200, gin.H{"account_id": account.ID, "provider": account.Provider, "status": status, "windows": []any{}, "local_usage": usage, "source": source, "checked_at": account.QuotaCheckedAt, "stale": true, "error": account.QuotaError})
}

func (s *Server) usageSummary(c *gin.Context) {
	accounts, err := s.repo.ListAccounts(c.Request.Context())
	if err != nil {
		apiError(c, 500, "internal_error", "could not query usage")
		return
	}
	data := make([]gin.H, 0, len(accounts))
	for _, account := range accounts {
		usage, _ := s.repo.UsageSummary(c.Request.Context(), account.ID)
		data = append(data, gin.H{"account_id": account.ID, "provider": account.Provider, "name": account.Name, "local_usage": usage})
	}
	c.JSON(200, gin.H{"data": data})
}

func (s *Server) accountByParam(c *gin.Context) (model.Account, bool) {
	id, ok := idParam(c)
	if !ok {
		return model.Account{}, false
	}
	account, err := s.repo.GetAccount(c.Request.Context(), id)
	if err != nil {
		handleRepoError(c, err)
		return model.Account{}, false
	}
	return account, true
}

func idParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		apiError(c, 400, "invalid_request", "invalid account id")
		return 0, false
	}
	return id, true
}

func handleRepoError(c *gin.Context, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		apiError(c, 404, "not_found", "account not found")
		return
	}
	apiError(c, 500, "internal_error", "database operation failed")
}
