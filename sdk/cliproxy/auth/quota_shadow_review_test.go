package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MoeForge/moego/ojson"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/pool"
)

func TestQuotaShadowRotationChecksExistingFileAndFailure(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing file", true: "rotation failure"}[blocked], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "shadow.jsonl")
			seed := strings.Repeat("x", 128) + "\n"
			if err := os.WriteFile(path, []byte(seed), 0600); err != nil {
				t.Fatal(err)
			}
			if blocked {
				if err := os.Mkdir(path+".1", 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path+".1", "keep"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r := newQuotaShadowRecorder(quotaShadowSettings{path: path, maxBytes: 64})
			r.emit(func(uint64) []byte { return []byte(`{"type":"selection_error"}`) })
			r.close()
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if blocked {
				if string(raw) != seed || r.monitor.status.WriteErrors != 1 || r.monitor.status.LastError != "rotate_failed" {
					t.Fatalf("failed rotation appended or went unreported: %+v", r.monitor.status)
				}
			} else {
				backup, errRead := os.ReadFile(path + ".1")
				if errRead != nil || string(backup) != seed || strings.Contains(string(raw), "xxx") {
					t.Fatalf("existing oversized file was not rotated before the first write: %v", errRead)
				}
			}
		})
	}
}

func TestQuotaShadowPathReuseWaitsForPriorWriter(t *testing.T) {
	m := NewManager(nil, nil, nil)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.jsonl")
	m.SetConfig(quotaShadowConfig(path))
	first := m.quotaShadow.Load()
	started, release := make(chan struct{}), make(chan struct{})
	first.emit(func(uint64) []byte { close(started); <-release; return []byte(`{"phase":"first"}`) })
	<-started
	m.SetConfig(quotaShadowConfig(filepath.Join(dir, "b.jsonl")))
	m.SetConfig(&internalconfig.Config{})
	m.SetConfig(quotaShadowConfig(filepath.Join(dir, ".", "a.jsonl")))
	last := m.quotaShadow.Load()
	overlapped := make(chan bool, 1)
	last.emit(func(uint64) []byte {
		select {
		case <-first.done:
			overlapped <- false
		default:
			overlapped <- true
		}
		return []byte(`{"phase":"last"}`)
	})
	before := m.QuotaShadowSnapshot()
	close(release)
	m.StopQuotaShadow()
	if before.State != "draining" || <-overlapped {
		t.Fatalf("successor did not wait for the old writer: %+v", before)
	}
	lines := readQuotaShadowLines(t, path)
	if len(lines) != 2 || ojson.GetBytes(lines[0], "phase").String() != "first" || ojson.GetBytes(lines[1], "phase").String() != "last" {
		t.Fatalf("file writes were reordered: %s", lines)
	}
}

func TestQuotaShadowRejectsNonRegularOutput(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "private")
	if err := os.WriteFile(target, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	r := newQuotaShadowRecorder(quotaShadowSettings{path: link})
	r.emit(func(uint64) []byte { return []byte(`{}`) })
	r.close()
	raw, _ := os.ReadFile(target)
	if string(raw) != "preserve" || r.monitor.status.LastError != "open_failed" || r.monitor.status.WriteErrors != 1 {
		t.Fatal("non-regular output was written or not reported")
	}
}

func TestQuotaShadowMonitorBoundsAndClassifiesComparisons(t *testing.T) {
	m := NewManager(nil, nil, nil)
	m.SetConfig(quotaShadowConfig(filepath.Join(t.TempDir(), "shadow.jsonl")))
	r := m.quotaShadow.Load()
	for i := 0; i < quotaShadowRecentLimit+5; i++ {
		r.emit(func(uint64) []byte {
			return []byte(`{"type":"selection","actual":"a","shadow":"b","actual_in_pool":true}`)
		})
	}
	for _, line := range []string{
		`{"type":"selection","actual":"a","shadow":"a","actual_in_pool":true,"agree":true}`,
		`{"type":"selection","actual":"a","shadow":"b","actual_in_pool":false}`,
		`{"type":"selection","actual":"a","shadow":""}`,
		`{"type":"selection_error","error":"never expose this raw error"}`,
		`{"type":"observation","success":true,"auth":"a","signals":{"Authorization":"secret"}}`,
		`{"type":"observation","success":false,"auth":"a","status":429}`,
	} {
		value := line
		r.emit(func(uint64) []byte { return []byte(value) })
	}
	m.StopQuotaShadow()
	s := m.QuotaShadowSnapshot()
	if s.Written != 211 || s.Processed != s.Written || s.Comparable != 206 || s.Disagreements != 205 || s.Agreements != 1 || s.Structural != 1 || s.NoCandidates != 1 || s.SelectionErrors != 1 || s.Successes != 1 || s.Failures != 1 {
		t.Fatalf("incorrect totals: %+v", s)
	}
	if len(s.Recent) != quotaShadowRecentLimit || s.State != "stopped" {
		t.Fatal("snapshot not bounded")
	}
	s.Recent[0].Model = "changed"
	if m.QuotaShadowSnapshot().Recent[0].Model == "changed" {
		t.Fatal("snapshot aliases recorder memory")
	}
}

