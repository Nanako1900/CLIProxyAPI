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
			provider, value := credentialUserAgentOverride(auth)
			if value == "" {
				continue
			}
			source := "credential"
			if coreauth.IsConfigAPIKeyAuth(auth) {
				source = "config"
			}
			label := auth.Label
			// Gemini credential labels can include an account email, project ID, or file path.
			if label == "" || (provider == "gemini" && source == "credential") {
				label = auth.Index
			}
			overrides = append(overrides, override{provider, auth.Index, label, source, value, auth.Disabled})
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

func credentialUserAgentOverride(auth *coreauth.Auth) (provider, value string) {
	if auth == nil {
		return "", ""
	}
	switch auth.Provider {
	case "gemini", "gemini-interactions", "gemini-cli", "vertex":
		return "gemini", helps.GeminiCredentialUserAgent(auth)
	case "claude":
		if auth.AuthKind() == coreauth.AuthKindAPIKey {
			return "claude", helps.ConfiguredCredentialUserAgent(auth)
		}
		for key, raw := range auth.Attributes {
			if strings.EqualFold(key, "header:User-Agent") && strings.TrimSpace(raw) != "" {
				return auth.Provider, raw
			}
		}
	case "codex":
		return "codex", helps.CodexCredentialUserAgent(auth)
	}
	return "", ""
}
