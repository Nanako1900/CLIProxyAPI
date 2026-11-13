package management

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/clientua"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// ClientUserAgentStatus exposes safe runtime metadata, not credential bodies or outbound traffic.
func (h *Handler) ClientUserAgentStatus(c *gin.Context) {
	cfg := h.configSnapshot()
	if cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration is unavailable"})
		return
	}
	type override struct {
		Provider  string `json:"provider"`
		AuthIndex string `json:"authIndex"`
		Name      string `json:"name"`
		Source    string `json:"source"`
		UserAgent string `json:"userAgent"`
		Disabled  bool   `json:"disabled"`
	}
	overrides := make([]override, 0)
	if h.authManager != nil {
		for _, auth := range h.authManager.List() {
			if auth == nil || !clientua.ValidProvider(auth.Provider) {
				continue
			}
			for name, value := range auth.Attributes {
				if !strings.EqualFold(name, "header:User-Agent") || strings.TrimSpace(value) == "" {
					continue
				}
				source := "credential"
				if coreauth.IsConfigAPIKeyAuth(auth) {
					source = "config"
				}
				label := auth.Label
				if label == "" {
					label = auth.Index
				}
				overrides = append(overrides, override{auth.Provider, auth.Index, label, source, value, auth.Disabled})
			}
		}
	}
	sort.Slice(overrides, func(i, j int) bool {
		if overrides[i].Provider != overrides[j].Provider {
			return overrides[i].Provider < overrides[j].Provider
		}
		return overrides[i].AuthIndex < overrides[j].AuthIndex
	})
	persistence := "unavailable"
	if h.clientUserAgents != nil && !cfg.Home.Enabled {
		persistence = "memory"
		if h.clientUserAgents.Persistent() {
			persistence = "disk"
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"providers":                  helps.ClientUserAgentStatuses(cfg),
		"overrides":                  overrides,
		"overrideInventoryAvailable": h.authManager != nil,
		"homeMode":                   cfg.Home.Enabled,
		"catalogAvailable":           h.clientUserAgents != nil && !cfg.Home.Enabled,
		"catalogPersistence":         persistence,
		"catalogLimit":               clientua.LimitPerProvider,
	})
}
