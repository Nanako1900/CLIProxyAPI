package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/MoeForge/moego/ojson"
)

// window is one quota window as rendered by the shadow recorder.
type window struct {
	Name      string
	Scope     string
	Used      float64
	HasUsed   bool
	ResetAtMs int64
	DurationS int64
	Status    string
}

// softState is the shadow view of whether the real pick was soft-limited.
type softState int

const (
	softUnknown softState = iota
	softNo
	softYes
)

// selection is the compact form of a `selection` record. Candidate details are
// folded into the fields the report needs so large logs stay small in memory.
type selection struct {
	TsMs                int64
	Model               string
	Pool                string
	Attempt             int
	Actual              string
	ActualProvider      string
	Shadow              string
	Reason              string
	Agree               bool
	InPool              bool
	ActualSoft          softState
	SoftFreeAlternative bool
}

// selectionError is a `selection_error` record.
type selectionError struct {
	TsMs          int64
	Model         string
	Pool          string
	Attempt       int
	Status        int
	RetryAfterMs  int64
	HasRetryAfter bool
}

// observation is an `observation` record. Windows is empty when the result did
// not refresh the credential's quota snapshot.
type observation struct {
	TsMs         int64
	Auth         string
	Provider     string
	Model        string
	Success      bool
	Status       int
	ObservedAtMs int64
	Windows      []window
}

// snapshot is a credential's quota windows at one observation time.
type snapshot struct {
	ObservedAtMs int64
	Windows      []window
}

// credential collects per-credential facts seen anywhere in the log.
type credential struct {
	ID          string
	Provider    string
	Priority    int
	priorityTs  int64
	HasPriority bool
	Routes      map[string]struct{}
	Initial     *snapshot
}

// dataset is the parsed log, with every record list sorted by timestamp.
type dataset struct {
	Files        []string
	Lines        int
	Malformed    int
	Unknown      int
	MaxDropped   uint64
	FirstMs      int64
	LastMs       int64
	Selections   []selection
	Errors       []selectionError
	Observations []observation
	Credentials  map[string]*credential
}

// routeKey identifies the credential pool a request was routed against.
func routeKey(pool, model string) string {
	return pool + " " + model
}

func newDataset() *dataset {
	return &dataset{Credentials: make(map[string]*credential)}
}

// loadDataset reads the given files in order. A missing file is an error; use
// resolveInputs to skip an absent rotation file.
func loadDataset(paths []string) (*dataset, error) {
	ds := newDataset()
	for _, path := range paths {
		if errRead := ds.readFile(path); errRead != nil {
			return nil, errRead
		}
	}
	ds.finish()
	return ds, nil
}

func (ds *dataset) readFile(path string) (err error) {
	file, errOpen := os.Open(path)
	if errOpen != nil {
		return fmt.Errorf("open %s: %w", path, errOpen)
	}
	defer func() {
		if errClose := file.Close(); errClose != nil && err == nil {
			err = fmt.Errorf("close %s: %w", path, errClose)
		}
	}()
	ds.Files = append(ds.Files, path)
	if errParse := ds.read(file); errParse != nil {
		return fmt.Errorf("read %s: %w", path, errParse)
	}
	return nil
}

// read consumes JSONL from r. Lines are unbounded in length because a
// selection can list up to 128 candidates with their windows.
func (ds *dataset) read(r io.Reader) error {
	reader := bufio.NewReaderSize(r, 1<<20)
	for {
		line, errRead := reader.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			ds.addLine(trimmed)
		}
		if errors.Is(errRead, io.EOF) {
			return nil
		}
		if errRead != nil {
			return errRead
		}
	}
}

func (ds *dataset) addLine(line []byte) {
	ds.Lines++
	if !ojson.ValidBytes(line) {
		ds.Malformed++
		return
	}
	root := ojson.ParseBytes(line)
	if !root.IsObject() {
		ds.Malformed++
		return
	}
	ts := root.Get("ts_ms").Int()
	if ts <= 0 {
		ds.Malformed++
		return
	}
	switch root.Get("type").String() {
	case "selection":
		ds.addSelection(root, ts)
	case "selection_error":
		ds.addSelectionError(root, ts)
	case "observation":
		ds.addObservation(root, ts)
	default:
		ds.Unknown++
		return
	}
	if dropped := root.Get("dropped").Uint(); dropped > ds.MaxDropped {
		ds.MaxDropped = dropped
	}
	if ds.FirstMs == 0 || ts < ds.FirstMs {
		ds.FirstMs = ts
	}
	if ts > ds.LastMs {
		ds.LastMs = ts
	}
}

