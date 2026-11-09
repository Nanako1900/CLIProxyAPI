package main

import (
	"math"
	"sort"
	"time"
)

const (
	// usedDropThreshold ignores small used decreases caused by concurrent
	// responses finishing out of order.
	usedDropThreshold = 0.02
	// minDriftElapsed is the observed time inside windows needed before the
	// reset drift ratio is trusted.
	minDriftElapsed = 30 * time.Minute
	// fixedDriftRatio and rollingDriftRatio bound how far reset_at_ms moves per
	// unit of elapsed time inside one window instance.
	fixedDriftRatio   = 0.05
	rollingDriftRatio = 0.5
	// minResetTolerance absorbs the jitter of resets derived from
	// reset-after-seconds plus the observation time (Codex).
	minResetTolerance = time.Minute
	// firstUseLagMargin allows the first observation of a window to trail its
	// start by the duration of the request, because observations are written
	// when the response completes.
	firstUseLagMargin = 15 * time.Minute
)

// seriesKey identifies one window of one credential.
type seriesKey struct {
	Auth     string
	Provider string
	Scope    string
	Name     string
}

// typeKey identifies a window type across credentials.
type typeKey struct {
	Provider string
	Scope    string
	Name     string
}

func (k seriesKey) typeKey() typeKey {
	return typeKey{Provider: k.Provider, Scope: k.Scope, Name: k.Name}
}

func windowLabel(scope, name string) string {
	if scope == "" {
		return name
	}
	return scope + "/" + name
}

// windowPoint is one observed state of a window.
type windowPoint struct {
	TsMs      int64
	ResetAtMs int64
	Used      float64
	HasUsed   bool
	DurationS int64
}

// windowInstance is one run of a window between two resets.
type windowInstance struct {
	FirstMs      int64
	LastMs       int64
	FirstResetMs int64
	LastResetMs  int64
	MaxResetMs   int64
	LastUsed     float64
	PeakUsed     float64
	HasUsed      bool
	Points       int
	// Closed reports that the reset passed inside the log, so the peak used
	// share is final and the remainder perished.
	Closed bool
}

func newInstance(p windowPoint) windowInstance {
	in := windowInstance{FirstMs: p.TsMs, FirstResetMs: p.ResetAtMs}
	in.add(p)
	return in
}

func (in *windowInstance) add(p windowPoint) {
	in.Points++
	in.LastMs = p.TsMs
	if p.ResetAtMs > 0 {
		if in.FirstResetMs == 0 {
			in.FirstResetMs = p.ResetAtMs
		}
		in.LastResetMs = p.ResetAtMs
		in.MaxResetMs = max(in.MaxResetMs, p.ResetAtMs)
	}
	if p.HasUsed {
		in.LastUsed = p.Used
		if !in.HasUsed || p.Used > in.PeakUsed {
			in.PeakUsed = p.Used
		}
		in.HasUsed = true
	}
}

// Perished is the share of the window left unused at reset.
func (in windowInstance) Perished() float64 {
	return clampUnit(1 - in.PeakUsed)
}

// boundaryCounts classifies transitions from one window instance to the next.
type boundaryCounts struct {
	// Contiguous: the next window was observed right after the reset, so a
	// first-use start and an anchored start cannot be told apart.
	Contiguous int
	// FirstUse: after an idle gap, the new window started at its first observed use.
	FirstUse int
	// Anchored: after an idle gap, the new window still started at the previous
	// reset (or a whole number of window lengths after it).
	Anchored int
	// External: the new window started during the idle gap, before any use
	// through this proxy (usage elsewhere, or another start rule).
	External int
	// Unknown: no nominal window length to compare against.
	Unknown int
}

func (b boundaryCounts) total() int {
	return b.Contiguous + b.FirstUse + b.Anchored + b.External + b.Unknown
}

// windowEvidence holds the counters that determine window semantics. It is
// additive so per-credential evidence can be merged per window type.
type windowEvidence struct {
	Points  int
	Same    int
	Forward int
	// Stale counts observations reporting an earlier reset than the window
	// already seen (responses completing out of order); they are skipped.
	Stale        int
	UsedDrops    int
	MissingReset int
	DriftMs      int64
	ElapsedMs    int64
	Boundaries   boundaryCounts
	LagsMs       []int64
	SpacingMs    []int64
	IdleMs       []int64
	MaxLeadMs    int64
	CostDelta    float64
	CostPairs    int
	Durations    map[int64]int
}

