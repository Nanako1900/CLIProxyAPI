package api

import (
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/clientua"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	log "github.com/sirupsen/logrus"
)

func (s *Server) initClientUserAgents(cfg *config.Config) {
	if cfg == nil || cfg.Home.Enabled {
		return
	}
	path := ""
	if authDir := strings.TrimSpace(cfg.AuthDir); authDir != "" {
		path = filepath.Join(authDir, ".client-user-agents.yaml")
	}
	catalog, errCatalog := clientua.NewStore(path)
	if errCatalog != nil {
		log.WithError(errCatalog).Warn("load client UA catalog; starting with an empty catalog")
	}
	s.clientUserAgents = catalog
	s.mgmt.SetClientUserAgents(catalog)
}

// captureClientUserAgent runs after client authentication and before executors
// can normalize headers. It never reads a request body or upstream credentials.
func (s *Server) captureClientUserAgent(c *gin.Context) {
	if c.IsAborted() || s.clientUserAgents == nil {
		return
	}
	if cfg := s.getConfig(); cfg == nil || cfg.Home.Enabled {
		return
	}
	s.clientUserAgents.Observe(c.Request.Header)
}
