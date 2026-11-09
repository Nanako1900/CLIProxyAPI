package accounting

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
	"go.etcd.io/bbolt"
)

var bucketNames = []string{"meta", "identities", "accounts", "events", "dedup", "pending", "daily", "prices", "account-events"}

type Store struct {
	mu         sync.RWMutex
	db         *bbolt.DB
	path       string
	cfg        config.UsageAccountingConfig
	current    PriceSnapshot
	compiled   map[string]map[string]priceRule
	started    time.Time
	lastError  string
	priceError string
	wake       chan struct{}
	stop       chan struct{}
	done       chan struct{}
	closeOnce  sync.Once
	nowFunc    func() time.Time
}

func encode(value any) ([]byte, error) {
	var b bytes.Buffer
	if errEncode := gob.NewEncoder(&b).Encode(value); errEncode != nil {
		return nil, errEncode
	}
	return b.Bytes(), nil
}

func decode(data []byte, value any) error {
	if len(data) == 0 {
		return fmt.Errorf("missing ledger value")
	}
	return gob.NewDecoder(bytes.NewReader(data)).Decode(value)
}

func put(bucket *bbolt.Bucket, key string, value any) error {
	payload, errEncode := encode(value)
	if errEncode != nil {
		return errEncode
	}
	return bucket.Put([]byte(key), payload)
}

func Open(path string, cfg config.UsageAccountingConfig) (*Store, error) {
	if errValidate := cfg.Validate(); errValidate != nil {
		return nil, errValidate
	}
	if errMkdir := os.MkdirAll(filepath.Dir(path), 0700); errMkdir != nil {
		return nil, fmt.Errorf("create usage ledger directory: %w", errMkdir)
	}
	// A separate ledger file avoids the general KV store's TTL and lifecycle rules.
	db, errOpen := bbolt.Open(path, 0600, &bbolt.Options{Timeout: time.Second})
	if errOpen != nil {
		return nil, fmt.Errorf("open usage ledger: %w", errOpen)
	}
	s := &Store{db: db, path: path, cfg: cfg, compiled: make(map[string]map[string]priceRule), wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}), nowFunc: time.Now}
	errInit := db.Update(func(tx *bbolt.Tx) error {
		for _, name := range bucketNames {
			if _, errBucket := tx.CreateBucketIfNotExists([]byte(name)); errBucket != nil {
				return errBucket
			}
		}
		meta := tx.Bucket([]byte("meta"))
		if version := meta.Get([]byte("schema")); len(version) > 0 && string(version) != "1" {
			return fmt.Errorf("unsupported usage ledger schema")
		}
		if errPut := meta.Put([]byte("schema"), []byte("1")); errPut != nil {
			return errPut
		}
		if raw := meta.Get([]byte("started")); len(raw) > 0 {
			if errDecode := decode(raw, &s.started); errDecode != nil {
				return errDecode
			}
		} else {
			s.started = s.nowFunc().UTC()
			if errPut := put(meta, "started", s.started); errPut != nil {
				return errPut
			}
		}
		if version := meta.Get([]byte("price")); len(version) > 0 {
			if errDecode := decode(tx.Bucket([]byte("prices")).Get(version), &s.current); errDecode != nil {
				return errDecode
			}
			return nil
		}
		snapshot, rules, errParse := parsePrices(builtinPrices, "builtin:"+DefaultPriceURL, time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC))
		if errParse != nil {
			return errParse
		}
		s.current = snapshot
		s.compiled[snapshot.Version] = rules
		if errPut := put(tx.Bucket([]byte("prices")), snapshot.Version, snapshot); errPut != nil {
			return errPut
		}
		return meta.Put([]byte("price"), []byte(snapshot.Version))
	})
	if errInit != nil {
		if errClose := db.Close(); errClose != nil {
			log.WithError(errClose).Error("close uninitialized usage ledger")
		}
		return nil, fmt.Errorf("initialize usage ledger: %w", errInit)
	}
	go s.run()
	s.signal()
	return s, nil
}