func (e *windowEvidence) merge(other windowEvidence) {
	e.Points += other.Points
	e.Same += other.Same
	e.Forward += other.Forward
	e.Stale += other.Stale
	e.UsedDrops += other.UsedDrops
	e.MissingReset += other.MissingReset
	e.DriftMs += other.DriftMs
	e.ElapsedMs += other.ElapsedMs
	e.Boundaries.Contiguous += other.Boundaries.Contiguous
	e.Boundaries.FirstUse += other.Boundaries.FirstUse
	e.Boundaries.Anchored += other.Boundaries.Anchored
	e.Boundaries.External += other.Boundaries.External
	e.Boundaries.Unknown += other.Boundaries.Unknown
	e.LagsMs = append(e.LagsMs, other.LagsMs...)
	e.SpacingMs = append(e.SpacingMs, other.SpacingMs...)
	e.IdleMs = append(e.IdleMs, other.IdleMs...)
	if other.MaxLeadMs > e.MaxLeadMs {
		e.MaxLeadMs = other.MaxLeadMs
	}
	e.CostDelta += other.CostDelta
	e.CostPairs += other.CostPairs
	for duration, count := range other.Durations {
		if e.Durations == nil {
			e.Durations = make(map[int64]int)
		}
		e.Durations[duration] += count
	}
}

// NominalS is the most frequent reported window length in seconds.
func (e windowEvidence) NominalS() int64 {
	var best int64
	bestCount := 0
	for duration, count := range e.Durations {
		if count > bestCount || (count == bestCount && duration < best) {
			best, bestCount = duration, count
		}
	}
	return best
}

// DriftRatio is reset movement per unit of elapsed time inside windows.
func (e windowEvidence) DriftRatio() float64 {
	if e.ElapsedMs <= 0 {
		return 0
	}
	return float64(e.DriftMs) / float64(e.ElapsedMs)
}

// semanticsKind is the window behavior inferred from the evidence.
type semanticsKind int

const (
	semInsufficient semanticsKind = iota
	semRolling
	semUnclear
	semFixedUndetermined
	semFixedFirstUse
	semFixedAnchored
	semFixedMixed
)

// semantics is a window verdict. DriftVerified is false when too little time
// was observed inside windows to rule out a rolling window.
type semantics struct {
	Kind          semanticsKind
	DriftVerified bool
}

func (s semantics) fixed() bool {
	return s.Kind >= semFixedUndetermined
}

func (s semantics) String() string {
	label := map[semanticsKind]string{
		semInsufficient:      "insufficient data",
		semRolling:           "rolling (reset moves with usage)",
		semUnclear:           "unclear (reset drifts)",
		semFixedUndetermined: "fixed, start rule undetermined",
		semFixedFirstUse:     "fixed, starts on first use",
		semFixedAnchored:     "fixed, anchored to previous reset",
		semFixedMixed:        "fixed, start rule mixed",
	}[s.Kind]
	if s.fixed() && !s.DriftVerified {
		label += " (drift unverified)"
	}
	return label
}

func (e windowEvidence) semantics() semantics {
	if e.Points < 2 {
		return semantics{Kind: semInsufficient}
	}
	verified := e.ElapsedMs >= minDriftElapsed.Milliseconds()
	if verified {
		switch ratio := e.DriftRatio(); {
		case ratio >= rollingDriftRatio:
			return semantics{Kind: semRolling, DriftVerified: true}
		case ratio > fixedDriftRatio:
			return semantics{Kind: semUnclear, DriftVerified: true}
		}
	} else if e.Boundaries.total() == 0 {
		return semantics{Kind: semInsufficient}
	}
	b := e.Boundaries
	kind := semFixedMixed
	switch {
	case b.FirstUse == 0 && b.Anchored == 0:
		kind = semFixedUndetermined
	case b.FirstUse >= 2*b.Anchored:
		kind = semFixedFirstUse
	case b.Anchored >= 2*b.FirstUse:
		kind = semFixedAnchored
	}
	return semantics{Kind: kind, DriftVerified: verified}
}

// windowAnalysis is the evidence and instance timeline of one credential window.
type windowAnalysis struct {
	seriesKey
	windowEvidence
	GranularityMs int64
	TolMs         int64
	Instances     []windowInstance
}

