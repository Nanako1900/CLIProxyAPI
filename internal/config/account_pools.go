package config

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// AccountPoolsConfig defines the client-visible credential isolation boundary.
type AccountPoolsConfig struct {
	Enabled  bool               `yaml:"enabled" json:"enabled"`
	Unscoped PoolUnscopedConfig `yaml:"unscoped,omitempty" json:"unscoped,omitempty"`
	Pools    []AccountPool      `yaml:"pools,omitempty" json:"pools,omitempty"`
}

type PoolUnscopedConfig struct {
	Mode   string `yaml:"mode,omitempty" json:"mode,omitempty"`
	PoolID string `yaml:"pool-id,omitempty" json:"pool-id,omitempty"`
}

type AccountPool struct {
	ID       string `yaml:"id" json:"id"`
	Name     string `yaml:"name" json:"name"`
	Enabled  *bool  `yaml:"enabled" json:"enabled"`
	Strategy string `yaml:"strategy,omitempty" json:"strategy,omitempty"`
}

type PoolClient struct {
	ID           string   `yaml:"id" json:"id"`
	APIKey       string   `yaml:"api-key" json:"api-key"`
	Enabled      *bool    `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	AllowedPools []string `yaml:"allowed-pools" json:"allowed-pools"`
}

type PoolProviderClient struct {
	Provider     string   `yaml:"provider" json:"provider"`
	ID           string   `yaml:"id" json:"id"`
	Enabled      *bool    `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	AllowedPools []string `yaml:"allowed-pools" json:"allowed-pools"`
}

var poolIDPattern = regexp.MustCompile("^[a-z][a-z0-9-]{0,47}$")

func ValidPoolID(id string) bool      { return poolIDPattern.MatchString(id) }
func (p AccountPool) IsEnabled() bool { return p.Enabled != nil && *p.Enabled }
func (p PoolClient) IsEnabled() bool  { return p.Enabled == nil || *p.Enabled }