func (s *Store) Configure(cfg config.UsageAccountingConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
}

func (s *Store) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Store) Record(_ context.Context, record usage.Record) error {
	if !usage.GenerateEnabled(record.Generate) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil || !s.cfg.Enabled {
		return nil
	}
	if strings.TrimSpace(record.RequestID) == "" {
		return fmt.Errorf("usage event has no execution ID")
	}
	now := s.nowFunc().UTC()
	detail := usage.EnsureTokenBreakdownForProvider(record.Detail, record.Provider, record.ExecutorType)
	params := make(map[string]bool, len(record.PricingParams))
	for k, v := range record.PricingParams {
		if k == "enable_thinking" {
			params[k] = v
		}
	}
	event := Event{ID: record.RequestID, ParentExecutionID: record.ParentExecutionID, TraceID: record.TraceID, AuthID: record.AuthID, AuthIndex: record.AuthIndex, Provider: record.Provider, Model: record.Model, Alias: record.Alias, UpstreamModel: record.UpstreamModel, ResponseModel: record.ResponseModel, RequestedAt: record.RequestedAt, RecordedAt: now, Failed: record.Failed, Stream: record.Stream, Tokens: detail.TokenBreakdown, PricingParams: params, ServiceTier: record.ResponseServiceTier}
	if event.RequestedAt.IsZero() {
		event.RequestedAt = now
	}
	event.RequestedAt = event.RequestedAt.UTC()
	event.UsageReported = detail.Reported || detail.TokenBreakdown.TotalTokens > 0
	event.PricingModel = strings.TrimSpace(record.ResponseModel)
	if event.PricingModel == "" {
		event.PricingModel = strings.TrimSpace(record.UpstreamModel)
	}
	if event.PricingModel == "" {
		event.PricingModel = strings.TrimSpace(record.Model)
	}
	if alias := s.cfg.ModelAliases[event.PricingModel]; strings.TrimSpace(alias) != "" {
		event.PricingModel = strings.TrimSpace(alias)
	}
	event.Cache1h = ptrValue(detail.CacheCreation1hTokens)
	event.Cache12h = ptrValue(detail.CacheCreation12hTokens)
	if detail.CacheDetails != nil {
		if detail.CacheCreation1hTokens == nil {
			event.Cache1h = ptrValue(detail.CacheDetails.Ephemeral1hInputTokens)
		}
		if detail.CacheCreation12hTokens == nil {
			event.Cache12h = ptrValue(detail.CacheDetails.Ephemeral12hInputTokens)
		}
	}
	event.AudioInput = ptrValue(detail.AudioInputTokens)
	event.AudioOutput = ptrValue(detail.AudioOutputTokens)
	event.ImageOutput = ptrValue(detail.ImageOutputTokens)
	event.ImageCount = ptrValue(detail.ImageCount)
	event.Valuation = Valuation{Status: "pending", PriceVersion: s.current.Version}
	identity := record.AccountKey
	if identity == "" {
		identity = record.AuthIndex
	}
	if identity == "" {
		identity = record.AuthID
	}
	if identity == "" {
		identity = "unattributed"
	}
	if binding := s.cfg.AccountBindings[record.AuthID]; binding != "" {
		identity = "binding:" + binding
	}
	if binding := s.cfg.AccountBindings[record.AuthIndex]; binding != "" {
		identity = "binding:" + binding
	}
	identityHash := sha256.Sum256([]byte(record.Provider + "\x00" + identity))
	identityKey := fmt.Sprintf("%x", identityHash)
	errCommit := s.db.Update(func(tx *bbolt.Tx) error {
		if tx.Bucket([]byte("dedup")).Get([]byte(event.ID)) != nil {
			return nil
		}
		identities := tx.Bucket([]byte("identities"))
		accounts := tx.Bucket([]byte("accounts"))
		var account Account
		if id := identities.Get([]byte(identityKey)); id != nil {
			if errDecode := decode(accounts.Get(id), &account); errDecode != nil {
				return errDecode
			}
		} else {
			account = Account{ID: uuid.NewString(), Provider: record.Provider, FirstSeen: now, Label: strings.TrimSpace(record.AccountLabel), Totals: Totals{OutputUSD: "0", TotalUSD: "0"}}
			if account.Label == "" {
				account.Label = record.Provider + " account"
			}
			if errPut := identities.Put([]byte(identityKey), []byte(account.ID)); errPut != nil {
				return errPut
			}
		}
		if record.AccountLabel != "" {
			account.Label = record.AccountLabel
		}
		account.AuthIndex = record.AuthIndex
		account.LastSeen = now
		event.AccountID = account.ID
		if errPut := put(accounts, account.ID, account); errPut != nil {
			return errPut
		}
		if errPut := put(tx.Bucket([]byte("events")), event.ID, event); errPut != nil {
			return errPut
		}
		if errPut := tx.Bucket([]byte("dedup")).Put([]byte(event.ID), []byte{1}); errPut != nil {
			return errPut
		}
		if errPut := tx.Bucket([]byte("account-events")).Put([]byte(eventIndexKey(event)), []byte(event.ID)); errPut != nil {
			return errPut
		}
		return tx.Bucket([]byte("pending")).Put([]byte(event.ID), []byte{1})
	})
	if errCommit != nil {
		s.lastError = "usage event commit failed"
		return fmt.Errorf("commit usage event: %w", errCommit)
	}
	s.signal()
	return nil
}