// perished sums the unused share of closed window instances.
func (a windowAnalysis) perished() (int, float64) {
	closed, perished := 0, 0.0
	for _, in := range a.Instances {
		if in.Closed && in.HasUsed {
			closed++
			perished += in.Perished()
		}
	}
	return closed, perished
}

// buildSeries groups observation windows per credential window, in time order.
func buildSeries(observations []observation) map[seriesKey][]windowPoint {
	series := make(map[seriesKey][]windowPoint)
	for _, obs := range observations {
		ts := obs.ObservedAtMs
		if ts <= 0 {
			ts = obs.TsMs
		}
		for _, w := range obs.Windows {
			key := seriesKey{Auth: obs.Auth, Provider: obs.Provider, Scope: w.Scope, Name: w.Name}
			series[key] = append(series[key], windowPoint{
				TsMs:      ts,
				ResetAtMs: w.ResetAtMs,
				Used:      w.Used,
				HasUsed:   w.HasUsed,
				DurationS: w.DurationS,
			})
		}
	}
	for key := range series {
		points := series[key]
		sort.SliceStable(points, func(i, j int) bool { return points[i].TsMs < points[j].TsMs })
	}
	return series
}

// analyzeSeries walks the observations of one window that carry a reset time.
// A new instance starts when the latest reset seen had passed and the reset
// moved later; an earlier reset is a stale snapshot; any other change is drift
// inside the instance.
func analyzeSeries(key seriesKey, points []windowPoint, tolMs, logEndMs int64) windowAnalysis {
	a := windowAnalysis{seriesKey: key}
	a.GranularityMs = resetGranularity(points)
	a.TolMs = max(tolMs, a.GranularityMs, minResetTolerance.Milliseconds())
	a.Durations = make(map[int64]int)
	for _, p := range points {
		if p.DurationS > 0 {
			a.Durations[p.DurationS]++
		}
	}
	nominalMs := a.NominalS() * 1000
	var current, prev windowInstance
	started := false
	for _, p := range points {
		a.Points++
		if p.ResetAtMs <= 0 {
			// Without a reset the point cannot be placed in a window instance.
			a.MissingReset++
			continue
		}
		a.MaxLeadMs = max(a.MaxLeadMs, p.ResetAtMs-p.TsMs)
		if !started {
			current, prev, started = newInstance(p), newInstance(p), true
			continue
		}
		if p.ResetAtMs < current.MaxResetMs-a.TolMs {
			a.Stale++
			continue
		}
		if p.TsMs >= current.MaxResetMs-a.TolMs && p.ResetAtMs-current.MaxResetMs > a.TolMs {
			a.classifyBoundary(current, p, nominalMs)
			current.Closed = true
			a.Instances = append(a.Instances, current)
			current, prev = newInstance(p), newInstance(p)
			continue
		}
		a.comparePair(prev, p)
		current.add(p)
		prev = newInstance(p)
	}
	if started {
		current.Closed = logEndMs >= current.MaxResetMs
		a.Instances = append(a.Instances, current)
	}
	for _, in := range a.Instances {
		if in.FirstResetMs > 0 && in.LastResetMs > 0 {
			a.DriftMs += absInt64(in.LastResetMs - in.FirstResetMs)
			a.ElapsedMs += in.LastMs - in.FirstMs
		}
	}
	return a
}

// comparePair records how the reset and the used share moved between two
// consecutive observations inside one instance.
func (a *windowAnalysis) comparePair(prev windowInstance, p windowPoint) {
	if absInt64(p.ResetAtMs-prev.LastResetMs) <= a.TolMs {
		a.Same++
	} else {
		a.Forward++
	}
	if prev.HasUsed && p.HasUsed {
		if p.Used < prev.LastUsed-usedDropThreshold {
			a.UsedDrops++
		}
		a.CostDelta += p.Used - prev.LastUsed
		a.CostPairs++
	}
}

