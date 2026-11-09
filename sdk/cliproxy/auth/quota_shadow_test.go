package auth

import (
	"bufio"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/MoeForge/moego/ojson"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func quotaShadowConfig(path string) *internalconfig.Config {
	return &internalconfig.Config{Routing: internalconfig.RoutingConfig{QuotaShadow: internalconfig.QuotaShadowConfig{Enabled: true, File: path}}}
}

func claudeShadowSignals(now time.Time, used5h float64, reset5h time.Duration, used7d float64, reset7d time.Duration) map[string]string {
	return map[string]string{
		"Anthropic-Ratelimit-Unified-5h-Utilization": strconv.FormatFloat(used5h, 'f', -1, 64),
		"Anthropic-Ratelimit-Unified-5h-Reset":       strconv.FormatInt(now.Add(reset5h).Unix(), 10),
		"Anthropic-Ratelimit-Unified-7d-Utilization": strconv.FormatFloat(used7d, 'f', -1, 64),
		"Anthropic-Ratelimit-Unified-7d-Reset":       strconv.FormatInt(now.Add(reset7d).Unix(), 10),
	}
}

func TestPickQuotaShadowRanking(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		candidates []quotaShadowEvaluation
		wantID     string
		wantReason string
	}{
		{
			name:       "no candidates",
			wantReason: "no_candidates",
		},
		{
			name:       "single candidate",
			candidates: []quotaShadowEvaluation{{candidate: quotaShadowCandidate{id: "a"}}},
			wantID:     "a",
			wantReason: "only_candidate",
		},
		{
			name: "soft limited credential loses even with perishing quota",
			candidates: []quotaShadowEvaluation{
				{candidate: quotaShadowCandidate{id: "a"}, softLimited: true, perishLong: 0.5},
				{candidate: quotaShadowCandidate{id: "b"}},
			},
			wantID:     "b",
			wantReason: "soft_limit",
		},
		{
			name: "weekly perishing quota wins over short perishing quota",
			candidates: []quotaShadowEvaluation{
				{candidate: quotaShadowCandidate{id: "a"}, perishShort: 0.9},
				{candidate: quotaShadowCandidate{id: "b"}, perishLong: 0.2},
			},
			wantID:     "b",
			wantReason: "perish_long",
		},
		{
			name: "short perishing quota decides when weekly ties",
			candidates: []quotaShadowEvaluation{
				{candidate: quotaShadowCandidate{id: "a"}, perishShort: 0.1, shortActive: true},
				{candidate: quotaShadowCandidate{id: "b"}, perishShort: 0.6, shortActive: true},
			},
			wantID:     "b",
			wantReason: "perish_short",
		},
		{
			name: "idle short window preferred when nothing perishes",
			candidates: []quotaShadowEvaluation{
				{candidate: quotaShadowCandidate{id: "a"}, shortActive: true, longRemaining: 1},
				{candidate: quotaShadowCandidate{id: "b"}, longRemaining: 0.5},
			},
			wantID:     "b",
			wantReason: "short_idle",
		},
		{
			name: "more remaining weekly quota spreads load",
			candidates: []quotaShadowEvaluation{
				{candidate: quotaShadowCandidate{id: "a"}, longRemaining: 0.3},
				{candidate: quotaShadowCandidate{id: "b"}, longRemaining: 0.7},
			},
			wantID:     "b",
			wantReason: "long_remaining",
		},
		{
			name: "differences below epsilon fall through to ID",
			candidates: []quotaShadowEvaluation{
				{candidate: quotaShadowCandidate{id: "b"}, perishLong: 0.305},
				{candidate: quotaShadowCandidate{id: "a"}, perishLong: 0.3},
			},
			wantID:     "a",
			wantReason: "id",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotReason := pickQuotaShadow(tt.candidates)
			if gotID != tt.wantID || gotReason != tt.wantReason {
				t.Fatalf("pickQuotaShadow() = (%q, %q), want (%q, %q)", gotID, gotReason, tt.wantID, tt.wantReason)
			}
		})
	}
}

