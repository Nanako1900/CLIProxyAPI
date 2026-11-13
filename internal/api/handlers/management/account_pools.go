package management

import (
	"crypto/sha256"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/clienterror"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func credentialPoolETag(a *coreauth.Auth) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s:%d:%d:%s", a.ID, a.RegistrationEpoch, a.Generation, a.AccountPoolID()))
	return fmt.Sprintf("\"%x\"", sum)
}

func writeCredentialPool(c *gin.Context, a *coreauth.Auth) {
	c.Header("ETag", credentialPoolETag(a))
	c.JSON(http.StatusOK, gin.H{"id": a.ID, "pool_id": a.AccountPoolID()})
}

// GetCredentialPool returns a secret-free membership view with a concurrency token.
func (h *Handler) GetCredentialPool(c *gin.Context) {
	if h.authManager == nil {
		c.JSON(503, gin.H{"error": "pool_state_unavailable"})
		return
	}
	a, ok := h.authManager.GetByID(c.Query("id"))
	if !ok || a == nil {
		c.JSON(404, gin.H{"error": "credential_not_found"})
		return
	}
	writeCredentialPool(c, a)
}

// PatchCredentialPool uses the credential mutation gate and durable manager update.
// Busy credentials must finish their current upstream operation before being moved.
func (h *Handler) PatchCredentialPool(c *gin.Context) {
	if h.authManager == nil {
		c.JSON(503, gin.H{"error": "pool_state_unavailable"})
		return
	}
	expected := c.GetHeader("If-Match")
	if expected == "" {
		c.JSON(428, gin.H{"error": "precondition_required"})
		return
	}
	var req struct {
		ID     string  `json:"id"`
		PoolID *string `json:"pool_id"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == "" || req.PoolID == nil {
		c.JSON(400, gin.H{"error": "invalid_pool_assignment"})
		return
	}
	if *req.PoolID != "" {
		cfg := h.configSnapshot()
		if cfg == nil {
			c.JSON(503, gin.H{"error": "pool_state_unavailable"})
			return
		}
		p, exists := cfg.AccountPool(*req.PoolID)
		if !exists {
			c.JSON(404, gin.H{"error": "pool_not_found"})
			return
		}
		if !p.IsEnabled() {
			c.JSON(409, gin.H{"error": "pool_disabled"})
			return
		}
	}
	status := http.StatusInternalServerError
	code := "pool_assignment_failed"
	a, changed, err := h.authManager.Modify(c.Request.Context(), req.ID, 0, func(a *coreauth.Auth) error {
		if expected != credentialPoolETag(a) {
			status, code = 412, "precondition_failed"
			return fmt.Errorf("%s", code)
		}
		if coreauth.IsConfigAPIKeyAuth(a) || coreauth.IsPluginVirtualAuth(a) {
			status, code = 409, "membership_source_managed"
			return fmt.Errorf("%s", code)
		}
		a.PoolID = *req.PoolID
		if a.Metadata == nil {
			a.Metadata = make(map[string]any)
		}
		a.Metadata["pool_id"] = *req.PoolID
		return nil
	})
	if err != nil {
		status = clienterror.HTTPStatusFromErrorOr(err, status)
		if status == 409 && code == "pool_assignment_failed" {
			code = "credential_busy"
		}
		c.JSON(status, gin.H{"error": code})
		return
	}
	if !changed || a == nil {
		c.JSON(404, gin.H{"error": "credential_not_found"})
		return
	}
	writeCredentialPool(c, a)
}
