package auth

import "testing"

type poolMetadataStorage struct{ metadata map[string]any }

func (s *poolMetadataStorage) SetMetadata(metadata map[string]any) { s.metadata = metadata }
func (s *poolMetadataStorage) SaveTokenToFile(string) error        { return nil }

func TestApplyAuthPoolMetadataUsesPersistedHostMembership(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source map[string]any
		want   string
	}{
		{"assigned", map[string]any{"pool_id": "kiro"}, "kiro"},
		{"explicit unassignment", map[string]any{"pool_id": ""}, ""},
		{"malformed fails closed", map[string]any{"pool_id": 42}, "!invalid"},
		{"null fails closed", map[string]any{"pool_id": nil}, "!invalid"},
		{"absent preserves plugin source", map[string]any{}, "plugin-source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storage := &poolMetadataStorage{}
			a := &Auth{PoolID: "plugin-source", Metadata: map[string]any{"pool_id": "plugin-source", "access_token": "fixture"}, Storage: storage}
			ApplyAuthPoolMetadata(a, tc.source)
			if a.AccountPoolID() != tc.want || a.Metadata["access_token"] != "fixture" {
				t.Fatalf("unexpected restored membership: %q", a.AccountPoolID())
			}
			if _, exists := tc.source["pool_id"]; exists && storage.metadata["pool_id"] != tc.source["pool_id"] {
				t.Fatal("plugin storage was not synchronized with host membership")
			}
		})
	}
	ApplyAuthPoolMetadata(nil, nil)
	a := &Auth{}
	ApplyAuthPoolMetadata(a, map[string]any{"pool_id": "kiro"})
	if a.AccountPoolID() != "kiro" {
		t.Fatal("membership not initialized on an empty parser record")
	}
}
