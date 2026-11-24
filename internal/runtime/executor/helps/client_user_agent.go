package helps

import (
	"context"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moego/regexp"
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
	lookups  map[string]clientUserAgentLookup
}

type clientUserAgentLookup struct {
	LastAttempt *time.Time
	LastSuccess *time.Time
	Result      string
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

// IsUserAgentAttribute identifies a credential header that would set User-Agent.
func IsUserAgentAttribute(key string) bool {
	prefix, name, ok := strings.Cut(key, ":")
	return ok && strings.EqualFold(prefix, "header") && strings.EqualFold(strings.TrimSpace(name), "User-Agent")
}

// credentialAttrsWithoutUserAgent reserves the native UA for the final resolver.
func credentialAttrsWithoutUserAgent(attrs map[string]string) map[string]string {
	for key := range attrs {
		if IsUserAgentAttribute(key) {
			filtered := maps.Clone(attrs)
			maps.DeleteFunc(filtered, func(key, _ string) bool { return IsUserAgentAttribute(key) })
			return filtered
		}
	}
	return attrs
}

// ConfiguredCredentialUserAgent selects the raw credential User-Agent before
// resolving request-specific header variables.
func ConfiguredCredentialUserAgent(auth *cliproxyauth.Auth) string {
	if auth == nil {
		return ""
	}
	return userAgentHeaderOverride(auth.Attributes, "header:User-Agent", IsUserAgentAttribute)
}

// Existing credentials can contain multiple spellings of one header. Prefer the
// canonical key, followed by the lexicographically first valid alternative.
func userAgentHeaderOverride[T any](headers map[string]T, canonical string, matches func(string) bool) string {
	var bestKey, bestValue string
	for key, raw := range headers {
		if !matches(key) {
			continue
		}
		value, ok := any(raw).(string)
		if !ok || !config.ValidUserAgent(value) {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if key == canonical {
			return value
		}
		if bestKey == "" || key < bestKey {
			bestKey, bestValue = key, value
		}
	}
	return bestValue
}

// credentialUserAgentAttribute returns the configured UA before header variables are resolved.
func credentialUserAgentAttribute(auth *cliproxyauth.Auth) string {
	if auth != nil {
		for key, raw := range auth.Attributes {
			if IsUserAgentAttribute(key) && config.ValidUserAgent(raw) {
				return strings.TrimSpace(raw)
			}
		}
	}
	return ""
}

// resolveCredentialUserAgent shares the downstream header and session-variable
// resolution used by other credential headers, validating the expanded value.
func resolveCredentialUserAgent(ctx context.Context, raw string, clientHeaders http.Header) string {
	if raw == "" || !strings.Contains(raw, "$") {
		return raw
	}
	req := &http.Request{Header: make(http.Header)}
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	util.ApplyCustomHeadersFromAttrs(req, map[string]string{"header:User-Agent": raw}, clientHeaders)
	value := req.Header.Get("User-Agent")
	if !config.ValidUserAgent(value) {
		return ""
	}
	return strings.TrimSpace(value)
}

// ApplyClaudeAPIKeyHeaders preserves the native UA until a validated setting
// replaces it. Explicit mappings supply the complete UA, including its fields.
func ApplyClaudeAPIKeyHeaders(r *http.Request, cfg *config.Config, auth *cliproxyauth.Auth, clientHeaders http.Header) {
	if auth != nil {
		util.ApplyCustomHeadersFromAttrs(r, credentialAttrsWithoutUserAgent(auth.Attributes), clientHeaders)
	}
	value := ConfiguredCredentialUserAgent(auth)
	if strings.HasPrefix(value, "$") || strings.Contains(strings.ToUpper(value), "$CPA-SESSION-ID") {
		if mapped := resolveCredentialUserAgent(r.Context(), value, clientHeaders); mapped != "" {
			setClientUserAgent(r.Header, mapped)
			return
		}
		value = ""
	}
	if value == "" && cfg != nil {
		value = cfg.ClaudeHeaderDefaults.UserAgent
	}
	applyClientUserAgentValue(r.Header, "claude", value)
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
	if override := resolveCredentialUserAgent(ctx, credentialUserAgentAttribute(auth), clientHeaders); override != "" {
		value = override
	}
	applyClientUserAgentValue(headers, provider, value)
}

func applyClientUserAgentValue(headers http.Header, provider, value string) {
	if !config.ValidUserAgent(value) || strings.TrimSpace(value) == "" {
		return
	}
	current := HeaderValueCaseInsensitive(headers, "User-Agent")
	value = ResolveConfiguredUserAgent(provider, value, current)
	if provider == "claude" {
		value = carryClaudeRequestFields(value, current)
	}
	setClientUserAgent(headers, value)
}

func setClientUserAgent(headers http.Header, value string) {
	for name := range headers {
		if strings.EqualFold(name, "User-Agent") {
			delete(headers, name)
		}
	}
	headers.Set("User-Agent", value)
}
