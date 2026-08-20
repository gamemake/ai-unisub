package server

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed web/admin.html
var adminHTML []byte

//go:embed web/admin.css
var adminCSS []byte

//go:embed web/admin.js
var adminJS []byte

func (s *Server) adminPage(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "text/html; charset=utf-8", adminHTML)
}

func (s *Server) adminStyles(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "text/css; charset=utf-8", adminCSS)
}

func (s *Server) adminScript(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "application/javascript; charset=utf-8", adminJS)
}
