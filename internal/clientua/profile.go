// Package clientua records the client-declared identity headers received by CPA.
package clientua

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

const MaxImportBytes = 32 << 10

var userAgentPattern = regexp.MustCompile(`^(claude-cli|codex-tui|codex_cli_rs|codex_exec|codex_vscode|codex|Codex Desktop)/([0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?)(?:[ (].*)?$`)
var softwareVersionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)

var claudeHeaders = []string{"User-Agent", "X-Stainless-Package-Version", "X-Stainless-Runtime-Version", "X-Stainless-Os", "X-Stainless-Arch", "X-Stainless-Timeout"}
var codexHeaders = []string{"User-Agent", "X-Codex-Beta-Features"}

// Profile contains only fields editable in the native header-default settings.
// Credentials, cookies, network addresses, and request bodies are never retained.
type Profile struct {
	Provider string            `json:"provider" yaml:"provider"`
	Version  string            `json:"version" yaml:"version"`
	Headers  map[string]string `json:"headers" yaml:"headers"`
}

func ValidProvider(provider string) bool { return provider == "claude" || provider == "codex" }

func headerValue(headers http.Header, name string) (string, error) {
	value := ""
	seen := false
	for key, values := range headers {
		if !strings.EqualFold(key, name) {
			continue
		}
		for _, candidate := range values {
			if !config.ValidUserAgent(candidate) {
				return "", fmt.Errorf("invalid %s header", name)
			}
			candidate = strings.TrimSpace(candidate)
			if seen && candidate != value {
				return "", fmt.Errorf("conflicting %s headers", name)
			}
			value = candidate
			seen = true
		}
	}
	return value, nil
}

// FromHeaders recognizes a declared client identity without claiming authenticity.
func FromHeaders(headers http.Header) (Profile, error) {
	ua, errHeader := headerValue(headers, "User-Agent")
	if errHeader != nil {
		return Profile{}, errHeader
	}
	match := userAgentPattern.FindStringSubmatch(ua)
	if len(match) != 3 {
		return Profile{}, fmt.Errorf("a Claude Code or Codex User-Agent with a version is required")
	}
	provider, allowed := "codex", codexHeaders
	if match[1] == "claude-cli" {
		provider, allowed = "claude", claudeHeaders
	}
	profile := Profile{Provider: provider, Version: match[2], Headers: make(map[string]string)}
	for _, name := range allowed {
		value, errValue := headerValue(headers, name)
		if errValue != nil {
			return Profile{}, errValue
		}
		if value == "" {
			continue
		}
		switch name {
		case "X-Stainless-Package-Version", "X-Stainless-Runtime-Version":
			if !softwareVersionPattern.MatchString(value) {
				return Profile{}, fmt.Errorf("invalid %s version", name)
			}
		case "X-Stainless-Timeout":
			seconds, errSeconds := strconv.ParseUint(value, 10, 32)
			if errSeconds != nil || seconds == 0 {
				return Profile{}, fmt.Errorf("invalid X-Stainless-Timeout value")
			}
		}
		profile.Headers[name] = value
	}
	return profile, nil
}

// Parse accepts a request-header block or a raw HTTP request. Parsing stops at
// the body boundary, and only whitelisted identity fields leave this function.
func Parse(raw string) (Profile, error) {
	if len(raw) > MaxImportBytes {
		return Profile{}, fmt.Errorf("request headers exceed 32 KiB")
	}
	headers := make(http.Header)
	scanner := bufio.NewScanner(strings.NewReader(raw))
	started := false
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			if started {
				break
			}
			continue
		}
		started = true
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		for _, allowed := range append(append([]string{}, claudeHeaders...), codexHeaders...) {
			if strings.EqualFold(name, allowed) {
				headers.Add(allowed, strings.TrimSpace(value))
				break
			}
		}
	}
	if errScan := scanner.Err(); errScan != nil {
		return Profile{}, fmt.Errorf("read request headers: %w", errScan)
	}
	return FromHeaders(headers)
}

func (p Profile) id() string {
	parts := []string{p.Provider}
	for _, key := range append(append([]string{}, claudeHeaders...), "X-Codex-Beta-Features") {
		parts = append(parts, key, p.Headers[key])
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:12])
}

func (p Profile) clone() Profile {
	clone := p
	clone.Headers = make(map[string]string, len(p.Headers))
	for key, value := range p.Headers {
		clone.Headers[key] = value
	}
	return clone
}
