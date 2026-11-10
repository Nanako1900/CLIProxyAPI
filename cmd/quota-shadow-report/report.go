package main

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// options configures the analysis.
type options struct {
	TolMs    int64
	Timeline bool
	Replay   replayOptions
}

// report holds every analysis of one dataset.
type report struct {
	ds        *dataset
	opts      options
	Agreement agreementStats
	Outages   outageStats
	Windows   windowReport
	Replay    replayResult
}

func buildReport(ds *dataset, opts options) report {
	windows := analyzeWindows(ds.Observations, opts.TolMs, ds.LastMs)
	return report{
		ds:        ds,
		opts:      opts,
		Agreement: analyzeAgreement(ds.Selections),
		Outages:   analyzeOutages(ds.Selections, ds.Errors),
		Windows:   windows,
		Replay:    runReplay(ds, windows, opts.Replay),
	}
}

// printer writes formatted text and keeps the first write error.
type printer struct {
	w   io.Writer
	err error
}

func (p *printer) f(format string, args ...any) {
	if p.err == nil {
		_, p.err = fmt.Fprintf(p.w, format, args...)
	}
}

func (p *printer) heading(title string) {
	p.f("\n%s\n%s\n", title, strings.Repeat("-", len(title)))
}

// table writes tab-separated rows aligned into columns.
func (p *printer) table(rows [][]string) {
	if p.err != nil || len(rows) == 0 {
		return
	}
	tw := tabwriter.NewWriter(p.w, 0, 0, 2, ' ', 0)
	for _, row := range rows {
		if _, errWrite := fmt.Fprintf(tw, "  %s\n", strings.Join(row, "\t")); errWrite != nil {
			p.err = errWrite
			return
		}
	}
	p.err = tw.Flush()
}

func (r report) render(w io.Writer) error {
	p := &printer{w: w}
	r.renderHeader(p)
	r.renderAgreement(p)
	r.renderSoftLimit(p)
	r.renderOutages(p)
	r.renderSemantics(p)
	r.renderUtilization(p)
	r.renderReplay(p)
	return p.err
}

func (r report) renderHeader(p *printer) {
	ds := r.ds
	p.f("Quota shadow report\n===================\n")
	p.f("Files:   %s\n", strings.Join(ds.Files, ", "))
	p.f("Records: %d lines: %d selection, %d selection_error, %d observation", ds.Lines, len(ds.Selections), len(ds.Errors), len(ds.Observations))
	p.f("; %d malformed, %d unknown type\n", ds.Malformed, ds.Unknown)
	if ds.LastMs > 0 {
		p.f("Span:    %s to %s (%s)\n", fmtTime(ds.FirstMs), fmtTime(ds.LastMs), fmtDuration(ds.LastMs-ds.FirstMs))
	}
	if ds.MaxDropped > 0 {
		p.f("WARNING: the recorder dropped at least %d records (queue full); counts below are incomplete.\n", ds.MaxDropped)
	}
}

func (r report) renderAgreement(p *printer) {
	a := r.Agreement
	p.heading("1. Agreement between actual and shadow picks")
	p.f("Selections: %d\n", a.Selections)
	p.f("  Real pick in the scored pool:      %s\n", fmtRatio(a.InPool))
	p.f("    first attempt:                   %s\n", fmtRatio(a.FirstAttempt))
	p.f("    failover attempts:               %s\n", fmtRatio(a.Failover))
	p.f("  Real pick outside the scored pool: %d (%s of selections; structural, not a policy difference)\n", a.OutOfPool, fmtShare(a.OutOfPool, a.Selections))
	p.f("  Selections with no scored candidate: %d\n", a.NoCandidates)
	if len(a.ByReason) == 0 {
		return
	}
	p.f("\n  In-pool agreement by shadow_reason:\n")
	rows := [][]string{{"reason", "selections", "agree", "rate"}}
	for _, row := range a.ByReason {
		rows = append(rows, []string{row.Reason, itoa(row.Total), itoa(row.Hit), fmtShare(row.Hit, row.Total)})
	}
	p.table(rows)
}

func (r report) renderSoftLimit(p *printer) {
	a := r.Agreement
	p.heading("2. Soft-limited real picks")
	p.f("In-pool picks with a scored entry: %d\n", a.SoftLimited.Total)
	p.f("  Soft-limited in the shadow view: %s\n", fmtHits(a.SoftLimited))
	p.f("    while another candidate was not soft-limited: %d\n", a.SoftAvoidable)
	if a.SoftUnknown > 0 {
		p.f("  Entry missing (candidate list truncated at 128): %d\n", a.SoftUnknown)
	}
}

