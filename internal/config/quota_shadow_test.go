package config

import (
	"strings"
	"testing"
)

func TestQuotaShadowConfigValidation(t *testing.T) {
	for _, field := range []string{"soft-limit-percent: .nan", "soft-limit-percent: .inf", "soft-limit-percent: -1", "soft-limit-percent: 101", "max-file-mb: -1", "max-file-mb: 65537", "file: \"line\\nname\""} {
		raw := []byte("config-version: 8\nrouting:\n  quota-shadow:\n    enabled: true\n    " + field + "\n")
		if _, err := ParseConfigBytes(raw); err == nil || !strings.Contains(err.Error(), "quota-shadow") {
			t.Fatalf("invalid setting accepted: %s (%v)", field, err)
		}
		if err := ValidateV8Config(raw); err == nil {
			t.Fatalf("v8 validation accepted %s", field)
		}
	}
	for _, field := range []string{"soft-limit-percent: 0", "soft-limit-percent: 0.1", "soft-limit-percent: 100", "max-file-mb: 0", "max-file-mb: 65536"} {
		if _, err := ParseConfigBytes([]byte("routing:\n  quota-shadow:\n    " + field)); err != nil {
			t.Fatalf("valid setting %s: %v", field, err)
		}
	}
}
