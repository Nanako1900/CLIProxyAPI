package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/pool"
)

// accountPoolMiddleware fails closed for adapters that have not implemented the
// pool contract. Management and OAuth callbacks keep their existing auth policy.
func (s *Server) accountPoolMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		scoped := strings.HasPrefix(path, "/pools/")
		cfg := s.getConfig()
		if !s.accountPoolsEnabled && (cfg == nil || !cfg.AccountPools.Enabled) {
			if scoped {
				c.AbortWithStatus(http.StatusNotFound)
			}
			return
		}
		if poolControlPath(path) {
			return
		}
		reject := func(status int, code string) {
			c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"type": "invalid_request_error", "code": code, "message": code}})
		}
		if cfg == nil || s.accountPoolsEnabled != cfg.AccountPools.Enabled {
			reject(503, "pool_restart_required")
			return
		}
		if cfg.Home.Enabled {
			reject(503, "pool_protocol_unsupported")
			return
		}
		if s.accessManager == nil {
			reject(401, "authentication_required")
			return
		}
		identity, errAuth := s.accessManager.Authenticate(c.Request.Context(), c.Request)
		if errAuth != nil {
			reject(errAuth.HTTPStatusCode(), "authentication_failed")
			return
		}
		if identity == nil {
			reject(401, "authentication_required")
			return
		}
		poolID := c.Param("pool_id")
		if scoped {
			if !config.ValidPoolID(poolID) || strings.Contains(c.Request.URL.EscapedPath(), "%") {
				reject(400, "invalid_pool_id")
				return
			}
			path = strings.TrimPrefix(path, "/pools/"+poolID)
		} else {
			if cfg.AccountPools.Unscoped.Mode != "default" {
				reject(400, "pool_required")
				return
			}
			poolID = cfg.AccountPools.Unscoped.PoolID
		}
		if !cfg.PoolAccess(identity.Provider, identity.Principal, poolID) {
			reject(403, "pool_access_denied")
			return
		}
		definition, exists := cfg.AccountPool(poolID)
		if !exists {
			reject(404, "pool_not_found")
			return
		}
		if !definition.IsEnabled() {
			reject(503, "pool_disabled")
			return
		}
		if !poolInferencePath(c.Request.Method, path) {
			reject(501, "pool_adapter_unsupported")
			return
		}

		c.Request = c.Request.WithContext(pool.WithScope(c.Request.Context(), pool.Scope{ID: poolID, Principal: identity.Principal, Provider: identity.Provider}))
	}
}

func poolControlPath(path string) bool {
	if path == "/" || path == "/healthz" || path == "/management.html" || path == "/usage.html" {
		return true
	}
	for _, prefix := range []string{"/v8/management", "/v0/management"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	switch path {
	case "/anthropic/callback", "/codex/callback", "/antigravity/callback", "/callback", "/devin/callback":
		return true
	}
	return false
}

// Only adapters executing through the credential manager are enabled here.
func poolInferencePath(method, path string) bool {
	if method == http.MethodGet {
		return path == "/v1/models" || strings.HasPrefix(path, "/v1/models/") || path == "/v1beta/models" || strings.HasPrefix(path, "/v1beta/models/")
	}
	if method != http.MethodPost {
		return false
	}
	switch path {
	case "/v1/chat/completions", "/v1/completions", "/v1/responses", "/v1/responses/compact",
		"/v1/messages", "/v1/messages/count_tokens", "/backend-api/codex/responses", "/backend-api/codex/responses/compact",
		"/v1/images/generations", "/v1/images/edits", "/v1/audio/speech", "/v1/tts":
		return true
	}
	if strings.HasPrefix(path, "/v1beta/models/") {
		return strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent") || strings.HasSuffix(path, ":countTokens")
	}
	return false
}