func (r report) renderOutages(p *printer) {
	o := r.Outages
	p.heading("3. Outages (selection_error)")
	p.f("selection_error records: %d (%d on the first attempt)\n", o.Errors, o.FirstAttemptErrors)
	if o.Errors == 0 {
		return
	}
	p.f("Episodes: %d", o.Episodes)
	if o.Open > 0 {
		p.f(" (%d still open at the end of the log, measured to their last failure)", o.Open)
	}
	p.f("\nGap minutes: total %s, max %s\n", fmtMinutes(o.TotalGapMs), fmtMinutes(o.MaxGapMs))
	p.f("  An episode runs from the first failed pick to the next successful pick on the same route;\n")
	p.f("  idle clients stretch it, so treat it as an upper bound on the unavailable time.\n")
	statuses := make([]string, 0, len(o.Statuses))
	for _, entry := range o.Statuses {
		statuses = append(statuses, fmt.Sprintf("%s x%d", entry.Label, entry.Count))
	}
	p.f("Status: %s\n", strings.Join(statuses, ", "))
	d := o.RetryAfter
	p.f("retry_after_ms: %d of %d records", d.Count, o.Errors)
	if d.Count > 0 {
		p.f("; min %s, p50 %s, p90 %s, max %s\n", fmtDuration(d.Min), fmtDuration(d.P50), fmtDuration(d.P90), fmtDuration(d.Max))
		rows := make([][]string, 0, len(d.Buckets))
		for _, bucket := range d.Buckets {
			rows = append(rows, []string{bucket.Label, itoa(bucket.Count)})
		}
		p.table(rows)
	} else {
		p.f("\n")
	}
	p.f("\nBy route (providers model):\n")
	rows := [][]string{{"route", "errors", "episodes", "open", "gap_total_min", "gap_max_min"}}
	for _, route := range o.Routes {
		rows = append(rows, []string{route.Route, itoa(route.Errors), itoa(route.Episodes), itoa(route.Open), fmtMinutes(route.TotalGapMs), fmtMinutes(route.MaxGapMs)})
	}
	p.table(rows)
}

func (r report) renderSemantics(p *printer) {
	p.heading("4. Window semantics (from consecutive observations)")
	if len(r.Windows.Series) == 0 {
		p.f("No observation carried quota windows.\n")
		return
	}
	p.f("same/fwd: consecutive observations inside one window whose reset_at_ms stayed within\n")
	p.f("tolerance or moved later; stale: observations reporting an earlier reset than already seen\n")
	p.f("(out-of-order responses), ignored. drift: reset movement per unit of observed time\n")
	p.f("inside windows (0 = fixed, about 1 = rolling). Boundaries after an idle gap tell a window\n")
	p.f("that starts on first use (first) from one anchored to the previous reset (anch); ext means\n")
	p.f("it started before any use through this proxy; cont means no idle gap, so undecidable.\n\n")
	rows := [][]string{{"provider", "window", "creds", "obs", "same/fwd/stale", "drift", "used_drops", "cont/first/anch/ext", "nominal", "max_lead", "reset_spacing", "verdict"}}
	for _, t := range r.Windows.Types {
		rows = append(rows, append([]string{t.Provider, windowLabel(t.Scope, t.Name), itoa(t.Credentials)}, evidenceColumns(t.windowEvidence, t.Semantics)...))
	}
	p.table(rows)
	for _, t := range r.Windows.Types {
		if len(t.LagsMs) > 0 {
			lags := summarizeSigned(t.LagsMs)
			p.f("  %s/%s first-use lag (first observation after window start): min %s, median %s, max %s\n",
				t.Provider, windowLabel(t.Scope, t.Name), lags[0], lags[1], lags[2])
		}
	}
	p.f("\nPer credential:\n")
	rows = [][]string{{"credential", "window", "obs", "same/fwd/stale", "drift", "used_drops", "cont/first/anch/ext", "nominal", "max_lead", "reset_spacing", "verdict"}}
	for _, s := range r.Windows.Series {
		rows = append(rows, append([]string{s.Auth, windowLabel(s.Scope, s.Name)}, evidenceColumns(s.windowEvidence, s.semantics())...))
	}
	p.table(rows)
}

