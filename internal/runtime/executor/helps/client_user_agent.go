package helps

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// DefaultCodexUserAgent is the pinned identity used without an operator override.
const DefaultCodexUserAgent = "codex-tui/0.154.0 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.154.0)"

var clientUserAgentPattern = regexp.MustCompile(`^(claude-cli|codex-tui|codex_cli_rs|codex_exec|codex_vscode)/(\d+\.\d+\.\d+)( .*)?$`)
var codexUserAgentSuffixPattern = regexp.MustCompile(`\((codex-tui|codex_cli_rs|codex_exec|codex_vscode);\s*\d+\.\d+\.\d+\)`)

type clientVersionCache struct {
	mu       sync.RWMutex
	versions map[string]string
}

var nativeClientVersions = clientVersionCache{versions: map[string]string{
	"codex":  "0.154.0",
	"claude": "2.1.280",
}}

func (cache *clientVersionCache) get(provider string) string {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.versions[provider]
}

func (cache *clientVersionCache) advance(provider, version string) bool {
	if _, ok := parseClientVersion(version); !ok {
		return false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if !newerClientVersion(version, cache.versions[provider]) {
		return false
	}
	cache.versions[provider] = version
	return true
}

func parseClientVersion(version string) ([3]uint64, bool) {
	var result [3]uint64
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return result, false
	}
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return result, false
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return result, false
			}
		}
		number, errParse := strconv.ParseUint(part, 10, 32)
		if errParse != nil {
			return result, false
		}
		result[i] = number
	}
	return result, true
}

func newerClientVersion(candidate, previous string) bool {
	next, ok := parseClientVersion(candidate)
	if !ok {
		return false
	}
	current, ok := parseClientVersion(previous)
	if !ok {
		return true
	}
	for i := range next {
		if next[i] != current[i] {
			return next[i] > current[i]
		}
	}
	return false
}

func defaultClientUserAgent(provider string) string {
	if provider == "codex" {
		return DefaultCodexUserAgent
	}
	return defaultClaudeFingerprintUserAgent
}

func replaceClientUserAgentVersion(provider, baseline, version string) string {
	match := clientUserAgentPattern.FindStringSubmatch(baseline)
	if len(match) != 4 || (provider == "claude") != (match[1] == "claude-cli") {
		baseline = defaultClientUserAgent(provider)
		match = clientUserAgentPattern.FindStringSubmatch(baseline)
	}
	if !newerClientVersion(version, match[2]) {
		return baseline
	}
	updated := match[1] + "/" + version + match[3]
	if provider == "codex" {
		updated = codexUserAgentSuffixPattern.ReplaceAllString(updated, "(${1}; "+version+")")
	}
	return updated
}

// ResolveConfiguredUserAgent resolves auto from memory; it never performs I/O.
func ResolveConfiguredUserAgent(provider, value, baseline string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "auto") {
		return replaceClientUserAgentVersion(provider, baseline, nativeClientVersions.get(provider))
	}
	return value
}

// carryClaudeRequestFields keeps the comment fields of the outgoing Claude Code
// UA (for example workload/cron or agent-sdk/<version>) that the configured
// identity does not name. A configured identity sets product, version, and
// entrypoint; per-request fields still come from the client. Values that are
// not Claude Code UAs are returned unchanged.
func carryClaudeRequestFields(configured, current string) string {
	if _, ok := parseClaudeCodeUserAgent(configured); !ok {
		return configured
	}
	if _, ok := parseClaudeCodeUserAgent(current); !ok {
		return configured
	}
	prefix, configuredComment, _ := strings.Cut(strings.TrimSpace(configured), "(")
	_, currentComment, _ := strings.Cut(strings.TrimSpace(current), "(")
	fields := claudeUserAgentCommentFields(configuredComment)
	named := make(map[string]struct{}, len(fields))
	for _, field := range fields[2:] {
		name, _, _ := strings.Cut(field, "/")
		named[strings.ToLower(name)] = struct{}{}
	}
	carried := false
	for _, field := range claudeUserAgentCommentFields(currentComment)[2:] {
		name, _, _ := strings.Cut(field, "/")
		if _, exists := named[strings.ToLower(name)]; exists {
			continue
		}
		fields = append(fields, field)
		carried = true
	}
	if !carried {
		return configured
	}
	merged := prefix + "(" + strings.Join(fields, ", ") + ")"
	if !config.ValidUserAgent(merged) {
		return configured
	}
	return merged
}

// claudeUserAgentCommentFields splits a parsed Claude Code UA comment, without
// its opening parenthesis, into trimmed fields.
func claudeUserAgentCommentFields(comment string) []string {
	fields := strings.Split(strings.TrimSuffix(comment, ")"), ",")
	for index, field := range fields {
		fields[index] = strings.TrimSpace(field)
	}
	return fields
}

// ApplyConfiguredUserAgent is the final UA boundary after native/model headers.
// Credential overrides take precedence over provider settings. Only UA is changed.
func ApplyConfiguredUserAgent(ctx context.Context, headers http.Header, cfg *config.Config, auth *cliproxyauth.Auth, provider string, clientHeaders http.Header) {
	if headers == nil || (provider != "codex" && provider != "claude") {
		return
	}
	value := ""
	if cfg != nil {
		if provider == "claude" {
			value = cfg.ClaudeHeaderDefaults.UserAgent
		} else if auth != nil && auth.AuthKind() != cliproxyauth.AuthKindAPIKey && strings.TrimSpace(auth.Attributes["api_key"]) == "" {
			value = cfg.CodexHeaderDefaults.UserAgent
		}
	}
	if auth != nil {
		// Reuse header-variable resolution while excluding all other header mutations.
		attrs := make(map[string]string)
		for name, raw := range auth.Attributes {
			if strings.EqualFold(name, "header:User-Agent") {
				attrs[name] = raw
			}
		}
		request := &http.Request{Header: make(http.Header)}
		if ctx != nil {
			request = request.WithContext(ctx)
		}
		util.ApplyCustomHeadersFromAttrs(request, attrs, clientHeaders)
		if override := request.Header.Get("User-Agent"); strings.TrimSpace(override) != "" && config.ValidUserAgent(override) {
			value = override
		}
	}
	if !config.ValidUserAgent(value) || strings.TrimSpace(value) == "" {
		return
	}
	current := HeaderValueCaseInsensitive(headers, "User-Agent")
	value = ResolveConfiguredUserAgent(provider, value, current)
	if provider == "claude" {
		value = carryClaudeRequestFields(value, current)
	}
	for name := range headers {
		if strings.EqualFold(name, "User-Agent") {
			delete(headers, name)
		}
	}
	headers.Set("User-Agent", value)
}
