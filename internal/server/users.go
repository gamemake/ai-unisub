package server

import (
	"net/http"
	"strings"
	"unicode"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/gin-gonic/gin"
)

func (s *Server) currentUser(c *gin.Context) {
	user := userFromContext(c)
	c.JSON(http.StatusOK, gin.H{"user": user})
}

func (s *Server) listUsers(c *gin.Context) {
	if !requireRole(c, model.RoleAdmin) {
		return
	}
	users, err := s.repo.ListUsers(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "internal_error", "could not list users")
		return
	}
	if users == nil {
		users = []model.User{}
	}
	c.JSON(http.StatusOK, gin.H{"data": users})
}

func (s *Server) createUser(c *gin.Context) {
	if !requireRole(c, model.RoleAdmin) {
		return
	}
	var request struct {
		Username string         `json:"username"`
		Password string         `json:"password"`
		Role     model.UserRole `json:"role"`
	}
	if c.ShouldBindJSON(&request) != nil {
		apiError(c, http.StatusBadRequest, "invalid_request", "username and password are required")
		return
	}
	if err := validateUsername(request.Username); err != nil {
		apiError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(request.Password) < 12 {
		apiError(c, http.StatusBadRequest, "invalid_request", "password must contain at least 12 characters")
		return
	}
	if request.Role == "" {
		request.Role = model.RoleUser
	}
	if !request.Role.Valid() {
		apiError(c, http.StatusBadRequest, "invalid_request", "role must be admin or user")
		return
	}
	user, err := s.repo.CreateUser(c.Request.Context(), request.Username, request.Password, request.Role)
	if err != nil {
		handleRepoError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"user": user})
}

func (s *Server) updateUser(c *gin.Context) {
	if !requireRole(c, model.RoleAdmin) {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var request struct {
		Role    model.UserRole `json:"role"`
		Enabled bool           `json:"enabled"`
	}
	if c.ShouldBindJSON(&request) != nil || !request.Role.Valid() {
		apiError(c, http.StatusBadRequest, "invalid_request", "role must be admin or user")
		return
	}
	user, err := s.repo.UpdateUser(c.Request.Context(), id, request.Role, request.Enabled)
	if err != nil {
		handleRepoError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": user})
}

func (s *Server) deleteUser(c *gin.Context) {
	if !requireRole(c, model.RoleAdmin) {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	if userFromContext(c).ID == id {
		apiError(c, http.StatusBadRequest, "invalid_request", "cannot delete the signed-in user")
		return
	}
	if err := s.repo.DeleteUser(c.Request.Context(), id); err != nil {
		handleRepoError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func userFromContext(c *gin.Context) model.User {
	value, _ := c.Get("current_user")
	user, _ := value.(model.User)
	return user
}

func requireRole(c *gin.Context, role model.UserRole) bool {
	if userFromContext(c).Role != role {
		apiError(c, http.StatusForbidden, "forbidden", "admin role is required")
		return false
	}
	return true
}

func validateUsername(username string) error {
	username = strings.TrimSpace(username)
	if len(username) < 2 || len(username) > 32 {
		return errUsernameLength
	}
	for _, character := range username {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '_' || character == '-' || character == '.' {
			continue
		}
		return errUsernameCharset
	}
	return nil
}

var (
	errUsernameLength  = errString("username must be 2 to 32 characters")
	errUsernameCharset = errString("username may only contain letters, digits, '.', '_' and '-'")
)

type errString string

func (e errString) Error() string { return string(e) }
