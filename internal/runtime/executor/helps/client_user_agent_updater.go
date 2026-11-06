package helps

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	log "github.com/sirupsen/logrus"
)

const clientUserAgentRefreshInterval = 3 * time.Hour

type clientUserAgentSettings struct {
	Codex    bool
	Claude   bool
	ProxyURL string
}

func userAgentSettings(cfg *config.Config) clientUserAgentSettings {
	var settings clientUserAgentSettings
	if cfg == nil || cfg.Home.Enabled {
		return settings
	}
	settings.ProxyURL = cfg.ProxyURL
	settings.Codex = strings.EqualFold(strings.TrimSpace(cfg.CodexHeaderDefaults.UserAgent), "auto")
	settings.Claude = strings.EqualFold(strings.TrimSpace(cfg.ClaudeHeaderDefaults.UserAgent), "auto")
	hasAutoHeader := func(headers map[string]string) bool {
		for name, value := range headers {
			if strings.EqualFold(name, "User-Agent") && strings.EqualFold(strings.TrimSpace(value), "auto") {
				return true
			}
		}
		return false
	}
	for _, key := range cfg.CodexKey {
		settings.Codex = settings.Codex || hasAutoHeader(key.Headers)
	}
	for _, key := range cfg.ClaudeKey {
		settings.Claude = settings.Claude || hasAutoHeader(key.Headers)
	}
	return settings
}

// RunClientUserAgentUpdater follows opt-in auto settings for one service lifetime.
// Configuration changes trigger a lookup; unrelated saves do not repeat lookups.
func RunClientUserAgentUpdater(ctx context.Context, getConfig func() *config.Config, updates <-chan struct{}) {
	ticker := time.NewTicker(clientUserAgentRefreshInterval)
	defer ticker.Stop()
	runClientUserAgentUpdater(ctx, getConfig, updates, ticker.C, func(ctx context.Context, settings clientUserAgentSettings) {
		client := NewProxyAwareHTTPClient(ctx, &config.Config{SDKConfig: config.SDKConfig{ProxyURL: settings.ProxyURL}}, nil, 0)
		refreshClientUserAgents(ctx, settings, client, &nativeClientVersions)
	})
}

func runClientUserAgentUpdater(ctx context.Context, getConfig func() *config.Config, updates <-chan struct{}, ticks <-chan time.Time, lookup func(context.Context, clientUserAgentSettings)) {
	var previous clientUserAgentSettings
	var cancel context.CancelFunc
	var completed <-chan struct{}
	defer func() {
		if cancel != nil {
			cancel()
		}
	}()
	refresh := func(force bool) {
		settings := userAgentSettings(getConfig())
		if settings == previous && (!force || completed != nil) {
			return
		}
		// A settings change cancels an obsolete lookup, even if npm is stalled.
		if cancel != nil {
			cancel()
			cancel = nil
		}
		completed = nil
		previous = settings
		if !settings.Codex && !settings.Claude {
			return
		}
		lookupCtx, cancelLookup := context.WithCancel(ctx)
		cancel = cancelLookup
		done := make(chan struct{})
		completed = done
		go func() {
			defer close(done)
			defer cancelLookup()
			lookup(lookupCtx, settings)
		}()
	}
	refresh(true)
	for {
		select {
		case <-ctx.Done():
			return
		case <-updates:
			refresh(false)
		case <-completed:
			completed = nil
			cancel = nil
		case <-ticks:
			refresh(true)
		}
	}
}

func refreshClientUserAgents(ctx context.Context, settings clientUserAgentSettings, client *http.Client, cache *clientVersionCache) {
	var pending sync.WaitGroup
	for provider, enabled := range map[string]bool{"codex": settings.Codex, "claude": settings.Claude} {
		if !enabled {
			continue
		}
		pending.Go(func() {
			version, errFetch := fetchClientUserAgentVersion(ctx, client, provider)
			if errFetch != nil {
				if ctx.Err() == nil {
					log.WithField("provider", provider).WithError(errFetch).Warn("UA version lookup failed; keeping cached identity")
				}
				return
			}
			if cache.advance(provider, version) {
				log.WithFields(log.Fields{"provider": provider, "version": version}).Info("updated automatic client User-Agent version")
			}
		})
	}
	pending.Wait()
}

func fetchClientUserAgentVersion(ctx context.Context, client *http.Client, provider string) (string, error) {
	packageName := "@openai/codex"
	if provider == "claude" {
		packageName = "@anthropic-ai/claude-code"
	}
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, "https://registry.npmjs.org/"+packageName+"/latest", nil)
	if errRequest != nil {
		return "", errRequest
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "CLIProxyAPI-UA-Updater")
	response, errFetch := client.Do(req)
	if errFetch != nil {
		return "", fmt.Errorf("fetch official client version: %w", errFetch)
	}
	defer func() {
		if errClose := response.Body.Close(); errClose != nil {
			log.WithError(errClose).Warn("close client version response")
		}
	}()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("official registry returned HTTP %d", response.StatusCode)
	}
	const limit = 1 << 20
	body, errRead := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if errRead != nil {
		return "", fmt.Errorf("read official client version: %w", errRead)
	}
	if len(body) > limit || !ojson.ValidBytes(body) {
		return "", fmt.Errorf("invalid official registry response")
	}
	value := ojson.GetBytes(body, "version")
	version := value.String()
	if _, ok := parseClientVersion(version); !ok || value.Type != ojson.String {
		return "", fmt.Errorf("official registry did not return a stable client version")
	}
	return version, nil
}
