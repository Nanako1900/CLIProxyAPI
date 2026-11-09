package auth

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// QuotaWindow is one provider usage window decoded from a passive quota
// observation snapshot (QuotaState.Signals).
type QuotaWindow struct {
	// Name identifies the window within its scope, e.g. "5h", "7d", "primary", "daily".
	Name string
	// Scope is empty for credential-wide windows. Codex additional limits and
	// model-specific Claude windows carry their limit namespace here.
	Scope string
	// Label is a human-readable limit name when the provider supplies one.
	Label string
	// UsedFraction is the consumed share of the window (1.0 means exhausted).
	UsedFraction float64
	// HasUsed reports whether UsedFraction was observed.
	HasUsed bool
	// ResetAt is when the window resets. Zero when unknown.
	ResetAt time.Time
	// Duration is the nominal window length. Zero when unknown.
	Duration time.Duration
	// Status is the provider status string when supplied (e.g. "allowed", "rejected").
	Status string
}

// Remaining returns the unconsumed share of the window at now. A window whose
// reset time has passed is treated as fully replenished, and a window the
// provider explicitly rejected is treated as exhausted.
func (w QuotaWindow) Remaining(now time.Time) float64 {
	if w.Expired(now) {
		return 1
	}
	if w.Status == "rejected" {
		return 0
	}
	if !w.HasUsed {
		return 1
	}
	return clampUnit(1 - w.UsedFraction)
}

// Expired reports whether the observed window has already reset by now.
func (w QuotaWindow) Expired(now time.Time) bool {
	return !w.ResetAt.IsZero() && !w.ResetAt.After(now)
}

// Active reports whether the window is running with recorded consumption.
func (w QuotaWindow) Active(now time.Time) bool {
	return !w.Expired(now) && !w.ResetAt.IsZero() && w.HasUsed && w.UsedFraction > 0
}

// PerishAtFullPace estimates the share of the window that will expire unused
// even if the credential is consumed at the nominal full pace (one full window
// per Duration) from now until the reset. Positive values mark quota that is
// free to spend now because it is lost otherwise. Unknown durations yield 0.
func (w QuotaWindow) PerishAtFullPace(now time.Time) float64 {
	if w.Duration <= 0 || w.ResetAt.IsZero() || w.Expired(now) {
		return 0
	}
	untilReset := w.ResetAt.Sub(now)
	if untilReset > w.Duration {
		untilReset = w.Duration
	}
	consumable := float64(untilReset) / float64(w.Duration)
	return clampUnit(w.Remaining(now) - consumable)
}

// ParseQuotaWindows decodes the provider quota observation snapshot into
// usage windows. Signals from providers without a known layout yield nil.
// The result is sorted by scope then name for deterministic output.
func ParseQuotaWindows(provider string, quota QuotaState) []QuotaWindow {
	if len(quota.Signals) == 0 {
		return nil
	}
	var windows []QuotaWindow
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude":
		windows = parseClaudeQuotaWindows(quota.Signals)
	case "codex":
		windows = parseCodexQuotaWindows(quota.Signals, quota.ObservedAt)
	case "devin":
		windows = parseDevinQuotaWindows(quota.Signals)
	default:
		return nil
	}
	sort.Slice(windows, func(i, j int) bool {
		if windows[i].Scope != windows[j].Scope {
			return windows[i].Scope < windows[j].Scope
		}
		return windows[i].Name < windows[j].Name
	})
	return windows
}

const claudeUnifiedSignalPrefix = "anthropic-ratelimit-unified-"

// claudeWindowDurations lists the nominal lengths of the Anthropic unified windows.
var claudeWindowDurations = map[string]time.Duration{
	"5h": 5 * time.Hour,
	"7d": 7 * 24 * time.Hour,
}

