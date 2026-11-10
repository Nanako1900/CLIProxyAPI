package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	// calibrationTolerance is the largest difference in rejected requests, as a
	// share of demand, between the simulated configured strategy and the log for
	// the replay to count as reproducing reality.
	calibrationTolerance = 0.02
	// shareTolerance is the largest total variation distance between the
	// simulated and the logged per-credential share of first picks.
	shareTolerance = 0.10
)

// calibrationState says whether the simulated configured strategy reproduces the log.
type calibrationState int

const (
	calibrationFailed calibrationState = iota
	// calibrationUninformative: neither the log nor the simulation ran out of
	// credentials, so the modeled exhaustion timing was never tested.
	calibrationUninformative
	calibrationOK
)

// replayOptions configures the policy replay.
type replayOptions struct {
	Strategy       string
	SoftLimit      float64
	MinCostSamples int
}

// costEstimate is the simulated per-request cost of one credential window.
type costEstimate struct {
	Auth     string
	Window   string
	Cost     float64
	Samples  int
	Fallback bool
}

// replayResult covers report section 6.
type replayResult struct {
	Ran            bool
	Skipped        []string
	Excluded       []string
	Needed         []string
	Assumptions    []string
	Demand         int
	Routes         int
	Credentials    int
	Actual         policyOutcome
	Policies       []policyOutcome
	Strategy       string
	Calibration    calibrationState
	CalibrationGap float64
	ShareDistance  float64
	Costs          []costEstimate
}

// replayModel is everything needed to build a fresh simulation per policy.
type replayModel struct {
	startMs   int64
	endMs     int64
	demand    []demandEvent
	eligible  map[string][]string
	creds     []*credential
	types     map[string][]windowTypeSummary
	costs     map[string]map[string]float64
	softLimit float64
}

func runReplay(ds *dataset, windows windowReport, opts replayOptions) replayResult {
	res := replayResult{Strategy: opts.Strategy}
	demand, routeProviders := collectDemand(ds)
	if len(demand) == 0 {
		res.Skipped = append(res.Skipped, "no requests recorded (no attempt-0 selection or selection_error records)")
		res.Needed = neededData(res.Skipped)
		return res
	}
	types := credentialWideTypes(windows)
	observed, medians := estimateCosts(ds, windows, opts.MinCostSamples)
	providerOK, excluded := gateProviders(routeProviders, types, medians, opts.MinCostSamples)
	res.Excluded = excluded
	model := replayModel{
		eligible:  make(map[string][]string),
		types:     types,
		costs:     make(map[string]map[string]float64),
		softLimit: opts.SoftLimit,
		endMs:     ds.LastMs,
	}
	included := make(map[string]bool)
	for route, providers := range routeProviders {
		if allProvidersOK(providers, providerOK) {
			included[route] = true
		}
	}
	for _, event := range demand {
		if included[event.Route] {
			model.demand = append(model.demand, event)
		}
	}
	if len(model.demand) == 0 {
		res.Skipped = append(res.Skipped, "no route has every provider passing the checks above")
		res.Skipped = append(res.Skipped, res.Excluded...)
		res.Excluded = nil
		res.Needed = neededData(res.Skipped)
		return res
	}
	model.startMs = model.demand[0].TsMs
	res.Costs = model.assignCredentials(ds, included, providerOK, observed, medians)
	res.Ran = true
	res.Demand = len(model.demand)
	res.Routes = len(model.eligible)
	res.Credentials = len(model.creds)
	res.Actual = actualOutcome(ds, included, len(model.demand))
	for _, policy := range []string{policyRoundRobin, policyFillFirst, policyShadow} {
		res.Policies = append(res.Policies, model.simulate(policy))
	}
	res.calibrateAgainst(opts.Strategy)
	res.Assumptions = replayAssumptions(types, providerOK)
	if len(res.Excluded) > 0 {
		res.Needed = neededData(res.Excluded)
	}
	return res
}

// gateProviders returns the providers that can be replayed and why the others cannot.
func gateProviders(routes map[string][]string, types map[string][]windowTypeSummary, medians map[string]map[string]float64, minSamples int) (map[string]bool, []string) {
	ok := make(map[string]bool)
	var excluded []string
	for _, provider := range sortedProviders(routes) {
		if reason := providerGate(provider, types[provider], medians[provider], minSamples); reason != "" {
			excluded = append(excluded, reason)
			continue
		}
		ok[provider] = true
	}
	return ok, excluded
}

