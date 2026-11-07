package clientua

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

const LimitPerProvider = 100

type Entry struct {
	ID        string `json:"id" yaml:"id"`
	Profile   `yaml:",inline"`
	FirstSeen time.Time `json:"firstSeen" yaml:"first-seen"`
	LastSeen  time.Time `json:"lastSeen" yaml:"last-seen"`
	Count     uint64    `json:"count" yaml:"count"`
	Observed  bool      `json:"observed" yaml:"observed"`
	Imported  bool      `json:"imported" yaml:"imported"`
}

// Store keeps request-time work in memory. A coalescing worker persists updates
// outside inference requests, and Close flushes the final snapshot.
type Store struct {
	mu         sync.RWMutex
	saveMu     sync.Mutex
	entries    map[string]Entry
	path       string
	now        func() time.Time
	generation uint64
	saved      uint64
	dirty      chan struct{}
	stop       chan struct{}
	done       chan struct{}
	started    bool
	closed     bool
}

func NewStore(path string) (*Store, error) {
	s := &Store{entries: make(map[string]Entry), path: path, now: time.Now, dirty: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	if path == "" {
		return s, nil
	}
	file, errOpen := os.Open(path)
	if errors.Is(errOpen, os.ErrNotExist) {
		return s, nil
	}
	if errOpen != nil {
		return s, fmt.Errorf("open client UA catalog: %w", errOpen)
	}
	defer func() {
		if errClose := file.Close(); errClose != nil {
			log.WithError(errClose).Warn("close client UA catalog")
		}
	}()
	var entries []Entry
	decoder := yaml.NewDecoder(io.LimitReader(file, 2<<20))
	decoder.KnownFields(true)
	if errDecode := decoder.Decode(&entries); errDecode != nil {
		return s, fmt.Errorf("decode client UA catalog: %w", errDecode)
	}
	for _, entry := range entries {
		headers := make(http.Header)
		for key, value := range entry.Headers {
			headers.Set(key, value)
		}
		profile, errProfile := FromHeaders(headers)
		if errProfile != nil || entry.Provider != profile.Provider {
			continue
		}
		entry.Profile, entry.ID = profile, profile.id()
		s.entries[entry.ID] = entry
	}
	s.trimLocked("claude")
	s.trimLocked("codex")
	return s, nil
}

func (s *Store) Observe(headers http.Header) {
	if s == nil {
		return
	}
	profile, errProfile := FromHeaders(headers)
	if errProfile == nil {
		s.add(profile, false)
	}
}

func (s *Store) Import(profile Profile) (Entry, error) {
	headers := make(http.Header)
	for key, value := range profile.Headers {
		headers.Add(key, value)
	}
	validated, errProfile := FromHeaders(headers)
	if errProfile != nil {
		return Entry{}, errProfile
	}
	if validated.Provider != profile.Provider {
		return Entry{}, fmt.Errorf("User-Agent does not match the selected provider")
	}
	entry := s.add(validated, true)
	if entry.ID == "" {
		return Entry{}, fmt.Errorf("client UA catalog is closed")
	}
	if errSave := s.Flush(); errSave != nil {
		return Entry{}, errSave
	}
	return entry, nil
}

func (s *Store) add(profile Profile, imported bool) Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Entry{}
	}
	id, now := profile.id(), s.now().UTC()
	entry, exists := s.entries[id]
	if !exists {
		entry = Entry{ID: id, Profile: profile.clone(), FirstSeen: now}
	}
	entry.LastSeen = now
	if imported {
		entry.Imported = true
	} else {
		entry.Observed = true
		entry.Count++
	}
	s.entries[id] = entry
	if !exists {
		s.trimLocked(profile.Provider)
	}
	s.changedLocked()
	entry.Profile = entry.Profile.clone()
	return entry
}

func (s *Store) trimLocked(provider string) {
	entries := s.listLocked(provider)
	for _, entry := range entries[min(len(entries), LimitPerProvider):] {
		delete(s.entries, entry.ID)
	}
}

func (s *Store) listLocked(provider string) []Entry {
	entries := make([]Entry, 0)
	for _, entry := range s.entries {
		if provider == "" || entry.Provider == provider {
			entry.Profile = entry.Profile.clone()
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].LastSeen.Equal(entries[j].LastSeen) {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].LastSeen.After(entries[j].LastSeen)
	})
	return entries
}

func (s *Store) List(provider string) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.listLocked(provider)
}

func (s *Store) Delete(provider, id string) error {
	s.mu.Lock()
	if entry, exists := s.entries[id]; exists && entry.Provider == provider {
		delete(s.entries, id)
		s.changedLocked()
	}
	s.mu.Unlock()
	return s.Flush()
}

func (s *Store) changedLocked() {
	s.generation++
	if s.path == "" || s.closed {
		return
	}
	if !s.started {
		s.started = true
		go s.run()
	}
	select {
	case s.dirty <- struct{}{}:
	default:
	}
}

func (s *Store) run() {
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			return
		case <-s.dirty:
			timer := time.NewTimer(time.Second)
			select {
			case <-s.stop:
				timer.Stop()
				return
			case <-timer.C:
			}
			if errSave := s.Flush(); errSave != nil {
				log.WithError(errSave).Warn("persist client UA catalog")
			}
		}
	}
}

func (s *Store) Flush() error {
	if s.path == "" {
		return nil
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.RLock()
	generation := s.generation
	entries := s.listLocked("")
	s.mu.RUnlock()
	if generation == s.saved {
		return nil
	}
	data, errMarshal := yaml.Marshal(entries)
	if errMarshal != nil {
		return errMarshal
	}
	if errDir := os.MkdirAll(filepath.Dir(s.path), 0700); errDir != nil {
		return errDir
	}
	file, errCreate := os.CreateTemp(filepath.Dir(s.path), ".client-ua-*")
	if errCreate != nil {
		return errCreate
	}
	defer func() {
		if errRemove := os.Remove(file.Name()); errRemove != nil && !errors.Is(errRemove, os.ErrNotExist) {
			log.WithError(errRemove).Warn("remove temporary client UA catalog")
		}
	}()
	_, errWrite := file.Write(data)
	errClose := file.Close()
	if errWrite != nil {
		return errWrite
	}
	if errClose != nil {
		return errClose
	}
	if errRename := os.Rename(file.Name(), s.path); errRename != nil {
		return errRename
	}
	s.saved = generation
	return nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.stop)
	}
	started := s.started
	s.mu.Unlock()
	if started {
		<-s.done
	}
	return s.Flush()
}
