package auth

import (
	"math"
	"strconv"
	"testing"
	"time"
)

func findQuotaWindow(t *testing.T, windows []QuotaWindow, scope, name string) QuotaWindow {
	t.Helper()
	for _, window := range windows {
		if window.Scope == scope && window.Name == name {
			return window
		}
	}
	t.Fatalf("window scope=%q name=%q not found in %#v", scope, name, windows)
	return QuotaWindow{}
}

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestParseQuotaWindowsClaude(t *testing.T) {
	t.Parallel()

	reset5h := time.Unix(1787000000, 0)
	reset7d := time.Unix(1787300000, 0)
	windows := ParseQuotaWindows("claude", QuotaState{Signals: map[string]string{
		"Anthropic-Ratelimit-Unified-Status":               "rejected",
		"Anthropic-Ratelimit-Unified-Reset":                "1787000000",
		"Anthropic-Ratelimit-Unified-5h-Utilization":       "0.42",
		"Anthropic-Ratelimit-Unified-5h-Reset":             strconv.FormatInt(reset5h.Unix(), 10),
		"Anthropic-Ratelimit-Unified-5h-Status":            "allowed",
		"Anthropic-Ratelimit-Unified-7d-Utilization":       "0.8",
		"Anthropic-Ratelimit-Unified-7d-Reset":             strconv.FormatInt(reset7d.Unix(), 10),
		"Anthropic-Ratelimit-Unified-7d_oi-Utilization":    "0.1",
		"Anthropic-Ratelimit-Unified-Overage-Status":       "rejected",
		"Anthropic-Ratelimit-Unified-Representative-Claim": "five_hour",
	}})

	if len(windows) != 3 {
		t.Fatalf("len(windows) = %d, want 3: %#v", len(windows), windows)
	}
	short := findQuotaWindow(t, windows, "", "5h")
	if !short.HasUsed || !approxEqual(short.UsedFraction, 0.42) || !short.ResetAt.Equal(reset5h) || short.Duration != 5*time.Hour || short.Status != "allowed" {
		t.Fatalf("5h window = %#v", short)
	}
	long := findQuotaWindow(t, windows, "", "7d")
	if !approxEqual(long.UsedFraction, 0.8) || !long.ResetAt.Equal(reset7d) || long.Duration != 7*24*time.Hour {
		t.Fatalf("7d window = %#v", long)
	}
	scoped := findQuotaWindow(t, windows, "7d_oi", "7d_oi")
	if !approxEqual(scoped.UsedFraction, 0.1) || scoped.Duration != 7*24*time.Hour {
		t.Fatalf("7d_oi window = %#v", scoped)
	}
}

func TestParseQuotaWindowsCodex(t *testing.T) {
	t.Parallel()

	observedAt := time.Unix(1787279282, 0)
	windows := ParseQuotaWindows("codex", QuotaState{ObservedAt: observedAt, Signals: map[string]string{
		"X-Codex-Plan-Type":                             "pro",
		"X-Codex-Limit-Reached":                         "true",
		"X-Codex-Primary-Used-Percent":                  "51",
		"X-Codex-Primary-Window-Minutes":                "300",
		"X-Codex-Primary-Reset-After-Seconds":           "600",
		"X-Codex-Primary-Over-Secondary-Limit-Percent":  "0",
		"X-Codex-Secondary-Used-Percent":                "20",
		"X-Codex-Secondary-Window-Minutes":              "10080",
		"X-Codex-Secondary-Reset-At":                    "1787588999",
		"X-Codex-Bengalfox-Limit-Name":                  "GPT-5.3-Codex-Spark",
		"X-Codex-Bengalfox-Secondary-Used-Percent":      "35",
		"X-Codex-Additional-Spark-Primary-Used-Percent": "7",
		"X-Codex-Credits-Has-Credits":                   "False",
	}})

	if len(windows) != 4 {
		t.Fatalf("len(windows) = %d, want 4: %#v", len(windows), windows)
	}
	primary := findQuotaWindow(t, windows, "", "primary")
	if !approxEqual(primary.UsedFraction, 0.51) || primary.Duration != 5*time.Hour || !primary.ResetAt.Equal(observedAt.Add(600*time.Second)) || primary.Status != "limit_reached" {
		t.Fatalf("primary window = %#v", primary)
	}
	secondary := findQuotaWindow(t, windows, "", "secondary")
	if !approxEqual(secondary.UsedFraction, 0.2) || secondary.Duration != 7*24*time.Hour || !secondary.ResetAt.Equal(time.Unix(1787588999, 0)) {
		t.Fatalf("secondary window = %#v", secondary)
	}
	scoped := findQuotaWindow(t, windows, "bengalfox", "secondary")
	if !approxEqual(scoped.UsedFraction, 0.35) || scoped.Label != "GPT-5.3-Codex-Spark" || scoped.Status != "" {
		t.Fatalf("bengalfox window = %#v", scoped)
	}
	websocketScoped := findQuotaWindow(t, windows, "spark", "primary")
	if !approxEqual(websocketScoped.UsedFraction, 0.07) {
		t.Fatalf("websocket additional window = %#v", websocketScoped)
	}
}

