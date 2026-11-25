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
		Provider  string            `json:"provider"`
		AuthIndex string            `json:"authIndex"`
		Name      string            `json:"name"`
		Source    string            `json:"source"`
		UserAgent string            `json:"userAgent"`
		Headers   map[string]string `json:"headers,omitempty"`
		Disabled  bool              `json:"disabled"`
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
			overrides = append(overrides, override{
				Provider:  provider,
				AuthIndex: auth.Index,
				Name:      label,
				Source:    source,
				UserAgent: value,
				Headers:   credentialIdentityOverrideHeaders(provider, auth, value),
				Disabled:  auth.Disabled,
			})
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

// credentialIdentityOverrideHeaders returns only non-secret, provider-whitelisted
// identity headers configured on auth so the management UI can inspect and edit
// per-account UA overrides without ever exposing Authorization, Cookie, or keys.
func credentialIdentityOverrideHeaders(provider string, auth *coreauth.Auth, resolvedUA string) map[string]string {
	if auth == nil || len(auth.Attributes) == 0 {
		return nil
	}
	var allowed map[string]string
	switch provider {
	case "claude":
		allowed = map[string]string{
			"user-agent":                  "User-Agent",
			"x-stainless-package-version": "X-Stainless-Package-Version",
			"x-stainless-runtime-version": "X-Stainless-Runtime-Version",
			"x-stainless-os":              "X-Stainless-OS",
			"x-stainless-arch":            "X-Stainless-Arch",
			"x-stainless-timeout":         "X-Stainless-Timeout",
		}
	case "codex":
		allowed = map[string]string{
			"user-agent":            "User-Agent",
			"originator":            "Originator",
			"x-codex-beta-features": "X-Codex-Beta-Features",
			"accept-encoding":       "Accept-Encoding",
		}
	default:
		return nil
	}
	out := make(map[string]string)
	for key, raw := range auth.Attributes {
		prefix, name, ok := strings.Cut(key, ":")
		if !ok || !strings.EqualFold(prefix, "header") {
			continue
		}
		canonical, okAllowed := allowed[strings.ToLower(strings.TrimSpace(name))]
		if !okAllowed {
			continue
		}
		val := strings.TrimSpace(raw)
		if val == "" {
			continue
		}
		if canonical == "User-Agent" && resolvedUA != "" {
			out[canonical] = resolvedUA
			continue
		}
		out[canonical] = val
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