func TestEvaluateQuotaShadowCandidate(t *testing.T) {
	t.Parallel()

	now := time.Unix(1787000000, 0)
	tests := []struct {
		name            string
		provider        string
		signals         map[string]string
		wantSoftLimited bool
		wantShortActive bool
		wantPerishShort float64
		wantPerishLong  float64
		wantLongRemain  float64
	}{
		{
			name:           "no observation is neutral",
			provider:       "claude",
			wantLongRemain: 1,
		},
		{
			name:            "underused short window near reset",
			provider:        "claude",
			signals:         claudeShadowSignals(now, 0.2, time.Hour, 0.5, 3*24*time.Hour),
			wantShortActive: true,
			wantPerishShort: 0.6,
			// 50% weekly left with 3 of 7 days to go perishes 0.5 - 3/7 at full pace.
			wantPerishLong: 0.5 - 3.0/7.0,
			wantLongRemain: 0.5,
		},
		{
			name:            "short window above soft limit",
			provider:        "claude",
			signals:         claudeShadowSignals(now, 0.95, 4*time.Hour, 0.1, 6*24*time.Hour),
			wantSoftLimited: true,
			wantShortActive: true,
			wantPerishLong:  0.9 - 6.0/7.0,
			wantLongRemain:  0.9,
		},
		{
			name:     "weekly quota expiring soon",
			provider: "claude",
			// 60% weekly left with 12h to go: at most 12h/168h is consumable at full pace.
			signals:         claudeShadowSignals(now, 0, -time.Minute, 0.4, 12*time.Hour),
			wantPerishLong:  0.6 - 12.0/168.0,
			wantLongRemain:  0.6,
			wantShortActive: false,
		},
		{
			name:     "rejected status soft-limits",
			provider: "claude",
			signals: map[string]string{
				"Anthropic-Ratelimit-Unified-5h-Status": "rejected",
				"Anthropic-Ratelimit-Unified-5h-Reset":  strconv.FormatInt(now.Add(time.Hour).Unix(), 10),
			},
			wantSoftLimited: true,
			wantLongRemain:  1,
		},
		{
			name:     "scoped windows are not scored",
			provider: "codex",
			signals: map[string]string{
				"X-Codex-Bengalfox-Primary-Used-Percent":   "99",
				"X-Codex-Bengalfox-Primary-Window-Minutes": "300",
			},
			wantLongRemain: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evaluateQuotaShadowCandidate(quotaShadowCandidate{id: "x", provider: tt.provider, observedAt: now, signals: tt.signals}, now, 0.9)
			if got.softLimited != tt.wantSoftLimited || got.shortActive != tt.wantShortActive ||
				!approxEqual(got.perishShort, tt.wantPerishShort) || !approxEqual(got.perishLong, tt.wantPerishLong) ||
				!approxEqual(got.longRemaining, tt.wantLongRemain) {
				t.Fatalf("evaluation = %+v", got)
			}
		})
	}
}

func readQuotaShadowLines(t *testing.T, path string) [][]byte {
	t.Helper()
	file, errOpen := os.Open(path)
	if errOpen != nil {
		t.Fatalf("open %s: %v", path, errOpen)
	}
	defer func() {
		if errClose := file.Close(); errClose != nil {
			t.Errorf("close %s: %v", path, errClose)
		}
	}()
	var lines [][]byte
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for scanner.Scan() {
		lines = append(lines, append([]byte(nil), scanner.Bytes()...))
	}
	if errScan := scanner.Err(); errScan != nil {
		t.Fatalf("scan %s: %v", path, errScan)
	}
	return lines
}

func TestQuotaShadowRecorderWritesAndRotates(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "shadow.jsonl")
	recorder := newQuotaShadowRecorder(quotaShadowSettings{path: path, maxBytes: 40})
	for index := 0; index < 3; index++ {
		value := index
		if !recorder.emit(func(uint64) []byte { return []byte(`{"n":` + strconv.Itoa(value) + `,"pad":"xxxxxxxxxx"}`) }) {
			t.Fatalf("emit #%d rejected", index)
		}
	}
	recorder.close()
	if recorder.emit(func(uint64) []byte { return []byte(`{}`) }) {
		t.Fatal("emit after close accepted")
	}

	current := readQuotaShadowLines(t, path)
	rotated := readQuotaShadowLines(t, path+".1")
	if len(current) != 1 || len(rotated) != 1 {
		t.Fatalf("current=%d rotated=%d lines, want 1 and 1", len(current), len(rotated))
	}
	if got := ojson.GetBytes(current[0], "n").Int(); got != 2 {
		t.Fatalf("current line n = %d, want 2", got)
	}
	if got := ojson.GetBytes(rotated[0], "n").Int(); got != 1 {
		t.Fatalf("rotated line n = %d, want 1", got)
	}
}