// calibrateAgainst compares the simulated configured strategy with the log.
func (res *replayResult) calibrateAgainst(strategy string) {
	for _, outcome := range res.Policies {
		if outcome.Name != strategy {
			continue
		}
		res.CalibrationGap = math.Abs(float64(outcome.Rejected-res.Actual.Rejected)) / float64(res.Demand)
		res.ShareDistance = shareDistance(outcome.ServedBy, res.Actual.ServedBy)
		res.Calibration = calibrate(res.CalibrationGap, res.ShareDistance, outcome.Rejected, res.Actual.Rejected)
	}
}

func calibrate(gap, distance float64, simRejected, actualRejected int) calibrationState {
	switch {
	case gap > calibrationTolerance || distance > shareTolerance:
		return calibrationFailed
	case simRejected == 0 && actualRejected == 0:
		return calibrationUninformative
	default:
		return calibrationOK
	}
}

// shareDistance is the total variation distance between two count distributions.
func shareDistance(a, b map[string]int) float64 {
	totalA, totalB := 0, 0
	for _, count := range a {
		totalA += count
	}
	for _, count := range b {
		totalB += count
	}
	if totalA == 0 || totalB == 0 {
		if totalA == totalB {
			return 0
		}
		return 1
	}
	distance := 0.0
	for id, count := range a {
		distance += math.Abs(float64(count)/float64(totalA) - float64(b[id])/float64(totalB))
	}
	for id, count := range b {
		if _, seen := a[id]; !seen {
			distance += float64(count) / float64(totalB)
		}
	}
	return distance / 2
}

// collectDemand returns request arrivals (attempt 0 picks and failures) and
// the providers of every route they used.
func collectDemand(ds *dataset) ([]demandEvent, map[string][]string) {
	var demand []demandEvent
	routes := make(map[string][]string)
	addRoute := func(pool, model string) string {
		route := routeKey(pool, model)
		if _, ok := routes[route]; !ok {
			routes[route] = strings.Split(pool, "+")
		}
		return route
	}
	for _, sel := range ds.Selections {
		if sel.Attempt == 0 {
			demand = append(demand, demandEvent{TsMs: sel.TsMs, Route: addRoute(sel.Pool, sel.Model), Actual: sel.Actual})
		}
	}
	for _, rec := range ds.Errors {
		if rec.Attempt == 0 {
			demand = append(demand, demandEvent{TsMs: rec.TsMs, Route: addRoute(rec.Pool, rec.Model)})
		}
	}
	sort.SliceStable(demand, func(i, j int) bool { return demand[i].TsMs < demand[j].TsMs })
	return demand, routes
}

func credentialWideTypes(windows windowReport) map[string][]windowTypeSummary {
	types := make(map[string][]windowTypeSummary)
	for _, summary := range windows.Types {
		if summary.Scope == "" {
			types[summary.Provider] = append(types[summary.Provider], summary)
		}
	}
	return types
}

// estimateCosts returns observed per-request costs by credential and window,
// and per provider the median observed cost by window name. Only positive
// costs count: a zero estimate (usage below the reporting precision) would
// make a credential inexhaustible in the simulation.
func estimateCosts(ds *dataset, windows windowReport, minSamples int) (map[string]map[string]costEstimate, map[string]map[string]float64) {
	observed := make(map[string]map[string]costEstimate)
	samples := make(map[string]map[string][]float64)
	for _, series := range windows.Series {
		if series.Scope != "" || series.CostPairs < minSamples {
			continue
		}
		cost := series.CostDelta / float64(series.CostPairs)
		if cost <= 0 {
			continue
		}
		if observed[series.Auth] == nil {
			observed[series.Auth] = make(map[string]costEstimate)
		}
		observed[series.Auth][series.Name] = costEstimate{Auth: series.Auth, Window: series.Name, Cost: cost, Samples: series.CostPairs}
		provider := series.Provider
		if cred := ds.Credentials[series.Auth]; cred != nil && cred.Provider != "" {
			provider = cred.Provider
		}
		if samples[provider] == nil {
			samples[provider] = make(map[string][]float64)
		}
		samples[provider][series.Name] = append(samples[provider][series.Name], cost)
	}
	medians := make(map[string]map[string]float64)
	for provider, byName := range samples {
		medians[provider] = make(map[string]float64)
		for name, values := range byName {
			sort.Float64s(values)
			medians[provider][name] = values[len(values)/2]
		}
	}
	return observed, medians
}