func evidenceColumns(e windowEvidence, sem semantics) []string {
	b := e.Boundaries
	boundaries := fmt.Sprintf("%d/%d/%d/%d", b.Contiguous, b.FirstUse, b.Anchored, b.External)
	if b.Unknown > 0 {
		boundaries += fmt.Sprintf(" (+%d no length)", b.Unknown)
	}
	drift := "-"
	if e.ElapsedMs > 0 {
		drift = strconv.FormatFloat(e.DriftRatio(), 'f', 2, 64)
	}
	spacing := "-"
	if len(e.SpacingMs) > 0 {
		values := summarizeSigned(e.SpacingMs)
		spacing = values[0] + "/" + values[1] + "/" + values[2]
	}
	nominal := "-"
	if seconds := e.NominalS(); seconds > 0 {
		nominal = fmtDuration(seconds * 1000)
	}
	lead := "-"
	if e.MaxLeadMs > 0 {
		lead = fmtDuration(e.MaxLeadMs)
	}
	return []string{
		itoa(e.Points),
		fmt.Sprintf("%d/%d/%d", e.Same, e.Forward, e.Stale),
		drift,
		itoa(e.UsedDrops),
		boundaries,
		nominal,
		lead,
		spacing,
		sem.String(),
	}
}

func (r report) renderUtilization(p *printer) {
	p.heading("5. Utilization and perished quota")
	if len(r.Windows.Series) == 0 {
		p.f("No observation carried quota windows.\n")
		return
	}
	p.f("A window is closed once its reset passed inside the log. Perished = 1 - peak used share\n")
	p.f("observed before the reset. Usage outside this proxy is invisible, so perished is an upper\n")
	p.f("bound. idle_after_reset is time between a reset and the next observed use. Rolling or\n")
	p.f("unclear window types have no fixed reset, so no perished share is computed for them.\n\n")
	rows := [][]string{{"provider", "window", "closed", "mean_used_at_reset", "mean_perished", "perished_total", "idle_after_reset"}}
	for _, t := range r.Windows.Types {
		rows = append(rows, []string{
			t.Provider, windowLabel(t.Scope, t.Name), itoa(t.Closed),
			fmtMean(float64(t.Closed)-t.Perished, t.Closed), fmtMean(t.Perished, t.Closed),
			fmtFloat(t.Perished), fmtDuration(sumInt64(t.IdleMs)),
		})
	}
	p.table(rows)
	for _, s := range r.Windows.Series {
		r.renderCredentialTimeline(p, s)
	}
}

func (r report) renderCredentialTimeline(p *printer, s windowAnalysis) {
	last := s.Instances[len(s.Instances)-1]
	p.f("\n%s %s: last used %s at %s", s.Auth, windowLabel(s.Scope, s.Name), fmtUsed(last), fmtTime(last.LastMs))
	if last.LastResetMs > 0 {
		p.f(" (reset %s)", fmtTime(last.LastResetMs))
	}
	sem := r.Windows.TypeSemantics[s.typeKey()]
	if !sem.perishDefined() {
		p.f("\n  window type is %s; perished quota not computed\n", sem)
		return
	}
	closed, perished := s.perished()
	p.f("\n  closed windows %d, mean used at reset %s, perished total %s, idle after reset %s\n",
		closed, fmtMean(float64(closed)-perished, closed), fmtFloat(perished), fmtDuration(sumInt64(s.IdleMs)))
	if !r.opts.Timeline {
		return
	}
	rows := [][]string{{"reset", "obs", "first", "last", "last_used", "peak_used", "perished"}}
	for _, in := range s.Instances {
		perishedText := "open"
		if in.Closed && in.HasUsed {
			perishedText = fmtFloat(in.Perished())
		}
		rows = append(rows, []string{fmtTime(in.LastResetMs), itoa(in.Points), fmtTime(in.FirstMs), fmtTime(in.LastMs), fmtUsed(in), fmtPeak(in), perishedText})
	}
	p.table(rows)
}

