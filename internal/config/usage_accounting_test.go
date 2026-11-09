package config

import "testing"

func TestUsageAccountingV8Configuration(t *testing.T) {
	cfg, errParse := ParseConfigBytes([]byte("observability:\n  usage:\n    accounting:\n      enabled: true\n      path: usage.db\n      detail-retention-days: 60\n      account-bindings: {credential: stable-account}\n      pricing: {refresh-interval: 12h}\n"))
	if errParse != nil {
		t.Fatal(errParse)
	}
	if !cfg.UsageAccounting.Enabled || cfg.UsageAccounting.DetailRetentionDays != 60 || cfg.UsageAccounting.AccountBindings["credential"] != "stable-account" {
		t.Fatalf("lost accounting configuration: %+v", cfg.UsageAccounting)
	}
	clone := cfg.CloneForRuntime()
	clone.UsageAccounting.AccountBindings["credential"] = "changed"
	if cfg.UsageAccounting.AccountBindings["credential"] != "stable-account" {
		t.Fatal("shared binding map")
	}
	for _, value := range []string{"detail-retention-days: -1", "pricing: {refresh-interval: 1s}", "pricing: {source-url: 'file:///tmp/prices'}", "model-aliases: {alias: ''}"} {
		if _, errInvalid := ParseConfigBytes([]byte("usage-accounting: {" + value + "}")); errInvalid == nil {
			t.Fatal("invalid accounting configuration accepted", value)
		}
	}
}
