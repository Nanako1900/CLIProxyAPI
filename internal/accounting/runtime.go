package accounting

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/home"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

// Runtime owns one server's ledger. It never installs a process-global recorder.
type Runtime struct {
	mu        sync.RWMutex
	store     *Store
	cfg       config.UsageAccountingConfig
	path      string
	lastError string
	closed    bool
	ctx       context.Context
	cancel    context.CancelFunc
	wake      chan struct{}
	done      chan struct{}
	refreshMu sync.Mutex
	client    *http.Client
}

func NewRuntime(cfg *config.Config) *Runtime {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runtime{ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{}), client: &http.Client{}}
	r.Configure(cfg)
	go r.run(ctx)
	return r
}

func (r *Runtime) Configure(cfg *config.Config) {
	if cfg == nil {
		return
	}
	c := cfg.UsageAccounting
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	diagnostic := ""
	if cfg.Home.Enabled || home.Current() != nil {
		c.Enabled = false
		if cfg.UsageAccounting.Enabled {
			diagnostic = "Home mode requires accounting in the Home control plane"
		}
		r.cfg = c
		r.lastError = diagnostic
		if r.store != nil {
			r.store.Configure(c)
		}
		return
	}
	path := strings.TrimSpace(c.Path)
	if path == "" {
		path = filepath.Join(cfg.AuthDir, "usage.db")
	}
	resolved, errResolve := filepath.Abs(path)
	if errResolve != nil {
		r.lastError = "cannot resolve usage ledger path"
		return
	}
	if r.store != nil {
		if resolved != r.path {
			r.lastError = "changing the usage ledger path requires a restart"
			return
		}
		r.cfg = c
		r.store.Configure(c)
	} else {
		r.cfg = c
		if !c.Enabled {
			if _, errStat := os.Stat(resolved); errStat != nil {
				r.lastError = diagnostic
				return
			}
		}
		store, errOpen := Open(resolved, c)
		if errOpen != nil {
			r.lastError = "cannot open usage ledger"
			log.WithError(errOpen).Error("usage accounting initialization failed")
			return
		}
		r.store = store
		r.path = resolved
	}
	r.lastError = diagnostic
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Runtime) Store() *Store { r.mu.RLock(); defer r.mu.RUnlock(); return r.store }

func (r *Runtime) Record(ctx context.Context, record usage.Record) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.cfg.Enabled || r.closed || home.Current() != nil {
		return nil
	}
	if r.store == nil {
		return fmt.Errorf("usage accounting unavailable")
	}
	return r.store.Record(ctx, record)
}

func (r *Runtime) Status() Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	status := Status{Enabled: r.cfg.Enabled, Coverage: "API base-price estimates; observed usage only"}
	if r.store != nil {
		status = r.store.Status()
	}
	if r.lastError != "" {
		status.LastError = r.lastError
	}
	return status
}

func (r *Runtime) RefreshPrices(ctx context.Context) error {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.ctx, cancel)
	defer func() { stop(); cancel() }()
	r.mu.RLock()
	store := r.store
	source := strings.TrimSpace(r.cfg.Pricing.SourceURL)
	closed := r.closed
	r.mu.RUnlock()
	if store == nil || closed {
		return fmt.Errorf("usage ledger unavailable")
	}
	if source == "" {
		source = DefaultPriceURL
	}
	parsed, errParse := url.Parse(source)
	if errParse != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("invalid pricing source URL")
	}
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if errRequest != nil {
		return errRequest
	}
	response, errFetch := r.client.Do(req)
	if errFetch == nil {
		defer func() {
			if errClose := response.Body.Close(); errClose != nil {
				log.WithError(errClose).Debug("close pricing response")
			}
		}()
		if response.StatusCode != http.StatusOK {
			errFetch = fmt.Errorf("pricing source returned status %d", response.StatusCode)
		} else {
			payload, errRead := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
			if errRead != nil {
				errFetch = errRead
			} else if len(payload) > 4<<20 {
				errFetch = fmt.Errorf("pricing source exceeds 4 MiB")
			} else {
				errFetch = store.InstallPrices(payload, source)
			}
		}
	}
	if errFetch != nil {
		store.mu.Lock()
		store.priceError = "price refresh failed; previous snapshot retained"
		store.mu.Unlock()
		return fmt.Errorf("refresh pricing: %w", errFetch)
	}
	return nil
}

func (r *Runtime) run(ctx context.Context) {
	defer close(r.done)
	for {
		r.mu.RLock()
		raw := r.cfg.Pricing.RefreshInterval
		enabled := r.cfg.Enabled
		r.mu.RUnlock()
		interval := 24 * time.Hour
		if parsed, errParse := time.ParseDuration(raw); errParse == nil && parsed >= time.Minute {
			interval = parsed
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-r.wake:
			timer.Stop()
			continue
		case <-timer.C:
			if enabled {
				if errRefresh := r.RefreshPrices(ctx); errRefresh != nil && ctx.Err() == nil {
					log.WithError(errRefresh).Warn("usage price refresh failed")
				}
			}
		}
	}
}

func (r *Runtime) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.cancel()
	store := r.store
	r.mu.Unlock()
	<-r.done
	// Wait for an administrative refresh before closing its target database.
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	if store != nil {
		return store.Close()
	}
	return nil
}
