package management

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"gopkg.in/yaml.v3"
)

// Called with the config and pool mutation locks held. Deletion never moves or
// deletes credentials: operators must first disable the pool and empty it.
func (h *Handler) validateAccountPoolRemoval(c *gin.Context, before *yaml.Node, next *config.Config) bool {
	var previous config.AccountPoolsConfig
	if node := configV8Node(before, []string{"routing", "account-pools"}); node != nil {
		if errDecode := node.Decode(&previous); errDecode != nil {
			c.JSON(http.StatusConflict, gin.H{"error": "pool_state_unavailable"})
			return false
		}
	}
	removed := make(map[string]bool)
	for _, p := range previous.Pools {
		if _, exists := next.AccountPool(p.ID); !exists {
			version := strings.TrimSpace(strings.Join(c.Request.Header.Values("If-Match"), ","))
			if version == "" || version == "*" {
				c.JSON(http.StatusPreconditionRequired, gin.H{"error": "precondition_required"})
				return false
			}
			if p.IsEnabled() {
				c.JSON(http.StatusConflict, gin.H{"error": "pool_must_be_disabled", "pool_id": p.ID})
				return false
			}
			removed[p.ID] = true
		}
	}
	if len(removed) == 0 {
		return true
	}
	if h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "pool_state_unavailable"})
		return false
	}
	for _, a := range h.authManager.List() {
		if a != nil && removed[a.AccountPoolID()] {
			c.JSON(http.StatusConflict, gin.H{"error": "pool_not_empty", "pool_id": a.AccountPoolID()})
			return false
		}
	}
	return true
}
