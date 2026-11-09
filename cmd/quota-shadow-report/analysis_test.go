package main

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func TestAnalyzeAgreement(t *testing.T) {
	stats := analyzeAgreement(mustLoad(t, "agreement.jsonl").Selections)
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"selections", stats.Selections, 6},
		{"in pool", stats.InPool, ratio{Hit: 2, Total: 4}},
		{"out of pool", stats.OutOfPool, 2},
		{"no candidates", stats.NoCandidates, 1},
		{"first attempt", stats.FirstAttempt, ratio{Hit: 2, Total: 3}},
		{"failover", stats.Failover, ratio{Hit: 0, Total: 1}},
		{"soft-limited actual", stats.SoftLimited, ratio{Hit: 2, Total: 4}},
		{"soft-limited with alternative", stats.SoftAvoidable, 1},
		{"soft unknown", stats.SoftUnknown, 0},
		{"by reason in ranking order", stats.ByReason, []reasonAgreement{
			{Reason: "soft_limit", ratio: ratio{Hit: 0, Total: 1}},
			{Reason: "perish_long", ratio: ratio{Hit: 1, Total: 1}},
			{Reason: "short_idle", ratio: ratio{Hit: 0, Total: 1}},
			{Reason: "only_candidate", ratio: ratio{Hit: 1, Total: 1}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !reflect.DeepEqual(tt.got, tt.want) {
				t.Fatalf("got %+v, want %+v", tt.got, tt.want)
			}
		})
	}
}

func TestAnalyzeOutages(t *testing.T) {
	ds := mustLoad(t, "outages.jsonl")
	stats := analyzeOutages(ds.Selections, ds.Errors)
	minute := time.Minute.Milliseconds()
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"totals", stats.outageTotals, outageTotals{Errors: 4, Episodes: 2, Open: 1, TotalGapMs: 39 * minute, MaxGapMs: 30 * minute}},
		{"first attempt errors", stats.FirstAttemptErrors, 3},
		{"routes by total gap", stats.Routes, []routeOutage{
			{Route: "codex gpt-5", Errors: 2, Episodes: 1, Open: 1, TotalGapMs: 30 * minute, MaxGapMs: 30 * minute},
			{Route: "claude claude-sonnet-4-5", Errors: 2, Episodes: 1, TotalGapMs: 9 * minute, MaxGapMs: 9 * minute},
		}},
		{"statuses", stats.Statuses, []countEntry{{"429", 3}, {"503", 1}}},
		{"retry-after summary", []int64{int64(stats.RetryAfter.Count), stats.RetryAfter.Min, stats.RetryAfter.P50, stats.RetryAfter.P90, stats.RetryAfter.Max},
			[]int64{3, minute, 5 * minute, 180 * minute, 180 * minute}},
		{"retry-after buckets", stats.RetryAfter.Buckets, []countEntry{
			{"< 1m", 0}, {"1m-5m", 1}, {"5m-30m", 1}, {"30m-1h", 0}, {"1h-5h", 1}, {"5h-24h", 0}, {">= 24h", 0},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !reflect.DeepEqual(tt.got, tt.want) {
				t.Fatalf("got %+v, want %+v", tt.got, tt.want)
			}
		})
	}
}