func TestQuotaShadowPoolScopePinsAndSecretRedaction(t *testing.T) {
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	cfg := poolTestConfig()
	cfg.Routing.QuotaShadow = internalconfig.QuotaShadowConfig{Enabled: true, File: filepath.Join(t.TempDir(), "shadow.jsonl")}
	m.SetConfig(cfg)
	m.executors["claude"] = schedulerTestExecutor{provider: "claude"}
	for _, a := range []*Auth{{ID: "a", Provider: "claude", PoolID: "basic"}, {ID: "b", Provider: "claude", PoolID: "basic"}, {ID: "foreign", Provider: "claude", PoolID: "premium"}} {
		if _, err := m.Register(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	ctx := poolTestContext("basic")
	opts := cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.PinnedAuthMetadataKey: "a", cliproxyexecutor.CanonicalSessionIDMetadataKey: "raw-session-secret"}}
	candidates := m.quotaShadowCandidates(ctx, []string{"claude"}, "", opts, nil, time.Now())
	if len(candidates) != 1 || candidates[0].id != "a" {
		t.Fatalf("pin/pool eligibility lost: %+v", candidates)
	}
	a, _, _, err := m.pickNextMixed(ctx, []string{"claude"}, "", opts, nil)
	if err != nil || a.ID != "a" {
		t.Fatalf("real pick changed: %v", err)
	}
	m.MarkResult(ctx, Result{AuthID: "a", Provider: "claude", Success: true})
	m.observeQuotaShadowSelection(ctx, []string{"claude"}, "", opts, nil, nil, "", errors.New("Bearer forbidden-secret"))
	other := pool.WithScope(context.Background(), pool.Scope{ID: "premium", Principal: "another-client"})
	if quotaShadowSessionHash(ctx, opts) == quotaShadowSessionHash(other, opts) {
		t.Fatal("session identity crosses pools")
	}
	m.StopQuotaShadow()
	lines := readQuotaShadowLines(t, cfg.Routing.QuotaShadow.File)
	if len(lines) != 3 {
		t.Fatalf("records = %d", len(lines))
	}
	for _, line := range lines {
		if ojson.GetBytes(line, "pool_id").String() != "basic" || strings.Contains(string(line), "secret") || strings.Contains(string(line), "foreign") {
			t.Fatalf("scope or redaction failure: %s", line)
		}
	}
}

func TestQuotaShadowRankingIsIndependentOfInputOrder(t *testing.T) {
	values := []quotaShadowEvaluation{
		{candidate: quotaShadowCandidate{id: "z"}, perishLong: .300},
		{candidate: quotaShadowCandidate{id: "a"}, perishLong: .309},
		{candidate: quotaShadowCandidate{id: "m"}, perishLong: .318},
	}
	for _, order := range [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		id, _ := pickQuotaShadow([]quotaShadowEvaluation{values[order[0]], values[order[1]], values[order[2]]})
		if id != "m" {
			t.Fatalf("order %v selected %s", order, id)
		}
	}
}

func TestQuotaShadowHomeDoesNotRecordObservations(t *testing.T) {
	m := NewManager(nil, nil, nil)
	cfg := quotaShadowConfig(filepath.Join(t.TempDir(), "shadow.jsonl"))
	cfg.Home.Enabled = true
	m.SetConfig(cfg)
	m.observeQuotaShadowResult(context.Background(), Result{Success: true}, &Auth{ID: "home-account"}, time.Now())
	if m.QuotaShadowSnapshot().State != "home" {
		t.Fatal("Home state was not surfaced")
	}
	m.StopQuotaShadow()
	if _, err := os.Stat(cfg.Routing.QuotaShadow.File); !os.IsNotExist(err) {
		t.Fatal("Home observation was recorded")
	}
}
