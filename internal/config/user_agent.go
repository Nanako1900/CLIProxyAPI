package config

import (
	"fmt"
	"strings"
)

// ValidUserAgent accepts empty defaults, "auto", and printable HTTP identities.
func ValidUserAgent(value string) bool {
	if len(value) > 1024 {
		return false
	}
	for _, c := range []byte(value) {
		if c < 32 || c > 126 {
			return false
		}
	}
	return true
}

// ValidateUserAgents rejects invalid identities before configuration is saved.
func (cfg *Config) ValidateUserAgents() error {
	if cfg == nil {
		return nil
	}
	for path, value := range map[string]string{
		"oauth.providers.codex.header-defaults.user-agent": cfg.CodexHeaderDefaults.UserAgent,
		"upstream.claude.header-defaults.user-agent":       cfg.ClaudeHeaderDefaults.UserAgent,
	} {
		if !ValidUserAgent(value) {
			return fmt.Errorf("%s must contain at most 1024 printable ASCII characters", path)
		}
	}
	validateHeaders := func(provider string, index int, headers map[string]string) error {
		for name, value := range headers {
			if strings.EqualFold(strings.TrimSpace(name), "User-Agent") && !ValidUserAgent(value) {
				return fmt.Errorf("%s credential %d User-Agent must contain at most 1024 printable ASCII characters", provider, index+1)
			}
		}
		return nil
	}
	for i, key := range cfg.CodexKey {
		if errValidate := validateHeaders("codex", i, key.Headers); errValidate != nil {
			return errValidate
		}
	}
	for i, key := range cfg.ClaudeKey {
		if errValidate := validateHeaders("claude", i, key.Headers); errValidate != nil {
			return errValidate
		}
	}
	return nil
}