func TestAnalyzeWindowsPerCredential(t *testing.T) {
	ds := mustLoad(t, "windows.jsonl")
	report := analyzeWindows(ds.Observations, defaultOptions().TolMs, ds.LastMs)
	bySeries := make(map[string]windowAnalysis)
	for _, series := range report.Series {
		bySeries[series.Auth+" "+windowLabel(series.Scope, series.Name)] = series
	}
	hour := time.Hour.Milliseconds()
	tests := []struct {
		series        string
		wantKind      semanticsKind
		wantPoints    int
		wantPairs     [3]int
		wantBoundary  boundaryCounts
		wantClosed    int
		wantPerished  float64
		wantIdleMs    []int64
		wantCostPairs int
		wantCost      float64
	}{
		{
			series: "claude-a 5h", wantKind: semFixedFirstUse, wantPoints: 6, wantPairs: [3]int{4, 0, 0},
			wantBoundary: boundaryCounts{FirstUse: 1}, wantClosed: 1, wantPerished: 0.5,
			wantIdleMs: []int64{2 * hour}, wantCostPairs: 4, wantCost: 0.55,
		},
		{
			series: "claude-c 5h", wantKind: semFixedAnchored, wantPoints: 4, wantPairs: [3]int{2, 0, 0},
			wantBoundary: boundaryCounts{Anchored: 1}, wantClosed: 1, wantPerished: 0.3,
			wantIdleMs: []int64{2 * hour}, wantCostPairs: 2, wantCost: 0.2,
		},
		{
			series: "claude-d 5h", wantKind: semFixedUndetermined, wantPoints: 4, wantPairs: [3]int{2, 0, 0},
			wantBoundary: boundaryCounts{External: 1}, wantClosed: 1, wantPerished: 0.5,
			wantIdleMs: []int64{2 * hour}, wantCostPairs: 2, wantCost: 0.2,
		},
		{
			series: "codex-b primary", wantKind: semRolling, wantPoints: 7, wantPairs: [3]int{0, 6, 0},
			wantClosed: 1, wantPerished: 0.4, wantCostPairs: 6, wantCost: 0.6,
		},
	}
	for _, tt := range tests {
		t.Run(tt.series, func(t *testing.T) {
			got, ok := bySeries[tt.series]
			if !ok {
				t.Fatalf("series %q not found", tt.series)
			}
			if sem := got.semantics(); sem.Kind != tt.wantKind || !sem.DriftVerified {
				t.Errorf("semantics = %v (kind %d, verified %v), want kind %d verified", sem, sem.Kind, sem.DriftVerified, tt.wantKind)
			}
			if got.Points != tt.wantPoints {
				t.Errorf("points = %d, want %d", got.Points, tt.wantPoints)
			}
			if pairs := [3]int{got.Same, got.Forward, got.Stale}; pairs != tt.wantPairs {
				t.Errorf("same/fwd/stale = %v, want %v", pairs, tt.wantPairs)
			}
			if got.Boundaries != tt.wantBoundary {
				t.Errorf("boundaries = %+v, want %+v", got.Boundaries, tt.wantBoundary)
			}
			closed, perished := got.perished()
			if closed != tt.wantClosed || !almostEqual(perished, tt.wantPerished) {
				t.Errorf("closed/perished = %d/%.4f, want %d/%.4f", closed, perished, tt.wantClosed, tt.wantPerished)
			}
			if !reflect.DeepEqual(got.IdleMs, tt.wantIdleMs) {
				t.Errorf("idle = %v, want %v", got.IdleMs, tt.wantIdleMs)
			}
			if got.CostPairs != tt.wantCostPairs || !almostEqual(got.CostDelta, tt.wantCost) {
				t.Errorf("cost pairs/delta = %d/%.4f, want %d/%.4f", got.CostPairs, got.CostDelta, tt.wantCostPairs, tt.wantCost)
			}
		})
	}
}

func TestAnalyzeWindowsEdgeCases(t *testing.T) {
	ds := mustLoad(t, "windows_edge.jsonl")
	report := analyzeWindows(ds.Observations, defaultOptions().TolMs, ds.LastMs)
	bySeries := make(map[string]windowAnalysis)
	for _, series := range report.Series {
		bySeries[series.Auth] = series
	}
	hour := time.Hour.Milliseconds()
	tests := []struct {
		auth        string
		wantMissing int
		wantStale   int
		wantSame    int
		wantTolMs   int64
		wantSpacing []int64
	}{
		// A point without a reset must not hide the boundary that follows it.
		{auth: "claude-e", wantMissing: 1, wantSame: 2, wantTolMs: 5 * time.Minute.Milliseconds(), wantSpacing: []int64{6 * hour}},
		// A late response still showing the previous window is skipped, not a second boundary.
		{auth: "claude-f", wantStale: 1, wantSame: 2, wantTolMs: 5 * time.Minute.Milliseconds(), wantSpacing: []int64{6 * hour}},
		// Hour-rounded resets widen the tolerance to an hour.
		{auth: "claude-g", wantSame: 2, wantTolMs: hour, wantSpacing: []int64{7 * hour}},
		// The first observation of a window may trail its start by a long request.
		{auth: "claude-h", wantSame: 2, wantTolMs: 5 * time.Minute.Milliseconds(), wantSpacing: []int64{7 * hour}},
	}
	for _, tt := range tests {
		t.Run(tt.auth, func(t *testing.T) {
			got, ok := bySeries[tt.auth]
			if !ok {
				t.Fatalf("series %q not found", tt.auth)
			}
			if sem := got.semantics(); sem.Kind != semFixedFirstUse || !sem.DriftVerified {
				t.Errorf("semantics = %v, want fixed, starts on first use (verified)", sem)
			}
			if got.Boundaries != (boundaryCounts{FirstUse: 1}) {
				t.Errorf("boundaries = %+v, want one first-use boundary", got.Boundaries)
			}
			counts := []int{got.MissingReset, got.Stale, got.Same, got.Forward}
			if want := []int{tt.wantMissing, tt.wantStale, tt.wantSame, 0}; !reflect.DeepEqual(counts, want) {
				t.Errorf("missing/stale/same/forward = %v, want %v", counts, want)
			}
			if got.TolMs != tt.wantTolMs || !reflect.DeepEqual(got.SpacingMs, tt.wantSpacing) {
				t.Errorf("tolerance/spacing = %d/%v, want %d/%v", got.TolMs, got.SpacingMs, tt.wantTolMs, tt.wantSpacing)
			}
			if closed, _ := got.perished(); closed != 1 {
				t.Errorf("closed windows = %d, want 1", closed)
			}
		})
	}
}