// classifyBoundary decides how the window that starts at p was anchored,
// using the instance that ended before it.
func (a *windowAnalysis) classifyBoundary(prev windowInstance, p windowPoint, nominalMs int64) {
	gap := p.TsMs - prev.MaxResetMs
	spacing := p.ResetAtMs - prev.MaxResetMs
	a.SpacingMs = append(a.SpacingMs, spacing)
	if gap > 0 {
		a.IdleMs = append(a.IdleMs, gap)
	}
	if nominalMs <= 0 {
		a.Boundaries.Unknown++
		return
	}
	if gap <= 2*a.TolMs {
		a.Boundaries.Contiguous++
		return
	}
	// lag is how long after the inferred window start the first use was observed.
	lag := p.TsMs - (p.ResetAtMs - nominalMs)
	firstUseFits := lag >= -a.TolMs && lag <= a.TolMs+firstUseLagMargin.Milliseconds()
	cycles := max(int64(math.Round(float64(spacing)/float64(nominalMs))), 1)
	errAnchored := absInt64(spacing - cycles*nominalMs)
	switch {
	case firstUseFits && absInt64(lag) <= errAnchored:
		a.Boundaries.FirstUse++
		a.LagsMs = append(a.LagsMs, lag)
	case errAnchored <= a.TolMs:
		a.Boundaries.Anchored++
	default:
		a.Boundaries.External++
	}
}

// resetGranularity returns the coarsest unit (hour, minute, second) that all
// reset times are aligned to, so rounded provider timestamps widen the tolerance.
func resetGranularity(points []windowPoint) int64 {
	units := []int64{time.Hour.Milliseconds(), time.Minute.Milliseconds(), time.Second.Milliseconds()}
	seen := false
	for _, unit := range units {
		aligned := true
		for _, p := range points {
			if p.ResetAtMs <= 0 {
				continue
			}
			seen = true
			if p.ResetAtMs%unit != 0 {
				aligned = false
				break
			}
		}
		if seen && aligned {
			return unit
		}
	}
	return 0
}

// windowReport covers report sections 4 and 5.
type windowReport struct {
	Series []windowAnalysis
	Types  []windowTypeSummary
	// TypeSemantics is the merged verdict per window type; perished quota is
	// only defined for types that are not rolling or unclear.
	TypeSemantics map[typeKey]semantics
}

// perishDefined reports whether "unused at reset" is meaningful for a window type.
func (s semantics) perishDefined() bool {
	return s.Kind != semRolling && s.Kind != semUnclear
}

// windowTypeSummary merges the evidence of one window type across credentials.
type windowTypeSummary struct {
	typeKey
	windowEvidence
	Credentials int
	Semantics   semantics
	Closed      int
	Perished    float64
}

func analyzeWindows(observations []observation, tolMs, logEndMs int64) windowReport {
	series := buildSeries(observations)
	keys := make([]seriesKey, 0, len(series))
	for key := range series {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return seriesLess(keys[i], keys[j]) })
	report := windowReport{Series: make([]windowAnalysis, 0, len(keys))}
	types := make(map[typeKey]*windowTypeSummary)
	for _, key := range keys {
		analysis := analyzeSeries(key, series[key], tolMs, logEndMs)
		report.Series = append(report.Series, analysis)
		summary := types[key.typeKey()]
		if summary == nil {
			summary = &windowTypeSummary{typeKey: key.typeKey()}
			types[key.typeKey()] = summary
		}
		summary.Credentials++
		summary.merge(analysis.windowEvidence)
	}
	report.TypeSemantics = make(map[typeKey]semantics, len(types))
	for key, summary := range types {
		summary.Semantics = summary.semantics()
		report.TypeSemantics[key] = summary.Semantics
	}
	for _, analysis := range report.Series {
		summary := types[analysis.typeKey()]
		if !summary.Semantics.perishDefined() {
			continue
		}
		closed, perished := analysis.perished()
		summary.Closed += closed
		summary.Perished += perished
	}
	for _, summary := range types {
		report.Types = append(report.Types, *summary)
	}
	sort.Slice(report.Types, func(i, j int) bool { return typeLess(report.Types[i].typeKey, report.Types[j].typeKey) })
	return report
}

func seriesLess(a, b seriesKey) bool {
	if a.Auth != b.Auth {
		return a.Auth < b.Auth
	}
	return typeLess(a.typeKey(), b.typeKey())
}

func typeLess(a, b typeKey) bool {
	if a.Provider != b.Provider {
		return a.Provider < b.Provider
	}
	if a.Scope != b.Scope {
		return a.Scope < b.Scope
	}
	return a.Name < b.Name
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func clampUnit(value float64) float64 {
	switch {
	case value < 0:
		return 0
	case value > 1:
		return 1
	default:
		return value
	}
}
