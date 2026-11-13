package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const validPoolYAML = `routing:
  account-pools:
    enabled: true
    unscoped: {mode: deny}
    pools:
      - {id: basic, name: Basic, enabled: true}
      - {id: premium, name: Premium, enabled: true, strategy: fill-first}
access:
  clients:
    - {id: caller, api-key: caller-secret, allowed-pools: [basic]}
  api-keys: [legacy-secret]
  legacy-key-pools: [premium]
api-keys:
  codex:
    - name: premium-accounts
      base-url: https://upstream.example.invalid/v1
      pool-id: premium
      keys: [{api-key: upstream-secret}]
`

func TestAccountPoolsConfigRoundTrip(t *testing.T) {
	cfg, err := ParseConfigBytes([]byte(validPoolYAML))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AccountPools.Enabled || cfg.CodexKey[0].PoolID != "premium" {
		t.Fatalf("membership missing: %+v", cfg.AccountPools)
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParseConfigBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range []*Config{cfg, cfg.CloneForRuntime(), decoded} {
		if !view.PoolAccess("config-inline", "caller-secret", "basic") || view.PoolAccess("config-inline", "caller-secret", "premium") {
			t.Fatal("structured grant changed")
		}
		if !view.PoolAccess("config-inline", "legacy-secret", "premium") || view.PoolAccess("config-inline", "legacy-secret", "basic") {
			t.Fatal("legacy grant changed")
		}
		if view.CodexKey[0].PoolID != "premium" {
			t.Fatal("group inheritance lost")
		}
	}
	cfg.CloneForRuntime().PoolClients[0].AllowedPools[0] = "premium"
	if cfg.PoolClients[0].AllowedPools[0] != "basic" {
		t.Fatal("runtime clone aliases pool grants")
	}
}

func TestAccountPoolsConfigRejectsAmbiguity(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown pool field":              strings.Replace(validPoolYAML, "enabled: true\n    unscoped", "enabled: true\n    enabld: true\n    unscoped", 1),
		"unknown client field":            strings.Replace(validPoolYAML, "allowed-pools: [basic]", "allowed-pools: [basic], allowed-pool: [premium]", 1),
		"duplicate pool":                  strings.Replace(validPoolYAML, "id: premium, name: Premium", "id: basic, name: Premium", 1),
		"implicit enabled":                strings.Replace(validPoolYAML, "id: basic, name: Basic, enabled: true", "id: basic, name: Basic", 1),
		"invalid slug":                    strings.ReplaceAll(validPoolYAML, "premium", "../premium"),
		"missing grant pool":              strings.Replace(validPoolYAML, "allowed-pools: [basic]", "allowed-pools: [unknown]", 1),
		"mixed wildcard":                  strings.Replace(validPoolYAML, "allowed-pools: [basic]", "allowed-pools: ['*', basic]", 1),
		"duplicate key":                   strings.Replace(validPoolYAML, "caller-secret", "legacy-secret", 1),
		"undefined credential membership": strings.Replace(validPoolYAML, "pool-id: premium", "pool-id: undefined", 1),
		"unknown strategy":                strings.Replace(validPoolYAML, "fill-first", "random-invalid", 1),
		"default missing pool":            strings.Replace(validPoolYAML, "mode: deny", "mode: default, pool-id: missing", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfigBytes([]byte(raw)); err == nil {
				t.Fatal("unsafe config accepted")
			} else if strings.Contains(err.Error(), "caller-secret") || strings.Contains(err.Error(), "upstream-secret") {
				t.Fatal("validation leaked secret")
			}
		})
	}
}

func TestAccountPoolsGrantsAreProviderBound(t *testing.T) {
	cfg, err := ParseConfigBytes([]byte(validPoolYAML))
	if err != nil {
		t.Fatal(err)
	}
	cfg.PoolProviderClients = []PoolProviderClient{{Provider: "sso", ID: "caller", AllowedPools: []string{"basic"}}}
	if !cfg.PoolAccess("sso", "caller", "basic") || cfg.PoolAccess("forged", "caller", "basic") || cfg.PoolAccess("config-inline", "caller", "basic") {
		t.Fatal("provider identity was not bound")
	}
	disabled := false
	cfg.PoolProviderClients[0].Enabled = &disabled
	if cfg.PoolAccess("sso", "caller", "basic") {
		t.Fatal("disabled provider client accepted")
	}
}
