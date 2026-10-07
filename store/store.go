// Package store provides a versioned, capacity-bounded registry of
// parsed source maps with atomic load/replace semantics.
package store

import (
	"container/list"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/Diudiuseven/error-symbolication-go/sourcemap"
)

// Config controls store capacity.
type Config struct {
	// MaxBytes bounds total parsed map memory (estimated); 0 means
	// unbounded. When exceeded, least-recently-used maps are evicted.
	MaxBytes int64
}

type key struct {
	version string
	file    string
}

type entry struct {
	m    *sourcemap.SourceMap
	size int64
	elem *list.Element
}

// Store manages source maps keyed by (version, file). A map is parsed
// fully before being installed, so a failed or interrupted load never
// leaves a partial map visible. Installed maps are immutable; queries
// always observe one complete committed version.
type Store struct {
	mu      sync.Mutex
	cfg     Config
	entries map[key]*entry
	lru     *list.List // front = most recently used; values are key
	total   int64
}

// New creates an empty Store.
func New(cfg Config) *Store {
	return &Store{
		cfg:     cfg,
		entries: make(map[key]*entry),
		lru:     list.New(),
	}
}

// Load parses data and atomically installs it for (version, file),
// replacing any previous map for that key. Parsing happens before any
// state is mutated, so a parse failure leaves the previous state
// untouched and a bad map never affects other keys.
func (s *Store) Load(version, file string, data []byte) error {
	m, err := sourcemap.Parse(data)
	if err != nil {
		return fmt.Errorf("store: load %s/%s: %w", version, file, err)
	}
	s.install(key{version, file}, m)
	return nil
}

// LoadInline installs a map from an inline data: URL carrying base64
// encoded JSON, e.g. "data:application/json;base64,eyJ2...". A malformed
// URL or payload is reported without touching existing state.
func (s *Store) LoadInline(version, file, dataURL string) error {
	const prefix = "data:"
	if !strings.HasPrefix(dataURL, prefix) {
		return fmt.Errorf("store: inline map for %s/%s: not a data: URL", version, file)
	}
	body := dataURL[len(prefix):]
	comma := strings.IndexByte(body, ',')
	if comma < 0 {
		return fmt.Errorf("store: inline map for %s/%s: malformed data URL", version, file)
	}
	meta, payload := body[:comma], body[comma+1:]
	if !strings.HasSuffix(meta, ";base64") {
		return fmt.Errorf("store: inline map for %s/%s: only base64 data URLs are supported", version, file)
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return fmt.Errorf("store: inline map for %s/%s: corrupt base64 payload: %w", version, file, err)
	}
	return s.Load(version, file, data)
}

// LoadFile installs a map read from the filesystem. The file is fully
// read and closed before parsing; read or parse failures leave existing
// state untouched.
func (s *Store) LoadFile(version, file, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("store: read map for %s/%s: %w", version, file, err)
	}
	return s.Load(version, file, data)
}

// install atomically replaces the map for k and enforces the capacity
// bound. The previous map, if any, becomes unreachable for new queries
// as soon as install returns; in-flight queries keep their own reference.
func (s *Store) install(k key, m *sourcemap.SourceMap) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.entries[k]; ok {
		s.total -= old.size
		s.lru.Remove(old.elem)
	}
	e := &entry{m: m, size: estimateSize(m)}
	e.elem = s.lru.PushFront(k)
	s.entries[k] = e
	s.total += e.size
	s.evictLocked(k)
}

// evictLocked drops least-recently-used entries until the store is
// within budget. The just-installed key is never evicted, so a single
// oversized map is still served rather than silently dropped.
func (s *Store) evictLocked(protect key) {
	if s.cfg.MaxBytes <= 0 {
		return
	}
	for s.total > s.cfg.MaxBytes && s.lru.Len() > 1 {
		back := s.lru.Back()
		if back == nil {
			return
		}
		k := back.Value.(key)
		if k == protect {
			// Move the protected key out of the eviction path.
			s.lru.MoveToFront(back)
			if s.lru.Back().Value.(key) == protect {
				return // only the protected entry remains
			}
			continue
		}
		s.removeLocked(k)
	}
}

// Resolve returns the map for (version, file), if present. The lookup
// is pinned to the exact version: maps of other versions are never
// mixed in. The returned map is immutable and stays valid for the
// caller even if it is later replaced or evicted.
func (s *Store) Resolve(version, file string) (*sourcemap.SourceMap, bool) {
	k := key{version, file}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[k]
	if !ok {
		return nil, false
	}
	s.lru.MoveToFront(e.elem)
	return e.m, true
}

// Remove deletes the map for (version, file).
func (s *Store) Remove(version, file string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLocked(key{version, file})
}

func (s *Store) removeLocked(k key) {
	e, ok := s.entries[k]
	if !ok {
		return
	}
	delete(s.entries, k)
	s.lru.Remove(e.elem)
	s.total -= e.size
}

// Stats reports the number of installed maps and their estimated size.
func (s *Store) Stats() (count int, totalBytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries), s.total
}

// estimateSize approximates the in-memory footprint of a parsed map so
// the capacity bound tracks real usage without exact accounting.
func estimateSize(m *sourcemap.SourceMap) int64 {
	var n int64 = 128 // fixed overhead
	for _, s := range m.Sources {
		n += int64(len(s)) + 24
	}
	for _, s := range m.Names {
		n += int64(len(s)) + 24
	}
	n += int64(m.SegmentCount()) * 48 // per-segment struct + slice overhead
	n += int64(m.LineCount()) * 24
	return n
}