func ptrValue(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func (s *Store) run() {
	defer close(s.done)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-s.wake:
			if errProcess := s.ProcessPending(); errProcess != nil {
				log.WithError(errProcess).Error("usage accounting projection failed")
			}
		case <-ticker.C:
			if errPrune := s.Prune(); errPrune != nil {
				log.WithError(errPrune).Error("usage accounting retention failed")
			}
		case <-s.stop:
			if errProcess := s.ProcessPending(); errProcess != nil {
				log.WithError(errProcess).Error("usage accounting shutdown projection failed")
			}
			return
		}
	}
}

// ProcessPending is also a deterministic synchronization point for callers and tests.
func (s *Store) ProcessPending() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	for {
		processed := 0
		errProcess := s.db.Update(func(tx *bbolt.Tx) error {
			pending := tx.Bucket([]byte("pending"))
			cursor := pending.Cursor()
			for key, _ := cursor.First(); key != nil && processed < 128; key, _ = cursor.Next() {
				var event Event
				if errDecode := decode(tx.Bucket([]byte("events")).Get(key), &event); errDecode != nil {
					return errDecode
				}
				var snapshot PriceSnapshot
				if errDecode := decode(tx.Bucket([]byte("prices")).Get([]byte(event.Valuation.PriceVersion)), &snapshot); errDecode != nil {
					return errDecode
				}
				rules := s.compiled[snapshot.Version]
				if rules == nil {
					rules = make(map[string]priceRule, len(snapshot.Expressions))
					for model, source := range snapshot.Expressions {
						expr, refs, errCompile := compileExpression(source)
						rules[model] = priceRule{expr: expr, refs: refs, err: errCompile}
					}
					if len(s.compiled) >= 8 {
						s.compiled = make(map[string]map[string]priceRule)
					}
					s.compiled[snapshot.Version] = rules
				}
				event.Valuation = priceEvent(event, snapshot, rules)
				var account Account
				accounts := tx.Bucket([]byte("accounts"))
				if errDecode := decode(accounts.Get([]byte(event.AccountID)), &account); errDecode != nil {
					return errDecode
				}
				if errTotal := account.Totals.addEvent(event); errTotal != nil {
					return errTotal
				}
				if errPut := put(accounts, account.ID, account); errPut != nil {
					return errPut
				}
				day := event.RequestedAt.Format("2006-01-02")
				dailyKey := event.AccountID + "\x00" + day + "\x00" + event.PricingModel
				daily := DailyTotal{AccountID: event.AccountID, Day: day, Model: event.PricingModel}
				if raw := tx.Bucket([]byte("daily")).Get([]byte(dailyKey)); raw != nil {
					if errDecode := decode(raw, &daily); errDecode != nil {
						return errDecode
					}
				}
				if errTotal := daily.Totals.addEvent(event); errTotal != nil {
					return errTotal
				}
				if errPut := put(tx.Bucket([]byte("daily")), dailyKey, daily); errPut != nil {
					return errPut
				}
				if errPut := put(tx.Bucket([]byte("events")), event.ID, event); errPut != nil {
					return errPut
				}
				if errDelete := cursor.Delete(); errDelete != nil {
					return errDelete
				}
				processed++
			}
			return nil
		})
		if errProcess != nil {
			s.lastError = "usage projection failed"
			return fmt.Errorf("project usage events: %w", errProcess)
		}
		if processed < 128 {
			return nil
		}
	}
}

