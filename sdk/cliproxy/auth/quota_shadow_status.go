package auth

import (
	"sync"
	"time"

	"github.com/MoeForge/moego/ojson"
)

const quotaShadowRecentLimit = 200

// QuotaShadowEvent is a bounded management projection, not a raw log record.
// It deliberately omits headers, sessions, request bodies, and error text.
type QuotaShadowEvent struct {
	Type         string `json:"type"`
	TimestampMS  int64  `json:"ts_ms"`
	PoolID       string `json:"pool_id"`
	Model        string `json:"model"`
	Actual       string `json:"actual"`
	Shadow       string `json:"shadow"`
	Reason       string `json:"reason"`
	Candidates   int64  `json:"candidates"`
	ActualInPool bool   `json:"actual_in_pool"`
	Agree        bool   `json:"agree"`
	Success      bool   `json:"success"`
	Status       int64  `json:"status"`
}

// QuotaShadowStatus describes one recorder lifetime. It never reads arbitrary files.
type QuotaShadowStatus struct {
	State           string             `json:"state"`
	Since           string             `json:"since"`
	File            string             `json:"file"`
	MaxFileMB       int64              `json:"max_file_mb"`
	SoftLimit       float64            `json:"soft_limit_percent"`
	Processed       uint64             `json:"processed"`
	Written         uint64             `json:"written"`
	WriteErrors     uint64             `json:"write_errors"`
	RenderErrors    uint64             `json:"render_errors"`
	Dropped         uint64             `json:"dropped"`
	QueuePending    int                `json:"queue_pending"`
	QueueCapacity   int                `json:"queue_capacity"`
	Selections      uint64             `json:"selections"`
	Comparable      uint64             `json:"comparable"`
	Agreements      uint64             `json:"agreements"`
	Disagreements   uint64             `json:"disagreements"`
	Structural      uint64             `json:"structural"`
	NoCandidates    uint64             `json:"no_candidates"`
	SelectionErrors uint64             `json:"selection_errors"`
	Observations    uint64             `json:"observations"`
	Successes       uint64             `json:"successes"`
	Failures        uint64             `json:"failures"`
	LastError       string             `json:"last_error"`
	RecentLimit     int                `json:"recent_limit"`
	Recent          []QuotaShadowEvent `json:"recent"`
}

type quotaShadowMonitor struct {
	mu     sync.Mutex
	status QuotaShadowStatus
}

func (r *quotaShadowRecorder) recordStatus(line []byte) {
	value := ojson.ParseBytes(line)
	event := QuotaShadowEvent{
		Type: value.Get("type").String(), TimestampMS: value.Get("ts_ms").Int(),
		PoolID:       truncateString(value.Get("pool_id").String(), 48),
		Model:        truncateString(value.Get("model").String(), 256),
		Actual:       truncateString(value.Get("actual").String(), 512),
		Shadow:       truncateString(value.Get("shadow").String(), 512),
		Reason:       truncateString(value.Get("shadow_reason").String(), 64),
		Candidates:   value.Get("candidate_count").Int(),
		ActualInPool: value.Get("actual_in_pool").Bool(), Agree: value.Get("agree").Bool(),
		Success: value.Get("success").Bool(), Status: value.Get("status").Int(),
	}
	if event.Type == "observation" {
		event.Actual = truncateString(value.Get("auth").String(), 512)
	}
	r.monitor.mu.Lock()
	defer r.monitor.mu.Unlock()
	s := &r.monitor.status
	s.Processed++
	switch event.Type {
	case "selection":
		s.Selections++
		if event.Shadow == "" {
			s.NoCandidates++
		} else if !event.ActualInPool {
			s.Structural++
		} else {
			s.Comparable++
			if event.Agree {
				s.Agreements++
			} else {
				s.Disagreements++
			}
		}
	case "selection_error":
		s.SelectionErrors++
	case "observation":
		s.Observations++
		if event.Success {
			s.Successes++
		} else {
			s.Failures++
		}
	default:
		return
	}
	if len(s.Recent) == quotaShadowRecentLimit {
		copy(s.Recent, s.Recent[1:])
		s.Recent[len(s.Recent)-1] = event
	} else {
		s.Recent = append(s.Recent, event)
	}
}

func (r *quotaShadowRecorder) recordFailure(code string, rendering bool) {
	r.monitor.mu.Lock()
	defer r.monitor.mu.Unlock()
	if rendering {
		r.monitor.status.RenderErrors++
	} else {
		r.monitor.status.WriteErrors++
	}
	r.monitor.status.LastError = code
}

// QuotaShadowSnapshot returns live, process-local statistics and recent safe projections.
// Counts reset on enable or output-path change; disabled mode retains the last session.
func (m *Manager) QuotaShadowSnapshot() QuotaShadowStatus {
	empty := QuotaShadowStatus{State: "disabled", RecentLimit: quotaShadowRecentLimit, Recent: []QuotaShadowEvent{}}
	if m == nil {
		return empty
	}
	m.quotaShadowMu.Lock()
	r, active, stopped := m.quotaShadowLast, m.quotaShadow.Load(), m.quotaShadowStopped
	m.quotaShadowMu.Unlock()
	if r != nil {
		r.monitor.mu.Lock()
		empty = r.monitor.status
		empty.Recent = append([]QuotaShadowEvent{}, empty.Recent...)
		r.monitor.mu.Unlock()
		empty.File = r.path
		empty.MaxFileMB = r.maxBytes.Load() >> 20
		empty.SoftLimit = r.softLimit() * 100
		empty.Dropped = r.dropped.Load()
		empty.QueuePending = len(r.queue)
		empty.QueueCapacity = cap(r.queue)
		empty.RecentLimit = quotaShadowRecentLimit
	}
	switch {
	case stopped:
		empty.State = "stopped"
	case m.HomeEnabled():
		empty.State = "home"
	case active == nil:
		empty.State = "disabled"
	case !active.running.Load():
		empty.State = "draining"
	case empty.LastError != "":
		empty.State = "error"
	case empty.Processed == 0:
		empty.State = "idle"
	default:
		empty.State = "recording"
	}
	return empty
}

func newQuotaShadowMonitor() quotaShadowMonitor {
	return quotaShadowMonitor{status: QuotaShadowStatus{
		Since: time.Now().UTC().Format(time.RFC3339Nano), Recent: []QuotaShadowEvent{},
	}}
}