func TestAnalyzeWindowsByType(t *testing.T) {
	ds := mustLoad(t, "windows.jsonl")
	report := analyzeWindows(ds.Observations, defaultOptions().TolMs, ds.LastMs)
	tests := []struct {
		key          typeKey
		wantCreds    int
		wantKind     semanticsKind
		wantClosed   int
		wantPerished float64
	}{
		// One first-use and one anchored boundary leave the start rule mixed.
		{typeKey{Provider: "claude", Name: "5h"}, 3, semFixedMixed, 3, 1.3},
		// Rolling windows have no fixed reset, so nothing counts as perished.
		{typeKey{Provider: "codex", Name: "primary"}, 1, semRolling, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.key.Provider+"/"+tt.key.Name, func(t *testing.T) {
			var got *windowTypeSummary
			for index := range report.Types {
				if report.Types[index].typeKey == tt.key {
					got = &report.Types[index]
				}
			}
			if got == nil {
				t.Fatalf("type %+v not found", tt.key)
			}
			if got.Credentials != tt.wantCreds || got.Semantics.Kind != tt.wantKind || got.Closed != tt.wantClosed || !almostEqual(got.Perished, tt.wantPerished) {
				t.Fatalf("creds/kind/closed/perished = %d/%d/%d/%.2f, want %d/%d/%d/%.2f",
					got.Credentials, got.Semantics.Kind, got.Closed, got.Perished, tt.wantCreds, tt.wantKind, tt.wantClosed, tt.wantPerished)
			}
			if report.TypeSemantics[tt.key] != got.Semantics {
				t.Fatalf("TypeSemantics = %v, want %v", report.TypeSemantics[tt.key], got.Semantics)
			}
		})
	}
}

func TestSemanticsVerdict(t *testing.T) {
	hour := time.Hour.Milliseconds()
	tests := []struct {
		name         string
		evidence     windowEvidence
		wantKind     semanticsKind
		wantVerified bool
	}{
		{name: "single point", evidence: windowEvidence{Points: 1}, wantKind: semInsufficient},
		{name: "short observation without boundaries", evidence: windowEvidence{Points: 3, ElapsedMs: hour / 4}, wantKind: semInsufficient},
		{name: "short observation with a boundary", evidence: windowEvidence{Points: 3, ElapsedMs: hour / 4, Boundaries: boundaryCounts{FirstUse: 1}}, wantKind: semFixedFirstUse},
		{name: "drift between thresholds", evidence: windowEvidence{Points: 5, ElapsedMs: hour, DriftMs: hour / 5}, wantKind: semUnclear, wantVerified: true},
		{name: "rolling", evidence: windowEvidence{Points: 5, ElapsedMs: hour, DriftMs: hour}, wantKind: semRolling, wantVerified: true},
		{name: "fixed without idle boundaries", evidence: windowEvidence{Points: 5, ElapsedMs: hour, Boundaries: boundaryCounts{Contiguous: 4}}, wantKind: semFixedUndetermined, wantVerified: true},
		{name: "anchored majority", evidence: windowEvidence{Points: 5, ElapsedMs: hour, Boundaries: boundaryCounts{FirstUse: 1, Anchored: 3}}, wantKind: semFixedAnchored, wantVerified: true},
		{name: "mixed start rule", evidence: windowEvidence{Points: 5, ElapsedMs: hour, Boundaries: boundaryCounts{FirstUse: 2, Anchored: 2}}, wantKind: semFixedMixed, wantVerified: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.evidence.semantics()
			if got.Kind != tt.wantKind || got.DriftVerified != tt.wantVerified {
				t.Fatalf("semantics = %+v, want kind %d verified %v", got, tt.wantKind, tt.wantVerified)
			}
		})
	}
}

func TestResetGranularity(t *testing.T) {
	tests := []struct {
		name   string
		resets []int64
		want   int64
	}{
		{name: "hour aligned", resets: []int64{3 * time.Hour.Milliseconds(), 7 * time.Hour.Milliseconds()}, want: time.Hour.Milliseconds()},
		{name: "minute aligned", resets: []int64{time.Hour.Milliseconds(), 61 * time.Minute.Milliseconds()}, want: time.Minute.Milliseconds()},
		{name: "second precision", resets: []int64{fixtureT0}, want: time.Second.Milliseconds()},
		{name: "millisecond precision", resets: []int64{fixtureT0 + 7}, want: 0},
		{name: "no resets", resets: []int64{0}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			points := make([]windowPoint, 0, len(tt.resets))
			for _, reset := range tt.resets {
				points = append(points, windowPoint{ResetAtMs: reset})
			}
			if got := resetGranularity(points); got != tt.want {
				t.Fatalf("resetGranularity = %d, want %d", got, tt.want)
			}
		})
	}
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}