func (s *Store) InstallPrices(payload []byte, source string) error {
	snapshot, rules, errParse := parsePrices(payload, source, s.nowFunc().UTC())
	if errParse != nil {
		return errParse
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return fmt.Errorf("usage ledger is closed")
	}
	errInstall := s.db.Update(func(tx *bbolt.Tx) error {
		if errPut := put(tx.Bucket([]byte("prices")), snapshot.Version, snapshot); errPut != nil {
			return errPut
		}
		return tx.Bucket([]byte("meta")).Put([]byte("price"), []byte(snapshot.Version))
	})
	if errInstall != nil {
		return errInstall
	}
	s.current = snapshot
	s.compiled[snapshot.Version] = rules
	s.priceError = ""
	return nil
}

func (s *Store) Prune() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	days := s.cfg.DetailRetentionDays
	if days == 0 {
		days = 30
	}
	cutoff := s.nowFunc().UTC().AddDate(0, 0, -days)
	return s.db.Update(func(tx *bbolt.Tx) error {
		cursor := tx.Bucket([]byte("events")).Cursor()
		for key, raw := cursor.First(); key != nil; key, raw = cursor.Next() {
			var event Event
			if errDecode := decode(raw, &event); errDecode != nil {
				return errDecode
			}
			if event.RecordedAt.Before(cutoff) && tx.Bucket([]byte("pending")).Get(key) == nil {
				if errDelete := tx.Bucket([]byte("account-events")).Delete([]byte(eventIndexKey(event))); errDelete != nil {
					return errDelete
				}
				if errDelete := cursor.Delete(); errDelete != nil {
					return errDelete
				}
			}
		}
		return nil
	})
}

func (s *Store) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := Status{Enabled: s.cfg.Enabled, Available: s.db != nil, StartedAt: s.started, LastError: s.lastError, PriceError: s.priceError, PriceVersion: s.current.Version, PriceFetchedAt: s.current.FetchedAt, PriceSource: s.current.Source, Coverage: "API base-price estimates; observed usage only"}
	if s.db != nil {
		if errView := s.db.View(func(tx *bbolt.Tx) error { status.Pending = tx.Bucket([]byte("pending")).Stats().KeyN; return nil }); errView != nil {
			status.LastError = "usage ledger read failed"
		}
	}
	return status
}

func (s *Store) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		// Stop accepting events before draining and closing the database.
		s.mu.Lock()
		s.cfg.Enabled = false
		s.mu.Unlock()
		close(s.stop)
		<-s.done
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.db != nil {
			closeErr = s.db.Close()
			s.db = nil
		}
	})
	return closeErr
}

func eventIndexKey(e Event) string {
	return e.AccountID + "\x00" + e.RequestedAt.UTC().Format("20060102T150405.000000000Z") + "\x00" + e.ID
}
