package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/ai-unisub/ai-unisub/internal/repository"
	"github.com/gin-gonic/gin"
)

func (s *Server) listRequestLogs(c *gin.Context) {
	if !requireRole(c, model.RoleAdmin) {
		return
	}
	filter := repository.RequestLogFilter{
		Provider: strings.TrimSpace(c.Query("provider")),
		Query:    strings.TrimSpace(c.Query("q")),
	}
	if raw := strings.TrimSpace(c.Query("subscription_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			apiError(c, http.StatusBadRequest, "invalid_request", "invalid subscription_id")
			return
		}
		filter.SubscriptionID = &id
	}
	if raw := strings.TrimSpace(c.Query("api_key_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			apiError(c, http.StatusBadRequest, "invalid_request", "invalid api_key_id")
			return
		}
		filter.APIKeyID = &id
	}
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		status, err := strconv.Atoi(raw)
		if err != nil || status < 100 || status > 599 {
			apiError(c, http.StatusBadRequest, "invalid_request", "invalid status")
			return
		}
		filter.StatusCode = &status
	}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			apiError(c, http.StatusBadRequest, "invalid_request", "invalid limit")
			return
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(c.Query("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			apiError(c, http.StatusBadRequest, "invalid_request", "invalid offset")
			return
		}
		filter.Offset = offset
	}
	logs, total, err := s.repo.ListRequestLogs(c.Request.Context(), time.Local, filter)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "internal_error", "could not list request logs")
		return
	}
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	c.JSON(http.StatusOK, gin.H{"data": logs, "total": total, "limit": limit, "offset": filter.Offset})
}

func (s *Server) getRequestLog(c *gin.Context) {
	if !requireRole(c, model.RoleAdmin) {
		return
	}
	day := c.Param("day")
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		apiError(c, http.StatusBadRequest, "invalid_request", "invalid id")
		return
	}
	entry, err := s.repo.GetRequestLog(c.Request.Context(), day, id)
	if err != nil {
		handleRepoError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"log": entry})
}
