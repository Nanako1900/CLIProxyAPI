package management

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed usage_dashboard.html
var usageDashboard []byte

// UsageDashboard serves an empty shell; all account data requires management authentication.
func (h *Handler) UsageDashboard(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
	c.Data(http.StatusOK, "text/html; charset=utf-8", usageDashboard)
}