func TestQuotaShadowRecorderDropsWhenQueueFull(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "shadow.jsonl")
	recorder := newQuotaShadowRecorder(quotaShadowSettings{path: path})
	started := make(chan struct{})
	release := make(chan struct{})
	if !recorder.emit(func(uint64) []byte {
		close(started)
		<-release
		return []byte(`{"blocking":true}`)
	}) {
		t.Fatal("blocking emit rejected")
	}
	<-started
	for index := 0; index < quotaShadowQueueSize; index++ {
		if !recorder.emit(func(uint64) []byte { return nil }) {
			t.Fatalf("queued emit #%d rejected", index)
		}
	}
	if recorder.emit(func(uint64) []byte { return nil }) {
		t.Fatal("emit into a full queue was accepted")
	}
	if got := recorder.dropped.Load(); got != 1 {
		t.Fatalf("dropped = %d, want 1", got)
	}
	close(release)
	recorder.close()
}

func TestManagerQuotaShadowRecordsSelectionWithoutChangingRouting(t *testing.T) {
	t.Parallel()

	now := time.Now()
	path := filepath.Join(t.TempDir(), "shadow.jsonl")
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.executors["claude"] = schedulerTestExecutor{provider: "claude"}
	manager.SetConfig(quotaShadowConfig(path))
	t.Cleanup(manager.StopQuotaShadow)

	registerAuth := func(id string, signals map[string]string) {
		t.Helper()
		if _, errRegister := manager.Register(context.Background(), &Auth{
			ID:       id,
			Provider: "claude",
			Quota:    QuotaState{ObservedAt: now, Signals: signals},
		}); errRegister != nil {
			t.Fatalf("Register(%s) error = %v", id, errRegister)
		}
	}
	// Round-robin starts at the lowest ID, which is soft-limited in the shadow view.
	registerAuth("claude-a", claudeShadowSignals(now, 0.95, 2*time.Hour, 0.3, 5*24*time.Hour))
	registerAuth("claude-b", claudeShadowSignals(now, 0.1, 4*time.Hour, 0.3, 5*24*time.Hour))

	picked, _, provider, errPick := manager.pickNextMixed(context.Background(), []string{"claude"}, "", cliproxyexecutor.Options{}, map[string]struct{}{})
	if errPick != nil {
		t.Fatalf("pickNextMixed() error = %v", errPick)
	}
	if picked == nil || picked.ID != "claude-a" || provider != "claude" {
		t.Fatalf("pickNextMixed() = (%v, %q), want claude-a/claude", picked, provider)
	}
	manager.MarkResult(context.Background(), Result{AuthID: "claude-a", Provider: "claude", Model: "claude-test", Success: false, Error: &Error{Code: "rate_limited", HTTPStatus: 429}})
	manager.StopQuotaShadow()

	lines := readQuotaShadowLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("got %d records, want 2: %s", len(lines), lines)
	}
	selection := lines[0]
	if got := ojson.GetBytes(selection, "type").String(); got != "selection" {
		t.Fatalf("record type = %q, want selection: %s", got, selection)
	}
	if got := ojson.GetBytes(selection, "actual").String(); got != "claude-a" {
		t.Fatalf("actual = %q, want claude-a", got)
	}
	if got := ojson.GetBytes(selection, "shadow").String(); got != "claude-b" {
		t.Fatalf("shadow = %q, want claude-b: %s", got, selection)
	}
	if got := ojson.GetBytes(selection, "shadow_reason").String(); got != "soft_limit" {
		t.Fatalf("shadow_reason = %q, want soft_limit", got)
	}
	if ojson.GetBytes(selection, "agree").Bool() {
		t.Fatal("agree = true, want false")
	}
	if got := ojson.GetBytes(selection, "candidates.#").Int(); got != 2 {
		t.Fatalf("candidates = %d, want 2", got)
	}
	if got := ojson.GetBytes(selection, "candidates.0.windows.#").Int(); got != 2 {
		t.Fatalf("candidate windows = %d, want 2: %s", got, selection)
	}

	observation := lines[1]
	if got := ojson.GetBytes(observation, "type").String(); got != "observation" {
		t.Fatalf("record type = %q, want observation: %s", got, observation)
	}
	if got := ojson.GetBytes(observation, "status").Int(); got != 429 {
		t.Fatalf("observation status = %d, want 429: %s", got, observation)
	}
	if ojson.GetBytes(observation, "success").Bool() {
		t.Fatal("observation success = true, want false")
	}
}

