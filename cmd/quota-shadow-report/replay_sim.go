package main

import (
	"math"
	"sort"
	"time"
)

const (
	policyRoundRobin = "round-robin"
	policyFillFirst  = "fill-first"
	policyShadow     = "shadow"

	// shortWindowCutoff and shadowFloatEpsilon mirror the live shadow policy.
	shortWindowCutoff  = 24 * time.Hour
	shadowFloatEpsilon = 0.01
)

// simWindow is the simulated state of one credential-wide window.
type simWindow struct {
	name     string
	durMs    int64
	long     bool
	anchored bool
	cost     float64
	// resetMs is zero while the window is not running (first-use windows
	// between a reset and the next request).
	resetMs int64
	used    float64
}

// simCred is one simulated credential.
type simCred struct {
	id       string
	priority int
	windows  []*simWindow
}

// demandEvent is one request arrival: the first pick attempt of a request.
// Actual is the credential the log picked, empty when the pick failed.
type demandEvent struct {
	TsMs   int64
	Route  string
	Actual string
}

// policyOutcome is the result of serving the demand with one policy.
type policyOutcome struct {
	Name     string
	Served   int
	Rejected int
	outageTotals
	PerishedLong  float64
	ResetsLong    int
	PerishedShort float64
	ResetsShort   int
	FirstRejectMs int64
	// ServedBy counts first picks per credential.
	ServedBy map[string]int
}

func (o *policyOutcome) recordReset(w *simWindow, perished float64) {
	if w.long {
		o.PerishedLong += perished
		o.ResetsLong++
		return
	}
	o.PerishedShort += perished
	o.ResetsShort++
}

// advance applies every reset due by ts. Anchored windows roll forward by whole
// window lengths; a cycle that passes without any use perishes completely.
func (c *simCred) advance(ts int64, out *policyOutcome) {
	for _, w := range c.windows {
		for w.resetMs > 0 && w.resetMs <= ts {
			if out != nil {
				out.recordReset(w, clampUnit(1-w.used))
			}
			w.used = 0
			if w.anchored {
				w.resetMs += w.durMs
			} else {
				w.resetMs = 0
			}
		}
	}
}

func (c *simCred) available() bool {
	for _, w := range c.windows {
		if w.resetMs > 0 && w.used >= 1-1e-9 {
			return false
		}
	}
	return true
}

func (c *simCred) serve(ts int64) {
	for _, w := range c.windows {
		if w.resetMs == 0 {
			w.resetMs = ts + w.durMs
		}
		w.used += w.cost
	}
}

// shadowScore mirrors the live shadow evaluation on simulated windows.
type shadowScore struct {
	id            string
	soft          bool
	perishLong    float64
	perishShort   float64
	shortActive   bool
	longRemaining float64
}

func (c *simCred) score(ts int64, softLimit float64) shadowScore {
	score := shadowScore{id: c.id, longRemaining: 1}
	for _, w := range c.windows {
		running := w.resetMs > ts
		remaining := 1.0
		if running {
			remaining = clampUnit(1 - w.used)
		}
		if remaining <= 1-softLimit+1e-9 {
			score.soft = true
		}
		perish := 0.0
		if running {
			untilReset := min(w.resetMs-ts, w.durMs)
			perish = clampUnit(remaining - float64(untilReset)/float64(w.durMs))
		}
		if !w.long {
			score.perishShort = math.Max(score.perishShort, perish)
			if running && w.used > 0 {
				score.shortActive = true
			}
			continue
		}
		score.perishLong = math.Max(score.perishLong, perish)
		score.longRemaining = math.Min(score.longRemaining, remaining)
	}
	return score
}

func shadowLess(a, b shadowScore) bool {
	if a.soft != b.soft {
		return !a.soft
	}
	if decided, less := descending(a.perishLong, b.perishLong); decided {
		return less
	}
	if decided, less := descending(a.perishShort, b.perishShort); decided {
		return less
	}
	if a.shortActive != b.shortActive {
		return !a.shortActive
	}
	if decided, less := descending(a.longRemaining, b.longRemaining); decided {
		return less
	}
	return a.id < b.id
}

func descending(a, b float64) (decided bool, less bool) {
	if math.Abs(a-b) < shadowFloatEpsilon {
		return false, false
	}
	return true, a > b
}

// simulation replays the demand against one policy.
type simulation struct {
	policy    string
	softLimit float64
	creds     map[string]*simCred
	eligible  map[string][]string
	lastPick  map[string]string
}

func (s *simulation) run(demand []demandEvent, endMs int64) policyOutcome {
	out := policyOutcome{Name: s.policy, ServedBy: make(map[string]int)}
	tracker := newEpisodeTracker()
	for _, event := range demand {
		picked := s.pick(event, &out)
		if picked == nil {
			out.Rejected++
			if out.FirstRejectMs == 0 {
				out.FirstRejectMs = event.TsMs
			}
			tracker.fail(event.Route, event.TsMs)
			continue
		}
		picked.serve(event.TsMs)
		out.Served++
		out.ServedBy[picked.id]++
		tracker.serve(event.Route, event.TsMs)
	}
	ids := make([]string, 0, len(s.creds))
	for id := range s.creds {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s.creds[id].advance(endMs, &out)
	}
	out.outageTotals = sumOutages(tracker.finish())
	return out
}

// pick returns the credential the policy serves the event with, or nil.
// Candidates are the available credentials of the highest priority tier, by ID.
func (s *simulation) pick(event demandEvent, out *policyOutcome) *simCred {
	var available []*simCred
	bestPriority := 0
	for _, id := range s.eligible[event.Route] {
		cred := s.creds[id]
		cred.advance(event.TsMs, out)
		if !cred.available() {
			continue
		}
		switch {
		case len(available) == 0 || cred.priority > bestPriority:
			bestPriority = cred.priority
			available = append(available[:0], cred)
		case cred.priority == bestPriority:
			available = append(available, cred)
		}
	}
	if len(available) == 0 {
		return nil
	}
	switch s.policy {
	case policyFillFirst:
		return available[0]
	case policyShadow:
		best := available[0]
		bestScore := best.score(event.TsMs, s.softLimit)
		for _, cred := range available[1:] {
			if score := cred.score(event.TsMs, s.softLimit); shadowLess(score, bestScore) {
				best, bestScore = cred, score
			}
		}
		return best
	default:
		last := s.lastPick[event.Route]
		index := sort.Search(len(available), func(i int) bool { return available[i].id > last })
		if last == "" || index >= len(available) {
			index = 0
		}
		s.lastPick[event.Route] = available[index].id
		return available[index]
	}
}
