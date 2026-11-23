package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGeminiUserAgentV8Configuration(t *testing.T) {
	input := []byte("upstream: {gemini: {header-defaults: {user-agent: ' proxy/2 '}}}")
	if err := ValidateV8Config(input); err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseConfigBytes(input)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, input, 0600); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfigPreserveComments(path, cfg.ForAPIKey(), true); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(out, &node); err != nil {
		t.Fatal(err)
	}
	ua := yamlPath(node.Content[0], "upstream.gemini.header-defaults.user-agent")
	if ua == nil || ua.Value != "proxy/2" {
		t.Fatalf("shared Gemini default did not round trip: %v", ua)
	}
}

func TestGeminiUserAgentRejectsInvalidConfig(t *testing.T) {
	for _, data := range []string{
		`upstream: {gemini: {header-defaults: {user-agent: "bad\nheader"}}}`,
		`gemini-api-key: [{api-key: key, headers: {USER-agent: "bad\nheader"}}]`,
		`interactions-api-key: [{api-key: key, headers: {USER-agent: "bad\nheader"}}]`,
		`vertex-api-key: [{api-key: key, headers: {USER-agent: "bad\nheader"}}]`,
	} {
		if _, err := ParseConfigBytes([]byte(data)); err == nil || !strings.Contains(err.Error(), "printable ASCII") {
			t.Errorf("expected invalid User-Agent rejection for %s, got %v", data, err)
		}
	}
}

func TestGeminiUserAgentConflictingHeaders(t *testing.T) {
	for _, provider := range []string{"gemini", "interactions", "vertex"} {
		for _, tc := range []struct {
			first, second string
			conflict      bool
		}{
			{"mytool/1", "auto", true}, {"USER/1", "user/1", true}, {" auto", "AUTO", false}, {" mytool/1", "mytool/1", false},
		} {
			raw := "api-keys: {" + provider + ": [{name: group, keys: [{api-key: key}], headers: {User-Agent: '" + tc.first + "', user-agent: '" + tc.second + "'}}]}"
			if err := ValidateV8Config([]byte(raw)); (err != nil) != tc.conflict {
				t.Errorf("ValidateV8Config(%q) returned %v", raw, err)
			}
			if _, err := ParseConfigBytes([]byte(raw)); (err != nil) != tc.conflict {
				t.Errorf("ParseConfigBytes(%q) returned %v", raw, err)
			}
		}
	}
}

func TestGeminiUserAgentRejectsConflictingGroupEvenWhenShadowed(t *testing.T) {
	raw := []byte(`api-keys: {gemini: [{name: group, headers: {User-Agent: fixed, user-agent: auto}, keys: [{api-key: key, headers: {User-Agent: local}}]}]}`)
	if err := ValidateV8Config(raw); err == nil || !strings.Contains(err.Error(), "conflicting User-Agent") {
		t.Fatalf("conflict in group headers was accepted: %v", err)
	}
}