func TestManagerQuotaShadowRecordsSelectionErrors(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "shadow.jsonl")
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.executors["claude"] = schedulerTestExecutor{provider: "claude"}
	manager.SetConfig(quotaShadowConfig(path))
	t.Cleanup(manager.StopQuotaShadow)

	if _, _, _, errPick := manager.pickNextMixed(context.Background(), []string{"claude"}, "claude-test", cliproxyexecutor.Options{}, map[string]struct{}{}); errPick == nil {
		t.Fatal("pickNextMixed() error = nil, want no auth available")
	}
	manager.StopQuotaShadow()

	lines := readQuotaShadowLines(t, path)
	if len(lines) != 1 || ojson.GetBytes(lines[0], "type").String() != "selection_error" {
		t.Fatalf("records = %s, want one selection_error", lines)
	}
}

func TestManagerQuotaShadowConfigLifecycle(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manager := NewManager(nil, nil, nil)
	t.Cleanup(manager.StopQuotaShadow)
	if manager.quotaShadow.Load() != nil {
		t.Fatal("recorder enabled without configuration")
	}
	enabled := &internalconfig.Config{Routing: internalconfig.RoutingConfig{QuotaShadow: internalconfig.QuotaShadowConfig{Enabled: true, File: filepath.Join(dir, "a.jsonl")}}}
	manager.SetConfig(enabled)
	first := manager.quotaShadow.Load()
	if first == nil || first.softLimit() != quotaShadowDefaultSoftLimit || first.maxBytes.Load() != quotaShadowDefaultMaxBytes {
		t.Fatalf("recorder = %+v, want defaults", first)
	}
	manager.SetConfig(enabled)
	if manager.quotaShadow.Load() != first {
		t.Fatal("unchanged configuration replaced the recorder")
	}
	tuned := &internalconfig.Config{Routing: internalconfig.RoutingConfig{QuotaShadow: internalconfig.QuotaShadowConfig{Enabled: true, File: filepath.Join(dir, "a.jsonl"), SoftLimitPercent: 80, MaxFileMB: 1}}}
	manager.SetConfig(tuned)
	if manager.quotaShadow.Load() != first {
		t.Fatal("same-path settings change replaced the recorder; two recorders would share one file")
	}
	if !approxEqual(first.softLimit(), 0.8) || first.maxBytes.Load() != 1<<20 {
		t.Fatalf("in-place update not applied: softLimit=%v maxBytes=%d", first.softLimit(), first.maxBytes.Load())
	}
	moved := &internalconfig.Config{Routing: internalconfig.RoutingConfig{QuotaShadow: internalconfig.QuotaShadowConfig{Enabled: true, File: filepath.Join(dir, "b.jsonl")}}}
	manager.SetConfig(moved)
	second := manager.quotaShadow.Load()
	if second == nil || second == first || second.path != filepath.Join(dir, "b.jsonl") {
		t.Fatalf("recorder after path change = %+v", second)
	}
	manager.SetConfig(&internalconfig.Config{})
	if manager.quotaShadow.Load() != nil {
		t.Fatal("recorder still enabled after disabling")
	}
	manager.SetConfig(enabled)
	manager.StopQuotaShadow()
	manager.SetConfig(enabled)
	if manager.quotaShadow.Load() != nil {
		t.Fatal("config update restarted a stopped recorder")
	}
}