func parseClaudeQuotaWindows(signals map[string]string) []QuotaWindow {
	byName := make(map[string]*QuotaWindow)
	for key, value := range signals {
		lower := strings.ToLower(strings.TrimSpace(key))
		if !strings.HasPrefix(lower, claudeUnifiedSignalPrefix) {
			continue
		}
		rest := strings.TrimPrefix(lower, claudeUnifiedSignalPrefix)
		name, field, ok := splitLastField(rest, "utilization", "reset", "status")
		if !ok || name == "" || name == "overage" {
			continue
		}
		window := byName[name]
		if window == nil {
			window = &QuotaWindow{Name: name}
			if duration, known := claudeWindowDurations[name]; known {
				window.Duration = duration
			} else if strings.HasPrefix(name, "7d") {
				// Model-specific weekly windows (e.g. 7d_oi) share the weekly length
				// but only apply to a subset of models.
				window.Duration = 7 * 24 * time.Hour
				window.Scope = name
			} else {
				window.Scope = name
			}
			byName[name] = window
		}
		switch field {
		case "utilization":
			if used, okUsed := parseFiniteFloat(value); okUsed {
				window.UsedFraction = used
				window.HasUsed = true
			}
		case "reset":
			if resetAt, okReset := parseQuotaTimestamp(value); okReset {
				window.ResetAt = resetAt
			}
		case "status":
			window.Status = strings.ToLower(strings.TrimSpace(value))
		}
	}
	return collectWindows(byName)
}

const codexSignalPrefix = "x-codex-"

func parseCodexQuotaWindows(signals map[string]string, observedAt time.Time) []QuotaWindow {
	byKey := make(map[string]*QuotaWindow)
	labels := make(map[string]string)
	resetAfter := make(map[string]time.Duration)
	limitReached := make(map[string]bool)
	for key, value := range signals {
		lower := strings.ToLower(strings.TrimSpace(key))
		if !strings.HasPrefix(lower, codexSignalPrefix) {
			continue
		}
		rest := strings.TrimPrefix(lower, codexSignalPrefix)
		if scope, okLabel := strings.CutSuffix(rest, "-limit-name"); okLabel {
			labels[normalizeCodexScope(scope)] = strings.TrimSpace(value)
			continue
		}
		if scope, okReached := strings.CutSuffix(rest, "-limit-reached"); okReached || rest == "limit-reached" {
			if rest == "limit-reached" {
				scope = ""
			}
			reached, _ := strconv.ParseBool(strings.TrimSpace(value))
			limitReached[normalizeCodexScope(scope)] = reached
			continue
		}
		scope, slot, field, ok := splitCodexWindowSignal(rest)
		if !ok {
			continue
		}
		mapKey := scope + "\x00" + slot
		window := byKey[mapKey]
		if window == nil {
			window = &QuotaWindow{Name: slot, Scope: scope}
			byKey[mapKey] = window
		}
		switch field {
		case "used-percent":
			if used, okUsed := parseFiniteFloat(value); okUsed {
				window.UsedFraction = used / 100
				window.HasUsed = true
			}
		case "window-minutes":
			if minutes, okMinutes := parseFiniteFloat(value); okMinutes && minutes > 0 {
				window.Duration = time.Duration(minutes * float64(time.Minute))
			}
		case "reset-at":
			if resetAt, okReset := parseQuotaTimestamp(value); okReset {
				window.ResetAt = resetAt
			}
		case "reset-after-seconds":
			if seconds, okSeconds := parseFiniteFloat(value); okSeconds && seconds >= 0 {
				resetAfter[mapKey] = time.Duration(seconds * float64(time.Second))
			}
		}
	}
	for mapKey, window := range byKey {
		if window.ResetAt.IsZero() && !observedAt.IsZero() {
			if delay, okDelay := resetAfter[mapKey]; okDelay {
				window.ResetAt = observedAt.Add(delay)
			}
		}
		window.Label = labels[window.Scope]
		if limitReached[window.Scope] {
			window.Status = "limit_reached"
		}
	}
	return collectWindows(byKey)
}

