package main

import (
	"strings"
	"testing"
	"time"
)

func TestRunReplay(t *testing.T) {
	tests := []struct {
		name            string
		file            string
		minSamples      int
		wantRan         bool
		wantCalibration calibrationState
		wantDemand      int
		wantOutcome     [2]int
		wantSkipped     string
		wantNeeded      string
	}{
		{
			name: "capacity-bound pool: every policy serves the same requests", file: "replay.jsonl", minSamples: 3,
			wantRan: true, wantCalibration: calibrationOK, wantDemand: 10, wantOutcome: [2]int{8, 2},
		},
		{
			name: "no exhaustion leaves calibration uninformative", file: "replay_no_outage.jsonl", minSamples: 2,
			wantRan: true, wantCalibration: calibrationUninformative, wantDemand: 6, wantOutcome: [2]int{6, 0},
		},
		{
			name: "too few observations to estimate cost", file: "replay.jsonl", minSamples: 50,
			wantSkipped: "cost per request is unknown", wantNeeded: "More consecutive observations",
		},
		{
			name: "rolling window cannot be replayed", file: "replay_rolling.jsonl", minSamples: 3,
			wantSkipped: "rolling", wantNeeded: "per-request usage timestamps",
		},
		{
			name: "observations without requests", file: "windows.jsonl", minSamples: 3,
			wantSkipped: "no requests recorded", wantNeeded: "request ID",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ds := mustLoad(t, tt.file)
			opts := defaultOptions()
			opts.Replay.MinCostSamples = tt.minSamples
			res := buildReport(ds, opts).Replay
			if res.Ran != tt.wantRan {
				t.Fatalf("Ran = %v, want %v (skipped: %v, excluded: %v)", res.Ran, tt.wantRan, res.Skipped, res.Excluded)
			}
			if !tt.wantRan {
				if !strings.Contains(strings.Join(res.Skipped, "\n"), tt.wantSkipped) {
					t.Errorf("skipped = %v, want a reason containing %q", res.Skipped, tt.wantSkipped)
				}
				if !strings.Contains(strings.Join(res.Needed, "\n"), tt.wantNeeded) {
					t.Errorf("needed = %v, want an item containing %q", res.Needed, tt.wantNeeded)
				}
				return
			}
			if res.Calibration != tt.wantCalibration || res.Demand != tt.wantDemand {
				t.Fatalf("calibration/demand = %d/%d, want %d/%d", res.Calibration, res.Demand, tt.wantCalibration, tt.wantDemand)
			}
			for _, outcome := range append([]policyOutcome{res.Actual}, res.Policies...) {
				if got := [2]int{outcome.Served, outcome.Rejected}; got != tt.wantOutcome {
					t.Errorf("%s served/rejected = %v, want %v", outcome.Name, got, tt.wantOutcome)
				}
			}
			for _, estimate := range res.Costs {
				if estimate.Fallback || estimate.Cost <= 0 {
					t.Errorf("cost estimate %+v, want an observed positive cost", estimate)
				}
			}
		})
	}
}

func TestActualOutcomeCountsRequestsEndingInError(t *testing.T) {
	ds := newDataset()
	ds.Selections = []selection{
		{TsMs: fixtureT0, Pool: "claude", Model: "m", Attempt: 0, Actual: "a"},
		{TsMs: fixtureT0 + 1, Pool: "claude", Model: "m", Attempt: 1, Actual: "b"},
	}
	ds.Errors = []selectionError{
		{TsMs: fixtureT0 + 2, Pool: "claude", Model: "m", Attempt: 2},
		{TsMs: fixtureT0 + 3, Pool: "claude", Model: "m", Attempt: 0},
	}
	demand, _ := collectDemand(ds)
	if len(demand) != 2 {
		t.Fatalf("demand = %d, want 2 (failover picks and late failures are not new requests)", len(demand))
	}
	got := actualOutcome(ds, map[string]bool{routeKey("claude", "m"): true}, len(demand))
	if got.Served != 0 || got.Rejected != 2 || got.ServedBy["a"] != 1 || len(got.ServedBy) != 1 {
		t.Fatalf("served/rejected/servedBy = %d/%d/%v, want 0/2/map[a:1]", got.Served, got.Rejected, got.ServedBy)
	}
}