func (ds *dataset) addSelection(root ojson.Result, ts int64) {
	sel := selection{
		TsMs:           ts,
		Model:          root.Get("model").String(),
		Pool:           joinProviders(root.Get("providers")),
		Attempt:        int(root.Get("attempt").Int()),
		Actual:         root.Get("actual").String(),
		ActualProvider: root.Get("actual_provider").String(),
		Shadow:         root.Get("shadow").String(),
		Reason:         root.Get("shadow_reason").String(),
		Agree:          root.Get("agree").Bool(),
		InPool:         root.Get("actual_in_pool").Bool(),
	}
	route := routeKey(sel.Pool, sel.Model)
	for _, item := range root.Get("candidates").Array() {
		id := item.Get("id").String()
		if id == "" {
			continue
		}
		soft := item.Get("soft_limited").Bool()
		if id == sel.Actual {
			sel.ActualSoft = softNo
			if soft {
				sel.ActualSoft = softYes
			}
		} else if !soft {
			sel.SoftFreeAlternative = true
		}
		cred := ds.credential(id, item.Get("provider").String())
		cred.Routes[route] = struct{}{}
		if priority := item.Get("priority"); priority.Exists() && ts >= cred.priorityTs {
			cred.Priority = int(priority.Int())
			cred.priorityTs = ts
			cred.HasPriority = true
		}
		cred.offerSnapshot(item.Get("observed_at_ms").Int(), parseWindows(item.Get("windows")))
	}
	if sel.Actual != "" {
		ds.credential(sel.Actual, sel.ActualProvider).Routes[route] = struct{}{}
	}
	ds.Selections = append(ds.Selections, sel)
}

func (ds *dataset) addSelectionError(root ojson.Result, ts int64) {
	rec := selectionError{
		TsMs:    ts,
		Model:   root.Get("model").String(),
		Pool:    joinProviders(root.Get("providers")),
		Attempt: int(root.Get("attempt").Int()),
		Status:  int(root.Get("status").Int()),
	}
	if retry := root.Get("retry_after_ms"); retry.Exists() {
		rec.RetryAfterMs = retry.Int()
		rec.HasRetryAfter = true
	}
	ds.Errors = append(ds.Errors, rec)
}

func (ds *dataset) addObservation(root ojson.Result, ts int64) {
	obs := observation{
		TsMs:         ts,
		Auth:         root.Get("auth").String(),
		Provider:     root.Get("provider").String(),
		Model:        root.Get("model").String(),
		Success:      root.Get("success").Bool(),
		Status:       int(root.Get("status").Int()),
		ObservedAtMs: root.Get("observed_at_ms").Int(),
		Windows:      parseWindows(root.Get("windows")),
	}
	if obs.Auth != "" {
		ds.credential(obs.Auth, obs.Provider).offerSnapshot(obs.ObservedAtMs, obs.Windows)
	}
	ds.Observations = append(ds.Observations, obs)
}

func (ds *dataset) credential(id, provider string) *credential {
	cred := ds.Credentials[id]
	if cred == nil {
		cred = &credential{ID: id, Routes: make(map[string]struct{})}
		ds.Credentials[id] = cred
	}
	if cred.Provider == "" && provider != "" {
		cred.Provider = provider
	}
	return cred
}

// offerSnapshot keeps the earliest windows snapshot as the replay's initial state.
func (c *credential) offerSnapshot(observedAtMs int64, windows []window) {
	if observedAtMs <= 0 || len(windows) == 0 {
		return
	}
	if c.Initial != nil && c.Initial.ObservedAtMs <= observedAtMs {
		return
	}
	c.Initial = &snapshot{ObservedAtMs: observedAtMs, Windows: windows}
}

func (ds *dataset) finish() {
	sort.SliceStable(ds.Selections, func(i, j int) bool { return ds.Selections[i].TsMs < ds.Selections[j].TsMs })
	sort.SliceStable(ds.Errors, func(i, j int) bool { return ds.Errors[i].TsMs < ds.Errors[j].TsMs })
	sort.SliceStable(ds.Observations, func(i, j int) bool { return ds.Observations[i].TsMs < ds.Observations[j].TsMs })
}

func parseWindows(list ojson.Result) []window {
	items := list.Array()
	if len(items) == 0 {
		return nil
	}
	out := make([]window, 0, len(items))
	for _, item := range items {
		w := window{
			Name:      item.Get("name").String(),
			Scope:     item.Get("scope").String(),
			ResetAtMs: item.Get("reset_at_ms").Int(),
			DurationS: item.Get("duration_s").Int(),
			Status:    item.Get("status").String(),
		}
		if used := item.Get("used"); used.Exists() {
			w.Used = used.Float()
			w.HasUsed = true
		}
		if w.Name != "" {
			out = append(out, w)
		}
	}
	return out
}

func joinProviders(list ojson.Result) string {
	items := list.Array()
	names := make([]string, 0, len(items))
	for _, item := range items {
		if name := strings.TrimSpace(item.String()); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, "+")
}