func TestQuotaShadowRecorderRecoversJobPanic(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "shadow.jsonl")
	recorder := newQuotaShadowRecorder(quotaShadowSettings{path: path})
	recorder.emit(func(uint64) []byte { panic("malformed signal") })
	recorder.emit(func(uint64) []byte { return []byte(`{"after":true}`) })
	recorder.close()

	lines := readQuotaShadowLines(t, path)
	if len(lines) != 1 || !ojson.GetBytes(lines[0], "after").Bool() {
		t.Fatalf("records = %s, want only the record after the panic", lines)
	}
}

func TestManagerQuotaShadowDisabledRecordsNothing(t *testing.T) {
	t.Parallel()

	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.executors["claude"] = schedulerTestExecutor{provider: "claude"}
	manager.SetConfig(&internalconfig.Config{})
	if _, errRegister := manager.Register(context.Background(), &Auth{ID: "claude-a", Provider: "claude"}); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	picked, _, _, errPick := manager.pickNextMixed(context.Background(), []string{"claude"}, "", cliproxyexecutor.Options{}, map[string]struct{}{})
	if errPick != nil || picked == nil || picked.ID != "claude-a" {
		t.Fatalf("pickNextMixed() = (%v, %v), want claude-a", picked, errPick)
	}
	if manager.quotaShadow.Load() != nil {
		t.Fatal("recorder created while disabled")
	}
}

func TestManagerQuotaShadowLeavesAuthStateUnchanged(t *testing.T) {
	t.Parallel()

	now := time.Now()
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.executors["claude"] = schedulerTestExecutor{provider: "claude"}
	manager.SetConfig(quotaShadowConfig(filepath.Join(t.TempDir(), "shadow.jsonl")))
	t.Cleanup(manager.StopQuotaShadow)
	signals := claudeShadowSignals(now, 0.4, 2*time.Hour, 0.3, 5*24*time.Hour)
	if _, errRegister := manager.Register(context.Background(), &Auth{ID: "claude-a", Provider: "claude", Quota: QuotaState{ObservedAt: now, Signals: signals}}); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	manager.mu.RLock()
	before := manager.auths["claude-a"].Clone()
	manager.mu.RUnlock()

	if _, _, _, errPick := manager.pickNextMixed(context.Background(), []string{"claude"}, "", cliproxyexecutor.Options{}, map[string]struct{}{}); errPick != nil {
		t.Fatalf("pickNextMixed() error = %v", errPick)
	}
	manager.StopQuotaShadow()

	manager.mu.RLock()
	after := manager.auths["claude-a"].Clone()
	manager.mu.RUnlock()
	if before.Generation != after.Generation || !before.UpdatedAt.Equal(after.UpdatedAt) || !before.Quota.ObservedAt.Equal(after.Quota.ObservedAt) || len(before.Quota.Signals) != len(after.Quota.Signals) {
		t.Fatalf("auth state changed: before=%+v after=%+v", before, after)
	}
	for key, value := range before.Quota.Signals {
		if after.Quota.Signals[key] != value {
			t.Fatalf("signal %s changed from %q to %q", key, value, after.Quota.Signals[key])
		}
	}
}

func TestManagerQuotaShadowCandidatesApplyTriedAndPriority(t *testing.T) {
	t.Parallel()

	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.executors["claude"] = schedulerTestExecutor{provider: "claude"}
	for _, auth := range []*Auth{
		{ID: "high-a", Provider: "claude", Attributes: map[string]string{"priority": "10"}},
		{ID: "high-b", Provider: "claude", Attributes: map[string]string{"priority": "10"}},
		{ID: "low", Provider: "claude", Attributes: map[string]string{"priority": "0"}},
		{ID: "off", Provider: "claude", Disabled: true, Attributes: map[string]string{"priority": "10"}},
	} {
		if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
			t.Fatalf("Register(%s) error = %v", auth.ID, errRegister)
		}
	}
	tests := []struct {
		name  string
		tried map[string]struct{}
		want  []string
	}{
		{name: "highest tier only", tried: map[string]struct{}{}, want: []string{"high-a", "high-b"}},
		{name: "tried excluded", tried: map[string]struct{}{"high-a": {}}, want: []string{"high-b"}},
		{name: "falls to lower tier", tried: map[string]struct{}{"high-a": {}, "high-b": {}}, want: []string{"low"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidates := manager.quotaShadowCandidates(context.Background(), []string{"claude"}, "", cliproxyexecutor.Options{}, tt.tried, time.Now())
			if len(candidates) != len(tt.want) {
				t.Fatalf("candidates = %+v, want %v", candidates, tt.want)
			}
			for index, candidate := range candidates {
				if candidate.id != tt.want[index] {
					t.Fatalf("candidates[%d] = %q, want %q", index, candidate.id, tt.want[index])
				}
			}
		})
	}
}

