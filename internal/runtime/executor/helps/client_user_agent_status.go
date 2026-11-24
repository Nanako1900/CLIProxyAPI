package helps

import (
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

// ClientUserAgentStatus describes provider defaults, never an actual request or credential.
type ClientUserAgentStatus struct {
	Provider            string                    `json:"provider"`
	Mode                string                    `json:"mode"`
	ConfiguredUserAgent string                    `json:"configuredUserAgent"`
	DefaultUserAgent    string                    `json:"defaultUserAgent"`
	ResolvedUserAgent   string                    `json:"resolvedUserAgent"`
	BaselineHeaders     map[string]string         `json:"baselineHeaders"`
	BillingVersion      string                    `json:"billingVersion,omitempty"`
	Auto                ClientUserAgentAutoStatus `json:"auto"`
}

type ClientUserAgentAutoStatus struct {
	Enabled                bool       `json:"enabled"`
	CachedVersion          string     `json:"cachedVersion"`
	RegistryURL            string     `json:"registryURL"`
	RefreshIntervalSeconds int64      `json:"refreshIntervalSeconds"`
	Result                 string     `json:"result"`
	LastAttempt            *time.Time `json:"lastAttempt,omitempty"`
	LastSuccess            *time.Time `json:"lastSuccess,omitempty"`
}

func (cache *clientVersionCache) recordLookup(provider, result string, attempt, now time.Time) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.lookups == nil {
		cache.lookups = make(map[string]clientUserAgentLookup)
	}
	state := cache.lookups[provider]
	now = now.UTC()
	attempt = attempt.UTC()
	if result == "checking" {
		state.LastAttempt = &attempt
	} else if state.LastAttempt == nil || !state.LastAttempt.Equal(attempt) {
		// A cancelled lookup must not overwrite a newer configuration's status.
		return
	}
	if result == "ok" {
		state.LastSuccess = &now
	}
	state.Result = result
	cache.lookups[provider] = state
}

// ClientUserAgentStatuses reads the same cache and baseline used by executors without I/O.
func ClientUserAgentStatuses(cfg *config.Config) []ClientUserAgentStatus {
	settings := userAgentSettings(cfg)
	statuses := make([]ClientUserAgentStatus, 0, 3)
	for _, provider := range []string{"claude", "codex", "gemini"} {
		status := ClientUserAgentStatus{
			Provider: provider, Mode: "default",
			BaselineHeaders: map[string]string{},
		}
		status.Auto.RegistryURL = clientUserAgentRegistryURL(provider)
		switch provider {
		case "claude":
			status.DefaultUserAgent = defaultClientUserAgent(provider)
			profile := defaultClaudeDeviceProfile(cfg)
			status.BaselineHeaders = map[string]string{
				"User-Agent":                  profile.UserAgent,
				"X-Stainless-Package-Version": profile.PackageVersion,
				"X-Stainless-Runtime-Version": profile.RuntimeVersion,
				"X-Stainless-OS":              profile.OS,
				"X-Stainless-Arch":            profile.Arch,
				"X-Stainless-Timeout":         "600",
			}
			status.BillingVersion = DefaultClaudeVersion(cfg)
			status.Auto.Enabled = settings.Claude
			if cfg != nil {
				status.ConfiguredUserAgent = strings.TrimSpace(cfg.ClaudeHeaderDefaults.UserAgent)
				if value := cfg.ClaudeHeaderDefaults.Timeout; value != "" {
					status.BaselineHeaders["X-Stainless-Timeout"] = value
				}
			}
		case "codex":
			status.Mode = "unconfigured"
			status.Auto.Enabled = false
			status.Auto.RegistryURL = ""
			if cfg != nil {
				hd := cfg.CodexHeaderDefaults
				ua := strings.TrimSpace(hd.UserAgent)
				if ua != "" && !strings.EqualFold(ua, "auto") {
					status.Mode = "ualab"
					status.ConfiguredUserAgent = ua
					status.ResolvedUserAgent = ua
					status.BaselineHeaders["User-Agent"] = ua
				}
				if originator := strings.TrimSpace(hd.Originator); originator != "" {
					status.BaselineHeaders["Originator"] = originator
				}
				if beta := strings.TrimSpace(hd.BetaFeatures); beta != "" {
					status.BaselineHeaders["X-Codex-Beta-Features"] = beta
				}
				if ae := strings.TrimSpace(hd.AcceptEncoding); ae != "" {
					status.BaselineHeaders["Accept-Encoding"] = ae
				}
			}
			status.Auto.Result = "never"
			status.Auto.RefreshIntervalSeconds = 0
			statuses = append(statuses, status)
			continue
		case "gemini":
			status.Mode = "unconfigured"
			status.Auto.Enabled = false
			status.Auto.RegistryURL = ""
			if cfg != nil {
				hd := cfg.GeminiHeaderDefaults
				ua := strings.TrimSpace(hd.UserAgent)
				if ua != "" && !strings.EqualFold(ua, "auto") {
					status.Mode = "ualab"
					status.ConfiguredUserAgent = ua
					status.ResolvedUserAgent = ua
					status.BaselineHeaders["User-Agent"] = ua
				}
				if apiClient := strings.TrimSpace(hd.APIClient); apiClient != "" && !strings.EqualFold(apiClient, "auto") {
					status.BaselineHeaders["X-Goog-Api-Client"] = apiClient
				}
				if ae := strings.TrimSpace(hd.AcceptEncoding); ae != "" {
					status.BaselineHeaders["Accept-Encoding"] = ae
				}
			}
			status.Auto.Result = "never"
			status.Auto.RefreshIntervalSeconds = 0
			statuses = append(statuses, status)
			continue
		}
		if status.ConfiguredUserAgent != "" {
			status.Mode = "fixed"
			if strings.EqualFold(status.ConfiguredUserAgent, "auto") {
				status.Mode = "auto"
			}
		}
		status.ResolvedUserAgent = status.BaselineHeaders["User-Agent"]
		nativeClientVersions.mu.RLock()
		version := nativeClientVersions.versions[provider]
		lookup := nativeClientVersions.lookups[provider]
		nativeClientVersions.mu.RUnlock()
		if status.Mode == "auto" {
			status.ResolvedUserAgent = replaceClientUserAgentVersion(provider, status.ResolvedUserAgent, version)
		} else if status.Mode == "fixed" {
			status.ResolvedUserAgent = status.ConfiguredUserAgent
		}
		status.Auto.CachedVersion = version
		status.Auto.Result = lookup.Result
		if status.Auto.Result == "" {
			status.Auto.Result = "never"
		}
		status.Auto.LastAttempt = lookup.LastAttempt
		status.Auto.LastSuccess = lookup.LastSuccess
		status.Auto.RefreshIntervalSeconds = int64(clientUserAgentRefreshInterval / time.Second)
		statuses = append(statuses, status)
	}
	return statuses
}
