package main

import (
	"sort"
	"time"
)

// routeOutage summarizes outage episodes of one route (providers + model).
// An episode starts at a failed pick and ends at the next successful pick on
// the same route; its gap is the time between the two.
type routeOutage struct {
	Route      string
	Errors     int
	Episodes   int
	Open       int
	TotalGapMs int64
	MaxGapMs   int64
}

// episodeTracker turns a chronological stream of served/failed picks into
// outage episodes. Episodes still open at the end are closed at their last
// failure, so their gap is a lower bound.
type episodeTracker struct {
	start   map[string]int64
	lastErr map[string]int64
	routes  map[string]*routeOutage
}

func newEpisodeTracker() *episodeTracker {
	return &episodeTracker{
		start:   make(map[string]int64),
		lastErr: make(map[string]int64),
		routes:  make(map[string]*routeOutage),
	}
}

func (t *episodeTracker) route(route string) *routeOutage {
	stats := t.routes[route]
	if stats == nil {
		stats = &routeOutage{Route: route}
		t.routes[route] = stats
	}
	return stats
}

func (t *episodeTracker) fail(route string, ts int64) {
	stats := t.route(route)
	stats.Errors++
	if _, open := t.start[route]; !open {
		t.start[route] = ts
		stats.Episodes++
	}
	t.lastErr[route] = ts
}

func (t *episodeTracker) serve(route string, ts int64) {
	start, open := t.start[route]
	if !open {
		return
	}
	t.closeEpisode(route, ts-start)
}

func (t *episodeTracker) closeEpisode(route string, gap int64) {
	stats := t.route(route)
	if gap < 0 {
		gap = 0
	}
	stats.TotalGapMs += gap
	if gap > stats.MaxGapMs {
		stats.MaxGapMs = gap
	}
	delete(t.start, route)
	delete(t.lastErr, route)
}

// finish closes open episodes and returns per-route stats sorted by total gap.
func (t *episodeTracker) finish() []routeOutage {
	for route, start := range t.start {
		t.route(route).Open++
		t.closeEpisode(route, t.lastErr[route]-start)
	}
	out := make([]routeOutage, 0, len(t.routes))
	for _, stats := range t.routes {
		if stats.Errors > 0 {
			out = append(out, *stats)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalGapMs != out[j].TotalGapMs {
			return out[i].TotalGapMs > out[j].TotalGapMs
		}
		return out[i].Route < out[j].Route
	})
	return out
}

// outageTotals sums per-route episodes.
type outageTotals struct {
	Errors     int
	Episodes   int
	Open       int
	TotalGapMs int64
	MaxGapMs   int64
}

func sumOutages(routes []routeOutage) outageTotals {
	var totals outageTotals
	for _, stats := range routes {
		totals.Errors += stats.Errors
		totals.Episodes += stats.Episodes
		totals.Open += stats.Open
		totals.TotalGapMs += stats.TotalGapMs
		if stats.MaxGapMs > totals.MaxGapMs {
			totals.MaxGapMs = stats.MaxGapMs
		}
	}
	return totals
}

// countEntry is one value of a frequency table.
type countEntry struct {
	Label string
	Count int
}

// distribution summarizes a set of durations.
type distribution struct {
	Count   int
	Min     int64
	P50     int64
	P90     int64
	Max     int64
	Buckets []countEntry
}

// outageStats covers report section 3.
type outageStats struct {
	outageTotals
	FirstAttemptErrors int
	Routes             []routeOutage
	Statuses           []countEntry
	RetryAfter         distribution
}

func analyzeOutages(selections []selection, errs []selectionError) outageStats {
	tracker := newEpisodeTracker()
	i, j := 0, 0
	for i < len(selections) || j < len(errs) {
		// On equal timestamps the failure is applied first so a same-instant
		// success closes the episode instead of leaving it open.
		if j < len(errs) && (i >= len(selections) || errs[j].TsMs <= selections[i].TsMs) {
			tracker.fail(routeKey(errs[j].Pool, errs[j].Model), errs[j].TsMs)
			j++
			continue
		}
		tracker.serve(routeKey(selections[i].Pool, selections[i].Model), selections[i].TsMs)
		i++
	}
	stats := outageStats{Routes: tracker.finish()}
	stats.outageTotals = sumOutages(stats.Routes)
	statuses := make(map[int]int)
	retries := make([]int64, 0, len(errs))
	for _, rec := range errs {
		if rec.Attempt == 0 {
			stats.FirstAttemptErrors++
		}
		statuses[rec.Status]++
		if rec.HasRetryAfter {
			retries = append(retries, rec.RetryAfterMs)
		}
	}
	stats.Statuses = statusCounts(statuses)
	stats.RetryAfter = summarizeDurations(retries)
	return stats
}

func statusCounts(statuses map[int]int) []countEntry {
	codes := make([]int, 0, len(statuses))
	for code := range statuses {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	out := make([]countEntry, 0, len(codes))
	for _, code := range codes {
		label := "none"
		if code != 0 {
			label = itoa(code)
		}
		out = append(out, countEntry{Label: label, Count: statuses[code]})
	}
	return out
}

// retryBuckets are upper bounds (exclusive) for the retry-after histogram.
var retryBuckets = []struct {
	label string
	upper time.Duration
}{
	{"< 1m", time.Minute},
	{"1m-5m", 5 * time.Minute},
	{"5m-30m", 30 * time.Minute},
	{"30m-1h", time.Hour},
	{"1h-5h", 5 * time.Hour},
	{"5h-24h", 24 * time.Hour},
	{">= 24h", 0},
}

func summarizeDurations(values []int64) distribution {
	dist := distribution{Count: len(values)}
	if len(values) == 0 {
		return dist
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	dist.Min = sorted[0]
	dist.Max = sorted[len(sorted)-1]
	dist.P50 = percentile(sorted, 50)
	dist.P90 = percentile(sorted, 90)
	counts := make([]int, len(retryBuckets))
	for _, value := range sorted {
		counts[bucketIndex(time.Duration(value)*time.Millisecond)]++
	}
	for index, bucket := range retryBuckets {
		dist.Buckets = append(dist.Buckets, countEntry{Label: bucket.label, Count: counts[index]})
	}
	return dist
}

func bucketIndex(value time.Duration) int {
	for index, bucket := range retryBuckets {
		if bucket.upper == 0 || value < bucket.upper {
			return index
		}
	}
	return len(retryBuckets) - 1
}

// percentile uses the nearest-rank method on an ascending slice.
func percentile(sorted []int64, p int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := (p*len(sorted) + 99) / 100
	if rank < 1 {
		rank = 1
	}
	return sorted[rank-1]
}