func decodePoolNode(node *yaml.Node, target any) error {
	data, errMarshal := yaml.Marshal(node)
	if errMarshal != nil {
		return errMarshal
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	return decoder.Decode(target)
}

func (p *AccountPoolsConfig) UnmarshalYAML(node *yaml.Node) error {
	type plain AccountPoolsConfig
	return decodePoolNode(node, (*plain)(p))
}
func (p *PoolClient) UnmarshalYAML(node *yaml.Node) error {
	type plain PoolClient
	return decodePoolNode(node, (*plain)(p))
}
func (p *PoolProviderClient) UnmarshalYAML(node *yaml.Node) error {
	type plain PoolProviderClient
	return decodePoolNode(node, (*plain)(p))
}

func (c *SDKConfig) ValidateAccountPools() error {
	if len(c.AccountPools.Pools) > 1024 || len(c.PoolClients)+len(c.PoolProviderClients) > 10000 {
		return fmt.Errorf("account pool configuration exceeds entry limit")
	}
	pools := make(map[string]AccountPool, len(c.AccountPools.Pools))
	for _, p := range c.AccountPools.Pools {
		if !ValidPoolID(p.ID) {
			return fmt.Errorf("invalid account pool ID")
		}
		if _, ok := pools[p.ID]; ok {
			return fmt.Errorf("duplicate account pool ID %q", p.ID)
		}
		name := strings.TrimSpace(p.Name)
		if len([]rune(name)) == 0 || len([]rune(name)) > 100 || strings.IndexFunc(name, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
			return fmt.Errorf("invalid account pool name for %q", p.ID)
		}
		if p.Enabled == nil {
			return fmt.Errorf("account pool %q requires enabled", p.ID)
		}
		switch p.Strategy {
		case "", "round-robin", "weighted-round-robin", "fill-first":
		default:
			return fmt.Errorf("invalid strategy for account pool %q", p.ID)
		}
		pools[p.ID] = p
	}
	switch c.AccountPools.Unscoped.Mode {
	case "", "deny":
		if c.AccountPools.Unscoped.PoolID != "" {
			return fmt.Errorf("unscoped pool-id requires default mode")
		}
	case "default":
		p, ok := pools[c.AccountPools.Unscoped.PoolID]
		if !ok || !p.IsEnabled() {
			return fmt.Errorf("unscoped default requires an enabled pool")
		}
	default:
		return fmt.Errorf("invalid unscoped account pool mode")
	}
	validateGrant := func(ids []string) error {
		if len(ids) > 1024 {
			return fmt.Errorf("account pool grant exceeds limit")
		}
		seen := make(map[string]bool)
		for _, id := range ids {
			if seen[id] {
				return fmt.Errorf("duplicate pool grant")
			}
			seen[id] = true
			if id == "*" && len(ids) == 1 {
				continue
			}
			if _, ok := pools[id]; !ok {
				return fmt.Errorf("grant references undefined pool %q", id)
			}
		}
		return nil
	}
	if err := validateGrant(c.LegacyKeyPools); err != nil {
		return err
	}
	keys := make(map[string]bool)
	for _, key := range c.APIKeys {
		keys[strings.TrimSpace(key)] = true
	}
	ids := make(map[string]bool)
	for _, client := range c.PoolClients {
		if !ValidPoolID(client.ID) || ids[client.ID] {
			return fmt.Errorf("invalid or duplicate pool client ID")
		}
		ids[client.ID] = true
		key := strings.TrimSpace(client.APIKey)
		if key == "" || keys[key] {
			return fmt.Errorf("empty or duplicate client credential")
		}
		keys[key] = true
		if err := validateGrant(client.AllowedPools); err != nil {
			return err
		}
	}
	ids = make(map[string]bool)
	for _, client := range c.PoolProviderClients {
		key := client.Provider + ":" + client.ID
		if client.Provider == "" || !ValidPoolID(client.ID) || ids[key] {
			return fmt.Errorf("invalid or duplicate provider client")
		}
		ids[key] = true
		if err := validateGrant(client.AllowedPools); err != nil {
			return err
		}
	}
	return nil
}

func (c *SDKConfig) AccountPool(id string) (AccountPool, bool) {
	for _, p := range c.AccountPools.Pools {
		if p.ID == id {
			return p, true
		}
	}
	return AccountPool{}, false
}

// NativePoolAccess validates the current key binding as well as its grant.
func (c *SDKConfig) NativePoolAccess(key, poolID string) bool {
	permits := func(ids []string) bool { return slices.Contains(ids, poolID) || slices.Contains(ids, "*") }
	for _, client := range c.PoolClients {
		if strings.TrimSpace(client.APIKey) == key {
			return client.IsEnabled() && permits(client.AllowedPools)
		}
	}
	for _, candidate := range c.APIKeys {
		if strings.TrimSpace(candidate) == key {
			return permits(c.LegacyKeyPools)
		}
	}
	return false
}

// PoolAccess accepts only an identity returned by a configured authentication provider.
func (c *SDKConfig) PoolAccess(provider, principal, poolID string) bool {
	if provider == "config-inline" {
		return c.NativePoolAccess(principal, poolID)
	}
	for _, client := range c.PoolProviderClients {
		if client.Provider == provider && client.ID == principal {
			return (client.Enabled == nil || *client.Enabled) && (slices.Contains(client.AllowedPools, poolID) || slices.Contains(client.AllowedPools, "*"))
		}
	}
	return false
}

// ValidateAccountPools also validates config-backed credential membership.
func (c *Config) ValidateAccountPools() error {
	if err := c.SDKConfig.ValidateAccountPools(); err != nil {
		return err
	}
	check := func(id string) error {
		if id == "" {
			return nil
		}
		if _, ok := c.AccountPool(id); !ok {
			return fmt.Errorf("credential references undefined pool %q", id)
		}
		return nil
	}
	for _, group := range [][]GeminiKey{c.GeminiKey, c.InteractionsKey} {
		for _, key := range group {
			if err := check(key.PoolID); err != nil {
				return err
			}
		}
	}
	for _, key := range c.ClaudeKey {
		if err := check(key.PoolID); err != nil {
			return err
		}
	}
	for _, group := range [][]CodexKey{c.CodexKey, c.XAIKey, c.MetaKey} {
		for _, key := range group {
			if err := check(key.PoolID); err != nil {
				return err
			}
		}
	}
	for _, key := range c.VertexCompatAPIKey {
		if err := check(key.PoolID); err != nil {
			return err
		}
	}
	for _, group := range c.OpenAICompatibility {
		if err := check(group.PoolID); err != nil {
			return err
		}
		for _, key := range group.APIKeyEntries {
			if key.PoolID != nil {
				if err := check(*key.PoolID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Validate the complete subtree before the v8 leaf mapper can discard unknown fields.
func validatePoolConfigNodes(node *yaml.Node) error {
	node = expandConfigAliases(node)
	for _, path := range []string{"routing.account-pools", "account-pools"} {
		if value := yamlPath(node, path); value != nil {
			var target AccountPoolsConfig
			if err := value.Decode(&target); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}
	}
	return nil
}
