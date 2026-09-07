package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/ai-unisub/ai-unisub/internal/repository"
	"github.com/gin-gonic/gin"
)

type createAPIKeyRequest struct {
	SubscriptionID int64      `json:"subscription_id"`
	Name           string     `json:"name"`
	RPMLimit       *int       `json:"rpm_limit"`
	ExpiresAt      *time.Time `json:"expires_at"`
}

type updateAPIKeyRequest struct {
	Name      string     `json:"name"`
	Enabled   bool       `json:"enabled"`
	RPMLimit  *int       `json:"rpm_limit"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func (s *Server) listAPIKeys(c *gin.Context) {
	keys, err := s.repo.ListAPIKeys(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "internal_error", "could not list API keys")
		return
	}
	if keys == nil {
		keys = []model.APIKey{}
	}
	c.JSON(http.StatusOK, gin.H{"data": keys})
}

func (s *Server) getAPIKey(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	key, err := s.repo.GetAPIKey(c.Request.Context(), id)
	if err != nil {
		handleRepoError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"key": key, "api_key": key.APIKey})
}

func (s *Server) createAPIKey(c *gin.Context) {
	var request createAPIKeyRequest
	if c.ShouldBindJSON(&request) != nil || request.SubscriptionID <= 0 {
		apiError(c, http.StatusBadRequest, "invalid_request", "subscription_id is required")
		return
	}
	if request.RPMLimit != nil && *request.RPMLimit <= 0 {
		apiError(c, http.StatusBadRequest, "invalid_request", "rpm_limit must be greater than zero")
		return
	}
	var userID *int64
	if value, ok := c.Get("user_id"); ok {
		if id, valid := value.(int64); valid && id > 0 {
			userID = &id
		}
	}
	key, plaintext, err := s.repo.CreateAPIKey(c.Request.Context(), repository.CreateAPIKeyParams{
		SubscriptionID: request.SubscriptionID, UserID: userID, Name: strings.TrimSpace(request.Name), RPMLimit: request.RPMLimit, ExpiresAt: request.ExpiresAt,
	})
	if err != nil {
		handleRepoError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"key": key, "api_key": plaintext, "warning": "This API key is shown only once."})
}

func (s *Server) updateAPIKey(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var request updateAPIKeyRequest
	if c.ShouldBindJSON(&request) != nil || strings.TrimSpace(request.Name) == "" {
		apiError(c, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}
	if request.RPMLimit != nil && *request.RPMLimit <= 0 {
		apiError(c, http.StatusBadRequest, "invalid_request", "rpm_limit must be greater than zero")
		return
	}
	key, err := s.repo.UpdateAPIKey(c.Request.Context(), id, repository.UpdateAPIKeyParams{
		Name: strings.TrimSpace(request.Name), Enabled: request.Enabled, RPMLimit: request.RPMLimit, ExpiresAt: request.ExpiresAt,
	})
	if err != nil {
		handleRepoError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"api_key": key})
}

func (s *Server) resetAPIKey(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	plaintext, err := s.repo.ResetAPIKey(c.Request.Context(), id)
	if err != nil {
		handleRepoError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"api_key": plaintext, "warning": "The old key is invalid. This new key is shown only once."})
}

func (s *Server) deleteAPIKey(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := s.repo.DeleteAPIKey(c.Request.Context(), id); err != nil {
		handleRepoError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