func TestParseQuotaWindowsDevin(t *testing.T) {
	t.Parallel()

	windows := ParseQuotaWindows("devin", QuotaState{Signals: map[string]string{
		"plan":                           "pro",
		"daily_quota_remaining_percent":  "30%",
		"daily_quota_reset_at":           "2026-11-10T00:00:00Z",
		"weekly_quota_remaining_percent": "90%",
	}})
	daily := findQuotaWindow(t, windows, "", "daily")
	if !approxEqual(daily.UsedFraction, 0.7) || daily.Duration != 24*time.Hour || daily.ResetAt.IsZero() {
		t.Fatalf("daily window = %#v", daily)
	}
	weekly := findQuotaWindow(t, windows, "", "weekly")
	if !approxEqual(weekly.UsedFraction, 0.1) || !weekly.ResetAt.IsZero() {
		t.Fatalf("weekly window = %#v", weekly)
	}
}

func TestParseQuotaWindowsUnsupportedProvider(t *testing.T) {
	t.Parallel()

	if windows := ParseQuotaWindows("gemini", QuotaState{Signals: map[string]string{"X-Codex-Primary-Used-Percent": "5"}}); windows != nil {
		t.Fatalf("windows = %#v, want nil", windows)
	}
}

func TestQuotaWindowRemainingAndPerish(t *testing.T) {
	t.Parallel()

	now := time.Unix(1787000000, 0)
	tests := []struct {
		name          string
		window        QuotaWindow
		wantRemaining float64
		wantPerish    float64
		wantActive    bool
	}{
		{
			name:          "underused window close to reset perishes",
			window:        QuotaWindow{UsedFraction: 0.2, HasUsed: true, ResetAt: now.Add(time.Hour), Duration: 5 * time.Hour},
			wantRemaining: 0.8,
			wantPerish:    0.6,
			wantActive:    true,
		},
		{
			name:          "fresh window does not perish",
			window:        QuotaWindow{UsedFraction: 0.1, HasUsed: true, ResetAt: now.Add(5 * time.Hour), Duration: 5 * time.Hour},
			wantRemaining: 0.9,
			wantPerish:    0,
			wantActive:    true,
		},
		{
			name:          "expired window is replenished",
			window:        QuotaWindow{UsedFraction: 1, HasUsed: true, ResetAt: now.Add(-time.Minute), Duration: 5 * time.Hour},
			wantRemaining: 1,
			wantPerish:    0,
		},
		{
			name:          "unknown duration never perishes",
			window:        QuotaWindow{UsedFraction: 0, HasUsed: true, ResetAt: now.Add(time.Minute)},
			wantRemaining: 1,
			wantPerish:    0,
		},
		{
			name:          "reset beyond nominal length is capped",
			window:        QuotaWindow{UsedFraction: 0, HasUsed: true, ResetAt: now.Add(10 * time.Hour), Duration: 5 * time.Hour},
			wantRemaining: 1,
			wantPerish:    0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.window.Remaining(now); !approxEqual(got, tt.wantRemaining) {
				t.Fatalf("Remaining() = %v, want %v", got, tt.wantRemaining)
			}
			if got := tt.window.PerishAtFullPace(now); !approxEqual(got, tt.wantPerish) {
				t.Fatalf("PerishAtFullPace() = %v, want %v", got, tt.wantPerish)
			}
			if got := tt.window.Active(now); got != tt.wantActive {
				t.Fatalf("Active() = %v, want %v", got, tt.wantActive)
			}
		})
	}
}

func TestParseQuotaTimestamp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw    string
		want   time.Time
		wantOK bool
	}{
		{raw: "1787000000", want: time.Unix(1787000000, 0), wantOK: true},
		{raw: "1787000000.5", want: time.Unix(1787000000, 500000000), wantOK: true},
		{raw: "1787000000000", want: time.Unix(1787000000, 0), wantOK: true},
		{raw: "2026-11-10T00:00:00Z", want: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), wantOK: true},
		{raw: "0"},
		{raw: "-5"},
		{raw: "1e300"},
		{raw: "soon"},
		{raw: ""},
	}
	for _, tt := range tests {
		got, ok := parseQuotaTimestamp(tt.raw)
		if ok != tt.wantOK || (ok && !got.Equal(tt.want)) {
			t.Fatalf("parseQuotaTimestamp(%q) = (%v, %v), want (%v, %v)", tt.raw, got, ok, tt.want, tt.wantOK)
		}
	}
}