// splitCodexWindowSignal splits "primary-used-percent" or
// "bengalfox-secondary-reset-at" into scope, slot, and field.
func splitCodexWindowSignal(rest string) (scope, slot, field string, ok bool) {
	for _, candidate := range []string{"primary", "secondary"} {
		if after, found := strings.CutPrefix(rest, candidate+"-"); found {
			if !isCodexWindowField(after) {
				return "", "", "", false
			}
			return "", candidate, after, true
		}
		marker := "-" + candidate + "-"
		if index := strings.Index(rest, marker); index > 0 {
			after := rest[index+len(marker):]
			if !isCodexWindowField(after) {
				return "", "", "", false
			}
			return normalizeCodexScope(rest[:index]), candidate, after, true
		}
	}
	return "", "", "", false
}

func isCodexWindowField(field string) bool {
	switch field {
	case "used-percent", "window-minutes", "reset-at", "reset-after-seconds":
		return true
	default:
		return false
	}
}

// normalizeCodexScope folds the websocket "additional-<limit>" namespace onto
// the HTTP short-name namespace so both transports report one scope.
func normalizeCodexScope(scope string) string {
	scope = strings.TrimSpace(scope)
	return strings.TrimPrefix(scope, "additional-")
}

func parseDevinQuotaWindows(signals map[string]string) []QuotaWindow {
	windows := make([]QuotaWindow, 0, 2)
	for _, spec := range []struct {
		name     string
		duration time.Duration
	}{
		{name: "daily", duration: 24 * time.Hour},
		{name: "weekly", duration: 7 * 24 * time.Hour},
	} {
		window := QuotaWindow{Name: spec.name, Duration: spec.duration}
		found := false
		if raw, ok := signals[spec.name+"_quota_remaining_percent"]; ok {
			if remaining, okRemaining := parseFiniteFloat(strings.TrimSuffix(strings.TrimSpace(raw), "%")); okRemaining {
				window.UsedFraction = clampUnit(1 - remaining/100)
				window.HasUsed = true
				found = true
			}
		}
		if raw, ok := signals[spec.name+"_quota_reset_at"]; ok {
			if resetAt, okReset := parseQuotaTimestamp(raw); okReset {
				window.ResetAt = resetAt
				found = true
			}
		}
		if found {
			windows = append(windows, window)
		}
	}
	return windows
}

func collectWindows[K comparable](windows map[K]*QuotaWindow) []QuotaWindow {
	if len(windows) == 0 {
		return nil
	}
	out := make([]QuotaWindow, 0, len(windows))
	for _, window := range windows {
		if window == nil || (!window.HasUsed && window.ResetAt.IsZero() && window.Status == "") {
			continue
		}
		out = append(out, *window)
	}
	return out
}

// splitLastField splits "5h-utilization" into ("5h", "utilization") when the
// trailing segment is one of fields.
func splitLastField(value string, fields ...string) (string, string, bool) {
	for _, field := range fields {
		if prefix, ok := strings.CutSuffix(value, "-"+field); ok {
			return prefix, field, true
		}
	}
	return "", "", false
}

func parseFiniteFloat(raw string) (float64, bool) {
	parsed, errParse := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if errParse != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, false
	}
	return parsed, true
}

// quotaTimestampMillisThreshold separates Unix seconds from Unix milliseconds:
// second values above it would lie past the year 5000.
const quotaTimestampMillisThreshold = 1e11

// parseQuotaTimestamp accepts Unix seconds or milliseconds (integer or
// fractional) and RFC3339.
func parseQuotaTimestamp(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if seconds, ok := parseFiniteFloat(raw); ok {
		if seconds > quotaTimestampMillisThreshold {
			seconds /= 1000
		}
		if seconds <= 0 || seconds > quotaTimestampMillisThreshold {
			return time.Time{}, false
		}
		whole := math.Floor(seconds)
		return time.Unix(int64(whole), int64((seconds-whole)*1e9)), true
	}
	if parsed, errParse := time.Parse(time.RFC3339, raw); errParse == nil {
		return parsed, true
	}
	return time.Time{}, false
}

func clampUnit(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
