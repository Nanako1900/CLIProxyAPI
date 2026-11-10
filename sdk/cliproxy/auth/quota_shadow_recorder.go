package auth

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	log "github.com/sirupsen/logrus"
)

const (
	quotaShadowQueueSize       = 1024
	quotaShadowDefaultMaxBytes = 256 << 20
)

// quotaShadowJob renders one JSONL record off the request path. dropped is the
// number of records discarded so far because the queue was full.
type quotaShadowJob func(dropped uint64) []byte

// quotaShadowSettings is the normalized configuration of a recorder.
type quotaShadowSettings struct {
	path      string
	maxBytes  int64
	softLimit float64
}

// quotaShadowRecorder serializes shadow records to an append-only JSONL file
// from a single goroutine. Producers never block: when the queue is full the
// record is dropped and counted. A recorder owns its path for its lifetime;
// the remaining settings are updated in place so two recorders never share a file.
type quotaShadowRecorder struct {
	path          string
	maxBytes      atomic.Int64
	softLimitBits atomic.Uint64

	mu      sync.RWMutex
	closed  bool
	queue   chan quotaShadowJob
	done    chan struct{}
	dropped atomic.Uint64

	file       *os.File
	size       int64
	warnedOpen bool
}

func newQuotaShadowRecorder(settings quotaShadowSettings) *quotaShadowRecorder {
	if settings.maxBytes <= 0 {
		settings.maxBytes = quotaShadowDefaultMaxBytes
	}
	r := &quotaShadowRecorder{
		path:  settings.path,
		queue: make(chan quotaShadowJob, quotaShadowQueueSize),
		done:  make(chan struct{}),
	}
	r.update(settings)
	go r.run()
	return r
}

// update applies the in-place settings. The path is fixed at construction.
func (r *quotaShadowRecorder) update(settings quotaShadowSettings) {
	maxBytes := settings.maxBytes
	if maxBytes <= 0 {
		maxBytes = quotaShadowDefaultMaxBytes
	}
	r.maxBytes.Store(maxBytes)
	r.softLimitBits.Store(math.Float64bits(settings.softLimit))
}

func (r *quotaShadowRecorder) softLimit() float64 {
	return math.Float64frombits(r.softLimitBits.Load())
}

// emit enqueues job without blocking. It reports whether the job was accepted.
func (r *quotaShadowRecorder) emit(job quotaShadowJob) bool {
	if r == nil || job == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return false
	}
	select {
	case r.queue <- job:
		return true
	default:
		r.dropped.Add(1)
		return false
	}
}

// close stops accepting jobs, drains the queue, and closes the file.
func (r *quotaShadowRecorder) close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		<-r.done
		return
	}
	r.closed = true
	close(r.queue)
	r.mu.Unlock()
	<-r.done
}

func (r *quotaShadowRecorder) run() {
	defer close(r.done)
	defer r.closeFile()
	for job := range r.queue {
		line := r.render(job)
		if len(line) == 0 {
			continue
		}
		r.write(append(line, '\n'))
	}
}

// render runs one job and contains any panic so a malformed observation can
// never take down the process from this background goroutine.
func (r *quotaShadowRecorder) render(job quotaShadowJob) (line []byte) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Errorf("quota shadow: record rendering panicked: %v", recovered)
			line = nil
		}
	}()
	return job(r.dropped.Load())
}

func (r *quotaShadowRecorder) write(line []byte) {
	if r.file != nil && r.size+int64(len(line)) > r.maxBytes.Load() && r.size > 0 {
		r.rotate()
	}
	if r.file == nil {
		if errOpen := r.openFile(); errOpen != nil {
			if !r.warnedOpen {
				r.warnedOpen = true
				log.WithError(errOpen).Warn("quota shadow: failed to open output file; records are discarded")
			}
			return
		}
	}
	n, errWrite := r.file.Write(line)
	r.size += int64(n)
	if errWrite != nil {
		log.WithError(errWrite).Warn("quota shadow: failed to write record")
		r.closeFile()
	}
}

func (r *quotaShadowRecorder) openFile() error {
	path := r.path
	if path == "" {
		return fmt.Errorf("quota shadow: empty output path")
	}
	if errMkdir := os.MkdirAll(filepath.Dir(path), 0o700); errMkdir != nil {
		return fmt.Errorf("quota shadow: create directory: %w", errMkdir)
	}
	file, errOpen := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if errOpen != nil {
		return fmt.Errorf("quota shadow: open %s: %w", path, errOpen)
	}
	info, errStat := file.Stat()
	if errStat != nil {
		if errClose := file.Close(); errClose != nil {
			log.WithError(errClose).Warn("quota shadow: failed to close output file")
		}
		return fmt.Errorf("quota shadow: stat %s: %w", path, errStat)
	}
	r.file = file
	r.size = info.Size()
	r.warnedOpen = false
	return nil
}

func (r *quotaShadowRecorder) rotate() {
	r.closeFile()
	if errRename := os.Rename(r.path, r.path+".1"); errRename != nil && !os.IsNotExist(errRename) {
		log.WithError(errRename).Warn("quota shadow: failed to rotate output file")
	}
}

func (r *quotaShadowRecorder) closeFile() {
	if r.file == nil {
		return
	}
	if errClose := r.file.Close(); errClose != nil {
		log.WithError(errClose).Warn("quota shadow: failed to close output file")
	}
	r.file = nil
	r.size = 0
}
