package main

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fixtureT0 is the first timestamp used by the testdata fixtures.
const fixtureT0 = int64(1762675213000)

func fixture(name string) string {
	return filepath.Join("testdata", name)
}

func mustLoad(t *testing.T, names ...string) *dataset {
	t.Helper()
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, fixture(name))
	}
	ds, errLoad := loadDataset(paths)
	if errLoad != nil {
		t.Fatalf("loadDataset(%v): %v", names, errLoad)
	}
	return ds
}

func defaultOptions() options {
	return options{
		TolMs:    (5 * time.Minute).Milliseconds(),
		Timeline: true,
		Replay:   replayOptions{Strategy: policyRoundRobin, SoftLimit: 0.9, MinCostSamples: 3},
	}
}

func TestResolveInputs(t *testing.T) {
	rotatedMain := fixture("rotated/quota-shadow.jsonl")
	missing := filepath.Join(t.TempDir(), "quota-shadow.jsonl")
	tests := []struct {
		name        string
		file        string
		args        []string
		withRotated bool
		want        []string
	}{
		{name: "explicit files keep order", file: rotatedMain, args: []string{"b.jsonl", "a.jsonl"}, withRotated: true, want: []string{"b.jsonl", "a.jsonl"}},
		{name: "rotation read first", file: rotatedMain, withRotated: true, want: []string{rotatedMain + ".1", rotatedMain}},
		{name: "rotation disabled", file: rotatedMain, withRotated: false, want: []string{rotatedMain}},
		{name: "absent rotation skipped", file: missing, withRotated: true, want: []string{missing}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errResolve := resolveInputs(tt.file, tt.args, tt.withRotated)
			if errResolve != nil {
				t.Fatalf("resolveInputs: %v", errResolve)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("resolveInputs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadDataset(t *testing.T) {
	tests := []struct {
		name          string
		files         []string
		wantLines     int
		wantMalformed int
		wantUnknown   int
		wantSel       int
		wantFirst     int64
		wantLast      int64
	}{
		{
			name:          "malformed and unknown lines are counted and skipped",
			files:         []string{"agreement.jsonl"},
			wantLines:     8,
			wantMalformed: 1,
			wantUnknown:   1,
			wantSel:       6,
			wantFirst:     fixtureT0,
			wantLast:      fixtureT0 + 5*time.Minute.Milliseconds(),
		},
		{
			name:      "rotation file then current file",
			files:     []string{"rotated/quota-shadow.jsonl.1", "rotated/quota-shadow.jsonl"},
			wantLines: 2,
			wantSel:   2,
			wantFirst: fixtureT0,
			wantLast:  fixtureT0 + time.Hour.Milliseconds(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ds := mustLoad(t, tt.files...)
			got := []any{ds.Lines, ds.Malformed, ds.Unknown, len(ds.Selections), ds.FirstMs, ds.LastMs}
			want := []any{tt.wantLines, tt.wantMalformed, tt.wantUnknown, tt.wantSel, tt.wantFirst, tt.wantLast}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("lines/malformed/unknown/selections/first/last = %v, want %v", got, want)
			}
		})
	}
}

func TestRun(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantCode     int
		wantStdout   []string
		wantStderr   string
		absentStdout []string
	}{
		{
			name:     "full report",
			args:     []string{"-min-cost-samples", "3", fixture("replay.jsonl")},
			wantCode: 0,
			wantStdout: []string{
				"1. Agreement between actual and shadow picks",
				"2. Soft-limited real picks",
				"3. Outages (selection_error)",
				"4. Window semantics",
				"5. Utilization and perished quota",
				"6. Replay: round-robin vs fill-first vs shadow",
				"Calibration, simulated round-robin against the log: rejected requests differ by 0.0%",
				"  OK: differences between policies are indicative, not exact.",
				"peak_used",
			},
		},
		{
			name:         "timeline off",
			args:         []string{"-timeline=false", fixture("replay.jsonl")},
			wantCode:     0,
			wantStdout:   []string{"closed windows 0"},
			absentStdout: []string{"peak_used"},
		},
		{
			name:       "rotation read through -file",
			args:       []string{"-file", fixture("rotated/quota-shadow.jsonl")},
			wantCode:   0,
			wantStdout: []string{"quota-shadow.jsonl.1, " + fixture("rotated/quota-shadow.jsonl")},
		},
		{name: "unknown strategy", args: []string{"-strategy", "weighted", fixture("replay.jsonl")}, wantCode: 2, wantStderr: "-strategy must be"},
		{name: "missing file", args: []string{filepath.Join(t.TempDir(), "absent.jsonl")}, wantCode: 1, wantStderr: "absent.jsonl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code != tt.wantCode {
				t.Fatalf("run exit = %d, want %d; stderr: %s", code, tt.wantCode, stderr.String())
			}
			for _, want := range tt.wantStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout missing %q:\n%s", want, stdout.String())
				}
			}
			for _, absent := range tt.absentStdout {
				if strings.Contains(stdout.String(), absent) {
					t.Errorf("stdout unexpectedly contains %q", absent)
				}
			}
			if tt.wantStderr != "" && !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