// providerGate returns why a provider cannot be replayed, or "".
func providerGate(provider string, types []windowTypeSummary, medians map[string]float64, minSamples int) string {
	if len(types) == 0 {
		return fmt.Sprintf("%s: no credential-wide quota windows observed", provider)
	}
	for _, summary := range types {
		label := provider + "/" + summary.Name
		switch {
		case !summary.Semantics.fixed() || !summary.Semantics.DriftVerified:
			return fmt.Sprintf("%s: window behavior is %s", label, summary.Semantics)
		case summary.NominalS() <= 0:
			return fmt.Sprintf("%s: window length unknown", label)
		}
		if _, ok := medians[summary.Name]; !ok {
			return fmt.Sprintf("%s: no credential has %d consecutive observations in one window with a measurable usage increase, so the cost per request is unknown", label, minSamples)
		}
	}
	return ""
}

func allProvidersOK(providers []string, ok map[string]bool) bool {
	for _, provider := range providers {
		if !ok[provider] {
			return false
		}
	}
	return len(providers) > 0
}

func sortedProviders(routes map[string][]string) []string {
	set := make(map[string]struct{})
	for _, providers := range routes {
		for _, provider := range providers {
			set[provider] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for provider := range set {
		out = append(out, provider)
	}
	sort.Strings(out)
	return out
}

// assignCredentials fixes the eligible credentials per route and the cost of
// every simulated window, falling back to the provider median.
func (m *replayModel) assignCredentials(ds *dataset, included, providerOK map[string]bool, observed map[string]map[string]costEstimate, medians map[string]map[string]float64) []costEstimate {
	ids := make([]string, 0, len(ds.Credentials))
	for id := range ds.Credentials {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var costs []costEstimate
	for _, id := range ids {
		cred := ds.Credentials[id]
		if !providerOK[cred.Provider] {
			continue
		}
		used := false
		for route := range cred.Routes {
			if included[route] {
				m.eligible[route] = append(m.eligible[route], id)
				used = true
			}
		}
		if !used {
			continue
		}
		m.creds = append(m.creds, cred)
		m.costs[id] = make(map[string]float64)
		for _, summary := range m.types[cred.Provider] {
			estimate, ok := observed[id][summary.Name]
			if !ok {
				estimate = costEstimate{Auth: id, Window: summary.Name, Cost: medians[cred.Provider][summary.Name], Fallback: true}
			}
			m.costs[id][summary.Name] = estimate.Cost
			costs = append(costs, estimate)
		}
	}
	for route := range m.eligible {
		sort.Strings(m.eligible[route])
	}
	return costs
}

func (m *replayModel) simulate(policy string) policyOutcome {
	sim := &simulation{
		policy:    policy,
		softLimit: m.softLimit,
		creds:     make(map[string]*simCred, len(m.creds)),
		eligible:  m.eligible,
		lastPick:  make(map[string]string),
	}
	for _, cred := range m.creds {
		sim.creds[cred.ID] = m.initialState(cred)
	}
	return sim.run(m.demand, m.endMs)
}

// initialState starts a credential from its earliest snapshot observed before
// the first request, or with every window idle when there is none.
func (m *replayModel) initialState(cred *credential) *simCred {
	state := &simCred{id: cred.ID, priority: cred.Priority}
	for _, summary := range m.types[cred.Provider] {
		durMs := summary.NominalS() * 1000
		w := &simWindow{
			name:     summary.Name,
			durMs:    durMs,
			long:     durMs > shortWindowCutoff.Milliseconds(),
			anchored: summary.Semantics.Kind == semFixedAnchored,
			cost:     m.costs[cred.ID][summary.Name],
		}
		// A snapshot taken at or after the first request already includes replayed usage.
		if cred.Initial != nil && cred.Initial.ObservedAtMs < m.startMs {
			for _, observedWindow := range cred.Initial.Windows {
				if observedWindow.Scope == "" && observedWindow.Name == summary.Name {
					m.seedWindow(w, observedWindow)
				}
			}
		}
		state.windows = append(state.windows, w)
	}
	return state
}

func (m *replayModel) seedWindow(w *simWindow, observed window) {
	if observed.ResetAtMs <= 0 {
		return
	}
	if observed.ResetAtMs > m.startMs {
		w.resetMs = observed.ResetAtMs
		w.used = clampUnit(observed.Used)
		return
	}
	if w.anchored {
		cycles := (m.startMs-observed.ResetAtMs)/w.durMs + 1
		w.resetMs = observed.ResetAtMs + cycles*w.durMs
	}
}

// actualOutcome measures the log itself on the replayed routes. A request
// ends with at most one selection_error, whatever its attempt, so the errors
// count the requests that ended without a credential, which is what a
// simulated rejection is. Failover after an upstream rejection is not
// simulated because simulated credentials are never picked while exhausted.
func actualOutcome(ds *dataset, included map[string]bool, demand int) policyOutcome {
	servedBy := make(map[string]int)
	var selections []selection
	for _, sel := range ds.Selections {
		if !included[routeKey(sel.Pool, sel.Model)] {
			continue
		}
		selections = append(selections, sel)
		if sel.Attempt == 0 && sel.Actual != "" {
			servedBy[sel.Actual]++
		}
	}
	var errs []selectionError
	for _, rec := range ds.Errors {
		if included[routeKey(rec.Pool, rec.Model)] {
			errs = append(errs, rec)
		}
	}
	outages := analyzeOutages(selections, errs)
	out := policyOutcome{Name: "actual (log)", Rejected: len(errs), outageTotals: outages.outageTotals, ServedBy: servedBy}
	out.Served = max(demand-out.Rejected, 0)
	if len(errs) > 0 {
		out.FirstRejectMs = errs[0].TsMs
	}
	return out
}

func replayAssumptions(types map[string][]windowTypeSummary, providerOK map[string]bool) []string {
	assumptions := []string{
		"Demand is the first pick (attempt 0) of every recorded request at its recorded time; client retries during real outages count as extra requests.",
		"A request costs the credential's mean observed used-share increase per request in each window (provider median when unobserved); request size, prompt-cache loss when a session moves to another credential, and session affinity are not modeled.",
		"A credential is unavailable from the moment any credential-wide window reaches 100% until that window resets; cooldowns from other errors are not modeled.",
		"Usage outside this proxy is not modeled; credentials start from their earliest snapshot taken before the first request, otherwise with idle windows.",
	}
	var firstUse []string
	providers := make([]string, 0, len(providerOK))
	for provider := range providerOK {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	for _, provider := range providers {
		for _, summary := range types[provider] {
			if summary.Semantics.Kind == semFixedUndetermined || summary.Semantics.Kind == semFixedMixed {
				firstUse = append(firstUse, provider+"/"+summary.Name)
			}
		}
	}
	if len(firstUse) > 0 {
		assumptions = append(assumptions, "Simulated as starting on first use although the start rule was not determined: "+strings.Join(firstUse, ", ")+".")
	}
	return assumptions
}

// neededData explains what extra recording would make a replay possible.
func neededData(reasons []string) []string {
	needed := []string{
		"Per-request quota cost on the credential that served it (input, output, and cache tokens, or the used-share change attributable to that request); observations only show coarse used fractions, and moving a request changes its cache hits.",
		"A request ID shared by selection, failover, selection_error, and observation records, so client retries can be told apart from new demand.",
	}
	text := strings.Join(reasons, " ")
	if strings.Contains(text, "rolling") || strings.Contains(text, "unclear") || strings.Contains(text, "insufficient") || strings.Contains(text, "unverified") {
		needed = append(needed, "Window behavior verified as fixed over longer periods (section 4); a rolling window needs per-request usage timestamps so old usage can age out in the simulation.")
	}
	if strings.Contains(text, "cost per request is unknown") {
		needed = append(needed, "More consecutive observations of each credential inside one window, so the cost per request (which reflects the plan size) can be estimated.")
	}
	if strings.Contains(text, "no credential-wide quota windows") || strings.Contains(text, "length unknown") {
		needed = append(needed, "Quota headers on responses for every provider in the pool (window usage, reset time, and window length).")
	}
	return needed
}
