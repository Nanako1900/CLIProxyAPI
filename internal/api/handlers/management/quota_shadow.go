package management

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// GetQuotaShadowStatus returns bounded process-local telemetry, never raw log files.
func (h *Handler) GetQuotaShadowStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "quota_shadow_unavailable"})
		return
	}
	c.JSON(http.StatusOK, h.authManager.QuotaShadowSnapshot())
}