func TestManagerQuotaShadowObservationIncludesFreshSignals(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "shadow.jsonl")
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.executors["claude"] = schedulerTestExecutor{provider: "claude"}
	manager.SetConfig(quotaShadowConfig(path))
	t.Cleanup(manager.StopQuotaShadow)
	if _, errRegister := manager.Register(context.Background(), &Auth{ID: "claude-a", Provider: "claude"}); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	ctx := internallogging.WithResponseHeadersHolder(context.Background())
	reset := time.Now().Add(3 * time.Hour).Unix()
	internallogging.SetResponseHeaders(ctx, http.Header{
		"Anthropic-Ratelimit-Unified-5h-Utilization": []string{"0.25"},
		"Anthropic-Ratelimit-Unified-5h-Reset":       []string{strconv.FormatInt(reset, 10)},
	})
	manager.MarkResult(ctx, Result{AuthID: "claude-a", Provider: "claude", Model: "claude-test", Success: true, CompletedResponse: true})
	manager.StopQuotaShadow()

	lines := readQuotaShadowLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("records = %s, want one observation", lines)
	}
	observation := lines[0]
	if got := ojson.GetBytes(observation, "windows.0.name").String(); got != "5h" {
		t.Fatalf("window name = %q, want 5h: %s", got, observation)
	}
	if got := ojson.GetBytes(observation, "windows.0.used").Float(); !approxEqual(got, 0.25) {
		t.Fatalf("window used = %v, want 0.25", got)
	}
	if got := ojson.GetBytes(observation, "windows.0.reset_at_ms").Int(); got != reset*1000 {
		t.Fatalf("window reset_at_ms = %d, want %d", got, reset*1000)
	}
	if got := ojson.GetBytes(observation, "signals.Anthropic-Ratelimit-Unified-5h-Utilization").String(); got != "0.25" {
		t.Fatalf("raw signal = %q, want 0.25: %s", got, observation)
	}
}

func TestManagerQuotaShadowConcurrentPickReloadAndStop(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.executors["claude"] = schedulerTestExecutor{provider: "claude"}
	manager.SetConfig(quotaShadowConfig(filepath.Join(dir, "a.jsonl")))
	for _, id := range []string{"claude-a", "claude-b"} {
		if _, errRegister := manager.Register(context.Background(), &Auth{ID: id, Provider: "claude"}); errRegister != nil {
			t.Fatalf("Register(%s) error = %v", id, errRegister)
		}
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < 50; iteration++ {
				if _, _, _, errPick := manager.pickNextMixed(context.Background(), []string{"claude"}, "", cliproxyexecutor.Options{}, map[string]struct{}{}); errPick != nil {
					t.Errorf("pickNextMixed() error = %v", errPick)
					return
				}
				manager.MarkResult(context.Background(), Result{AuthID: "claude-a", Provider: "claude", Success: true})
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for iteration := 0; iteration < 20; iteration++ {
			if iteration%2 == 0 {
				manager.SetConfig(quotaShadowConfig(filepath.Join(dir, "b.jsonl")))
			} else {
				manager.SetConfig(quotaShadowConfig(filepath.Join(dir, "a.jsonl")))
			}
		}
	}()
	wg.Wait()
	manager.StopQuotaShadow()
	if manager.quotaShadow.Load() != nil {
		t.Fatal("recorder still present after stop")
	}
}
