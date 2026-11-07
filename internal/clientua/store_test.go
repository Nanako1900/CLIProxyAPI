package clientua

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T, path string) *Store {
	t.Helper()
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if errClose := s.Close(); errClose != nil {
			t.Error(errClose)
		}
	})
	return s
}

func TestStoreDeduplicatesAndPersistsOnlyIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth", ".client-user-agents.yaml")
	s := newTestStore(t, path)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	headers := http.Header{"User-Agent": {"claude-cli/2.1.319 (external, cli)"}, "Authorization": {"Bearer secret"}, "Cookie": {"private-cookie"}}
	s.Observe(headers)
	first := s.List("claude")[0]
	now = now.Add(time.Minute)
	s.Observe(headers)
	profile, err := FromHeaders(headers)
	if err != nil {
		t.Fatal(err)
	}
	profile.Headers["Authorization"] = "must-be-stripped"
	imported, err := s.Import(profile)
	if err != nil {
		t.Fatal(err)
	}
	entries := s.List("claude")
	if len(entries) != 1 || entries[0].Count != 2 || !entries[0].Observed || !entries[0].Imported || !entries[0].FirstSeen.Equal(first.FirstSeen) || !entries[0].LastSeen.Equal(now) {
		t.Fatalf("unexpected catalog: %#v", entries)
	}
	imported.Headers["User-Agent"] = "mutated"
	entries[0].Headers["User-Agent"] = "mutated"
	if s.List("claude")[0].Headers["User-Agent"] == "mutated" {
		t.Fatal("returned map aliases store")
	}
	if errClose := s.Close(); errClose != nil {
		t.Fatal(errClose)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Authorization", "Cookie", "secret", "private-cookie", "must-be-stripped"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("stored unrelated data: %s", forbidden)
		}
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm()&0077 != 0 {
		t.Fatalf("unsafe file mode: %v", stat.Mode())
	}
	loaded := newTestStore(t, path)
	if got := loaded.List("claude"); len(got) != 1 || got[0].Count != 2 || got[0].ID != first.ID {
		t.Fatalf("reload: %#v", got)
	}
	if errDelete := loaded.Delete("codex", first.ID); errDelete != nil {
		t.Fatal(errDelete)
	}
	if len(loaded.List("claude")) != 1 {
		t.Fatal("wrong provider deleted sample")
	}
	if errDelete := loaded.Delete("claude", first.ID); errDelete != nil {
		t.Fatal(errDelete)
	}
	if got := newTestStore(t, path).List(""); len(got) != 0 {
		t.Fatalf("delete not persisted: %#v", got)
	}
	if _, errImport := s.Import(profile); errImport == nil {
		t.Fatal("import into closed store succeeded")
	}
}

func TestStoreBoundedPerProviderAndNewestFirst(t *testing.T) {
	s := newTestStore(t, "")
	now := time.Unix(1, 0)
	s.now = func() time.Time { return now }
	for _, provider := range []string{"claude-cli", "codex_cli_rs"} {
		for i := 0; i < LimitPerProvider+3; i++ {
			now = now.Add(time.Second)
			s.Observe(http.Header{"User-Agent": {fmt.Sprintf("%s/1.2.%d", provider, i)}})
		}
	}
	for _, provider := range []string{"claude", "codex"} {
		list := s.List(provider)
		if len(list) != LimitPerProvider || list[0].Version != "1.2.102" || list[len(list)-1].Version != "1.2.3" {
			t.Fatalf("wrong bounded order for %s: %#v", provider, list)
		}
	}
}

func TestStoreConcurrentObservationAndShutdownFlush(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.yaml")
	s := newTestStore(t, path)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			for j := 0; j < 30; j++ {
				s.Observe(http.Header{"User-Agent": {"codex-tui/1.2.3"}})
				s.List("codex")
			}
		})
	}
	wg.Wait()
	if errClose := s.Close(); errClose != nil {
		t.Fatal(errClose)
	}
	entries := newTestStore(t, path).List("codex")
	if len(entries) != 1 || entries[0].Count != 360 {
		t.Fatalf("lost observations: %#v", entries)
	}
}

func TestStoreRevalidatesLoadedSamples(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.yaml")
	data := "- id: forged\n  provider: codex\n  version: forged\n  headers:\n    User-Agent: codex/1.2.3\n    Authorization: secret\n- provider: claude\n  headers:\n    User-Agent: codex/2.0.0\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	s := newTestStore(t, path)
	entries := s.List("")
	if len(entries) != 1 || entries[0].Version != "1.2.3" || entries[0].ID == "forged" || len(entries[0].Headers) != 1 {
		t.Fatalf("invalid loaded identity: %#v", entries)
	}
}
