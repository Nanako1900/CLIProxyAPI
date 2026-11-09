package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// UsageAccountingConfig controls the durable upstream-account usage ledger.
// Its switch is independent of the transient usage queue.
type UsageAccountingConfig struct {
	Enabled             bool               `yaml:"enabled" json:"enabled"`
	Path                string             `yaml:"path,omitempty" json:"path,omitempty"`
	DetailRetentionDays int                `yaml:"detail-retention-days,omitempty" json:"detail-retention-days,omitempty"`
	AccountBindings     map[string]string  `yaml:"account-bindings,omitempty" json:"account-bindings,omitempty"`
	ModelAliases        map[string]string  `yaml:"model-aliases,omitempty" json:"model-aliases,omitempty"`
	Pricing             UsagePricingConfig `yaml:"pricing,omitempty" json:"pricing,omitempty"`
}

type UsagePricingConfig struct {
	SourceURL       string `yaml:"source-url,omitempty" json:"source-url,omitempty"`
	RefreshInterval string `yaml:"refresh-interval,omitempty" json:"refresh-interval,omitempty"`
}

func (c UsageAccountingConfig) Validate() error {
	if c.DetailRetentionDays < 0 {
		return fmt.Errorf("usage accounting detail-retention-days must not be negative")
	}
	if raw := strings.TrimSpace(c.Pricing.SourceURL); raw != "" {
		u, errParse := url.Parse(raw)
		if errParse != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
			return fmt.Errorf("usage accounting pricing source-url must be an HTTP(S) URL without credentials or a fragment")
		}
	}
	if raw := strings.TrimSpace(c.Pricing.RefreshInterval); raw != "" {
		d, errParse := time.ParseDuration(raw)
		if errParse != nil || d < time.Minute {
			return fmt.Errorf("usage accounting pricing refresh-interval must be at least 1m")
		}
	}
	for key, value := range c.AccountBindings {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return fmt.Errorf("usage accounting account-bindings must contain non-empty identities")
		}
	}
	for key, value := range c.ModelAliases {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return fmt.Errorf("usage accounting model-aliases must contain non-empty model names")
		}
	}
	return nil
}
