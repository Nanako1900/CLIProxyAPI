package management

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/clientua"
	log "github.com/sirupsen/logrus"
)

func (h *Handler) SetClientUserAgents(catalog *clientua.Store) { h.clientUserAgents = catalog }

// ClientUserAgents exposes only whitelisted identities through authenticated v8
// routes. Applying a choice uses the existing config editor and save transaction.
func (h *Handler) ClientUserAgents(c *gin.Context) {
	if h.clientUserAgents == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "client UA catalog is unavailable"})
		return
	}
	provider := strings.TrimSpace(c.Query("provider"))
	if !clientua.ValidProvider(provider) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider must be claude or codex"})
		return
	}
	c.Header("Cache-Control", "no-store")
	switch c.Request.Method {
	case http.MethodGet:
		c.JSON(http.StatusOK, gin.H{"items": h.clientUserAgents.List(provider), "limit": clientua.LimitPerProvider})
	case http.MethodPost:
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2*clientua.MaxImportBytes)
		var input struct {
			Headers string `json:"headers"`
		}
		if errBind := c.ShouldBindJSON(&input); errBind != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expected a request-header block of at most 32 KiB"})
			return
		}
		profile, errProfile := clientua.Parse(input.Headers)
		if errProfile != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": errProfile.Error()})
			return
		}
		if profile.Provider != provider {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "User-Agent does not match the selected provider"})
			return
		}
		entry, errImport := h.clientUserAgents.Import(profile)
		if errImport != nil {
			log.WithError(errImport).Warn("save imported client UA")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not save client UA catalog"})
			return
		}
		c.JSON(http.StatusOK, entry)
	case http.MethodDelete:
		if errDelete := h.clientUserAgents.Delete(provider, c.Param("id")); errDelete != nil {
			log.WithError(errDelete).Warn("save client UA catalog after deletion")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not save client UA catalog"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}