func TestProviderGateRejectsUnmeasurableCost(t *testing.T) {
	fixed := semantics{Kind: semFixedFirstUse, DriftVerified: true}
	summary := windowTypeSummary{
		typeKey:        typeKey{Provider: "claude", Name: "5h"},
		windowEvidence: windowEvidence{Durations: map[int64]int{18000: 1}},
		Semantics:      fixed,
	}
	tests := []struct {
		name       string
		costDelta  float64
		wantReason string
	}{
		{name: "used share never moved", costDelta: 0, wantReason: "measurable usage increase"},
		{name: "used share decreased", costDelta: -0.1, wantReason: "measurable usage increase"},
		{name: "positive cost", costDelta: 0.3, wantReason: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			series := windowAnalysis{
				seriesKey:      seriesKey{Auth: "a", Provider: "claude", Name: "5h"},
				windowEvidence: windowEvidence{CostPairs: 30, CostDelta: tt.costDelta},
			}
			_, medians := estimateCosts(newDataset(), windowReport{Series: []windowAnalysis{series}}, 20)
			reason := providerGate("claude", []windowTypeSummary{summary}, medians["claude"], 20)
			mismatch := !strings.Contains(reason, tt.wantReason)
			if tt.wantReason == "" {
				mismatch = reason != ""
			}
			if mismatch {
				t.Fatalf("providerGate = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

func TestCalibrate(t *testing.T) {
	tests := []struct {
		name           string
		gap, distance  float64
		simRejected    int
		actualRejected int
		want           calibrationState
	}{
		{name: "rejections differ", gap: 0.03, simRejected: 5, actualRejected: 1, want: calibrationFailed},
		{name: "load spread differs", distance: 0.2, simRejected: 3, actualRejected: 3, want: calibrationFailed},
		{name: "nothing ran out", want: calibrationUninformative},
		{name: "close on both measures", gap: 0.01, distance: 0.05, simRejected: 2, actualRejected: 3, want: calibrationOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := calibrate(tt.gap, tt.distance, tt.simRejected, tt.actualRejected); got != tt.want {
				t.Fatalf("calibrate = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestShareDistance(t *testing.T) {
	tests := []struct {
		name string
		a, b map[string]int
		want float64
	}{
		{name: "same shares", a: map[string]int{"a": 2, "b": 2}, b: map[string]int{"a": 5, "b": 5}, want: 0},
		{name: "disjoint", a: map[string]int{"a": 1}, b: map[string]int{"b": 1}, want: 1},
		{name: "one side empty", a: map[string]int{}, b: map[string]int{"a": 1}, want: 1},
		{name: "both empty", a: map[string]int{}, b: map[string]int{}, want: 0},
		{name: "partial overlap", a: map[string]int{"a": 3, "b": 1}, b: map[string]int{"a": 1, "b": 3}, want: 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shareDistance(tt.a, tt.b); !almostEqual(got, tt.want) {
				t.Fatalf("shareDistance = %.3f, want %.3f", got, tt.want)
			}
		})
	}
}

func TestSimulationPick(t *testing.T) {
	now := fixtureT0
	hour := time.Hour.Milliseconds()
	week := 7 * 24 * hour
	short := func(used float64, resetMs int64) *simWindow {
		return &simWindow{name: "5h", durMs: 5 * hour, resetMs: resetMs, used: used}
	}
	long := func(used float64, resetMs int64) *simWindow {
		return &simWindow{name: "7d", durMs: week, long: true, resetMs: resetMs, used: used}
	}
	tests := []struct {
		name     string
		creds    []*simCred
		lastPick string
		want     map[string]string
	}{
		{
			name: "shadow spends weekly quota that would otherwise perish",
			creds: []*simCred{
				{id: "a-fresh", windows: []*simWindow{short(0, 0), long(0, 0)}},
				{id: "b-perishing", windows: []*simWindow{short(0, 0), long(0.2, now+hour)}},
			},
			want: map[string]string{policyFillFirst: "a-fresh", policyRoundRobin: "a-fresh", policyShadow: "b-perishing"},
		},
		{
			name: "shadow avoids a soft-limited credential",
			creds: []*simCred{
				{id: "a", windows: []*simWindow{short(0.95, now+hour)}},
				{id: "b", windows: []*simWindow{short(0.5, now+hour)}},
			},
			want: map[string]string{policyFillFirst: "a", policyShadow: "b"},
		},
		{
			name: "shadow prefers an idle short window",
			creds: []*simCred{
				{id: "a", windows: []*simWindow{short(0.3, now+4*hour)}},
				{id: "b", windows: []*simWindow{short(0, 0)}},
			},
			want: map[string]string{policyFillFirst: "a", policyShadow: "b"},
		},
		{
			name: "round-robin resumes after the last pick and wraps",
			creds: []*simCred{
				{id: "a", windows: []*simWindow{short(0, 0)}},
				{id: "b", windows: []*simWindow{short(0, 0)}},
			},
			lastPick: "b",
			want:     map[string]string{policyRoundRobin: "a"},
		},
		{
			name: "exhausted credentials are skipped",
			creds: []*simCred{
				{id: "a", windows: []*simWindow{short(1, now+hour)}},
				{id: "b", windows: []*simWindow{short(0.4, now+hour)}},
			},
			want: map[string]string{policyFillFirst: "b", policyRoundRobin: "b", policyShadow: "b"},
		},
		{
			name: "only the highest available priority tier is used",
			creds: []*simCred{
				{id: "a", windows: []*simWindow{short(0, 0)}},
				{id: "b", priority: 1, windows: []*simWindow{short(0.9, now+hour)}},
			},
			want: map[string]string{policyFillFirst: "b", policyRoundRobin: "b", policyShadow: "b"},
		},
		{
			name:  "no available credential",
			creds: []*simCred{{id: "a", windows: []*simWindow{short(1, now+hour)}}},
			want:  map[string]string{policyFillFirst: "", policyRoundRobin: "", policyShadow: ""},
		},
	}
	for _, tt := range tests {
		for policy, want := range tt.want {
			t.Run(tt.name+"/"+policy, func(t *testing.T) {
				sim := &simulation{
					policy:    policy,
					softLimit: 0.9,
					creds:     make(map[string]*simCred),
					eligible:  map[string][]string{"route": nil},
					lastPick:  map[string]string{"route": tt.lastPick},
				}
				for _, cred := range tt.creds {
					sim.creds[cred.id] = cloneSimCred(cred)
					sim.eligible["route"] = append(sim.eligible["route"], cred.id)
				}
				picked := sim.pick(demandEvent{TsMs: now, Route: "route"}, nil)
				got := ""
				if picked != nil {
					got = picked.id
				}
				if got != want {
					t.Fatalf("pick = %q, want %q", got, want)
				}
			})
		}
	}
}

func TestSimCredAdvance(t *testing.T) {
	hour := time.Hour.Milliseconds()
	tests := []struct {
		name          string
		window        simWindow
		advanceTo     int64
		wantResetMs   int64
		wantPerished  float64
		wantResets    int
		wantAvailable bool
	}{
		{
			name:          "first-use window stops at reset",
			window:        simWindow{durMs: 5 * hour, resetMs: 5 * hour, used: 1, cost: 0.1},
			advanceTo:     6 * hour,
			wantResetMs:   0,
			wantPerished:  0,
			wantResets:    1,
			wantAvailable: true,
		},
		{
			name:          "anchored window perishes whole missed cycles",
			window:        simWindow{durMs: 5 * hour, anchored: true, resetMs: 5 * hour, used: 0.4},
			advanceTo:     16 * hour,
			wantResetMs:   20 * hour,
			wantPerished:  0.6 + 1 + 1,
			wantResets:    3,
			wantAvailable: true,
		},
		{
			name:          "running exhausted window stays unavailable",
			window:        simWindow{durMs: 5 * hour, resetMs: 5 * hour, used: 1},
			advanceTo:     4 * hour,
			wantResetMs:   5 * hour,
			wantAvailable: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := tt.window
			cred := &simCred{id: "a", windows: []*simWindow{&w}}
			var out policyOutcome
			cred.advance(tt.advanceTo, &out)
			if w.resetMs != tt.wantResetMs || out.ResetsShort != tt.wantResets || !almostEqual(out.PerishedShort, tt.wantPerished) {
				t.Fatalf("reset/resets/perished = %d/%d/%.2f, want %d/%d/%.2f", w.resetMs, out.ResetsShort, out.PerishedShort, tt.wantResetMs, tt.wantResets, tt.wantPerished)
			}
			if cred.available() != tt.wantAvailable {
				t.Fatalf("available = %v, want %v", cred.available(), tt.wantAvailable)
			}
		})
	}
}

func TestSimCredServeStartsFirstUseWindow(t *testing.T) {
	hour := time.Hour.Milliseconds()
	w := &simWindow{durMs: 5 * hour, cost: 0.3}
	cred := &simCred{id: "a", windows: []*simWindow{w}}
	cred.serve(2 * hour)
	cred.serve(3 * hour)
	if w.resetMs != 7*hour || !almostEqual(w.used, 0.6) {
		t.Fatalf("reset/used = %d/%.2f, want %d/0.60", w.resetMs, w.used, 7*hour)
	}
}

func cloneSimCred(cred *simCred) *simCred {
	clone := &simCred{id: cred.id, priority: cred.priority}
	for _, w := range cred.windows {
		copied := *w
		clone.windows = append(clone.windows, &copied)
	}
	return clone
}
