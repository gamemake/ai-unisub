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
	filter := repository.RequestLogFilter{
		Provider: strings.TrimSpace(c.Query("provider")),
		Query:    strings.TrimSpace(c.Query("q")),
	}
	if raw := strings.TrimSpace(c.Query("account_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			apiError(c, http.StatusBadRequest, "invalid_request", "invalid account_id")
			return
		}
		filter.AccountID = &id
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
	if user := userFromContext(c); user.Role != model.RoleAdmin {
		ownedIDs, err := s.repo.ListAccountIDsByCreator(c.Request.Context(), user.ID)
		if err != nil {
			apiError(c, http.StatusInternalServerError, "internal_error", "could not list request logs")
			return
		}
		if filter.AccountID != nil {
			if !containsInt64(ownedIDs, *filter.AccountID) {
				limit := filter.Limit
				if limit <= 0 || limit > 200 {
					limit = 50
				}
				c.JSON(http.StatusOK, gin.H{"data": []repository.RequestLog{}, "total": 0, "limit": limit, "offset": filter.Offset})
				return
			}
		} else {
			if len(ownedIDs) == 0 {
				limit := filter.Limit
				if limit <= 0 || limit > 200 {
					limit = 50
				}
				c.JSON(http.StatusOK, gin.H{"data": []repository.RequestLog{}, "total": 0, "limit": limit, "offset": filter.Offset})
				return
			}
			filter.AccountIDs = ownedIDs
		}
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
	if user := userFromContext(c); user.Role != model.RoleAdmin {
		if !s.canViewRequestLog(c, user.ID, entry) {
			apiError(c, http.StatusNotFound, "not_found", "request log not found")
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"log": entry})
}

func (s *Server) canViewRequestLog(c *gin.Context, userID int64, entry repository.RequestLog) bool {
	if entry.AccountID == nil {
		return false
	}
	account, err := s.repo.GetAccount(c.Request.Context(), *entry.AccountID)
	if err != nil || account.CreatedByUserID == nil {
		return false
	}
	return *account.CreatedByUserID == userID
}

func containsInt64(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