func (r report) renderReplay(p *printer) {
	res := r.Replay
	p.heading("6. Replay: round-robin vs fill-first vs shadow")
	if !res.Ran {
		p.f("Not run: the log does not support a credible replay.\n")
		for _, reason := range res.Skipped {
			p.f("  - %s\n", reason)
		}
		renderNeeded(p, res.Needed)
		return
	}
	p.f("Model-based estimate over %d requests on %d routes and %d credentials.\n", res.Demand, res.Routes, res.Credentials)
	for _, reason := range res.Excluded {
		p.f("  excluded: %s\n", reason)
	}
	p.f("Assumptions:\n")
	for _, assumption := range res.Assumptions {
		p.f("  - %s\n", assumption)
	}
	rows := [][]string{{"policy", "served", "rejected", "episodes", "gap_total_min", "gap_max_min", "first_reject", "perished_long", "perished_short"}}
	for _, outcome := range append([]policyOutcome{res.Actual}, res.Policies...) {
		rows = append(rows, outcomeColumns(outcome, outcome.Name == res.Actual.Name))
	}
	p.f("\n")
	p.table(rows)
	p.f("\nrejected: requests that ended without a credential (a selection_error at any attempt in the log,\n")
	p.f("no available credential in the simulation). perished_*: unused share summed over window resets\n")
	p.f("inside the log (window-equivalents).\n")
	p.f("Calibration, simulated %s against the log: rejected requests differ by %.1f%% of demand\n", res.Strategy, res.CalibrationGap*100)
	p.f("(tolerance %.1f%%); per-credential share of first picks differs by %.2f (total variation,\n", calibrationTolerance*100, res.ShareDistance)
	p.f("tolerance %.2f).\n", shareTolerance)
	switch res.Calibration {
	case calibrationOK:
		p.f("  OK: differences between policies are indicative, not exact.\n")
	case calibrationUninformative:
		p.f("  UNINFORMATIVE: neither the log nor the simulation ran out of credentials, so the modeled\n")
		p.f("  exhaustion timing is untested; only the perished columns can be compared.\n")
	default:
		p.f("  FAILED: the model does not reproduce the log; do not base the decision on this table.\n")
	}
	fallback := 0
	for _, estimate := range res.Costs {
		if estimate.Fallback {
			fallback++
		}
	}
	p.f("Cost per request: %d credential windows observed, %d using the provider median.\n", len(res.Costs)-fallback, fallback)
	renderNeeded(p, res.Needed)
}

func renderNeeded(p *printer, needed []string) {
	if len(needed) == 0 {
		return
	}
	p.f("Extra data that would make the replay credible:\n")
	for _, item := range needed {
		p.f("  - %s\n", item)
	}
}

func outcomeColumns(o policyOutcome, actual bool) []string {
	perishedLong, perishedShort := fmtFloat(o.PerishedLong), fmtFloat(o.PerishedShort)
	if actual {
		perishedLong, perishedShort = "see 5", "see 5"
	}
	return []string{
		o.Name, itoa(o.Served), itoa(o.Rejected), itoa(o.Episodes),
		fmtMinutes(o.TotalGapMs), fmtMinutes(o.MaxGapMs), fmtTime(o.FirstRejectMs),
		perishedLong, perishedShort,
	}
}

func fmtTime(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04Z")
}

// fmtDuration renders milliseconds with a unit that keeps two significant figures.
func fmtDuration(ms int64) string {
	sign := ""
	if ms < 0 {
		sign, ms = "-", -ms
	}
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Minute:
		return sign + strconv.FormatFloat(d.Seconds(), 'f', 0, 64) + "s"
	case d < time.Hour:
		return sign + strconv.FormatFloat(d.Minutes(), 'f', 1, 64) + "m"
	case d < 48*time.Hour:
		return sign + strconv.FormatFloat(d.Hours(), 'f', 1, 64) + "h"
	default:
		return sign + strconv.FormatFloat(d.Hours()/24, 'f', 1, 64) + "d"
	}
}

func fmtMinutes(ms int64) string {
	return strconv.FormatFloat(float64(ms)/float64(time.Minute.Milliseconds()), 'f', 1, 64)
}

func fmtRatio(r ratio) string {
	if r.Total == 0 {
		return "0"
	}
	return fmt.Sprintf("%d (agree %s, %d/%d)", r.Total, fmtShare(r.Hit, r.Total), r.Hit, r.Total)
}

func fmtHits(r ratio) string {
	return fmt.Sprintf("%d of %d (%s)", r.Hit, r.Total, fmtShare(r.Hit, r.Total))
}

func fmtShare(part, total int) string {
	if total == 0 {
		return "-"
	}
	return strconv.FormatFloat(float64(part)*100/float64(total), 'f', 1, 64) + "%"
}

func fmtMean(sum float64, count int) string {
	if count == 0 {
		return "-"
	}
	return fmtFloat(sum / float64(count))
}

func fmtFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 2, 64)
}

func fmtUsed(in windowInstance) string {
	if !in.HasUsed {
		return "-"
	}
	return fmtFloat(in.LastUsed)
}

func fmtPeak(in windowInstance) string {
	if !in.HasUsed {
		return "-"
	}
	return fmtFloat(in.PeakUsed)
}

// summarizeSigned returns min, median, and max as durations.
func summarizeSigned(values []int64) [3]string {
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return [3]string{fmtDuration(sorted[0]), fmtDuration(sorted[len(sorted)/2]), fmtDuration(sorted[len(sorted)-1])}
}

func sumInt64(values []int64) int64 {
	var total int64
	for _, value := range values {
		total += value
	}
	return total
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
