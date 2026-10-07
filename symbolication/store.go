package symbolication

import (
	"errors"
	"os"
	"path"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/Diudiuseven/error-symbolication-go/sourcemap"
	"github.com/Diudiuseven/error-symbolication-go/stacktrace"
)

// Options configures a Store.
type Options struct {
	// MaxConcurrent bounds how many resolutions run at once. Excess
	// requests wait for a slot; in-flight work is unaffected.
	// Zero means 64.
	MaxConcurrent int
	// MaxMapBytes bounds the total bytes of loaded map content. Loading
	// beyond the budget evicts least-recently-used maps (they report
	// no_map until reloaded). Zero means 256 MiB.
	MaxMapBytes int64
	// MaxTraceBytes bounds a single trace's size so one request's memory
	// stays constant regardless of map volume. Zero means 1 MiB.
	MaxTraceBytes int
}

func (o *Options) withDefaults() {
	if o.MaxConcurrent <= 0 {
		o.MaxConcurrent = 64
	}
	if o.MaxMapBytes <= 0 {
		o.MaxMapBytes = 256 << 20
	}
	if o.MaxTraceBytes <= 0 {
		o.MaxTraceBytes = 1 << 20
	}
}

// entry is one installed map (or its terminal load error). Entries are
// immutable once published; last is only an LRU hint and never affects
// resolution results.
type entry struct {
	sm   *sourcemap.SourceMap
	err  error // ErrCorruptMap / ErrTruncatedMap classified load failure
	size int64
	last atomic.Int64
}

// snapshot is an immutable view of all maps: version -> artifact -> entry.
type snapshot struct {
	maps  map[string]map[string]*entry
	bytes int64
}

// Store holds source maps grouped by release version. Loads are atomic:
// queries always see one fully-committed snapshot, never a half-installed
// map or field-level mixed state.
type Store struct {
	opts  Options
	mu    sync.Mutex // serializes loads/evictions
	snap  atomic.Pointer[snapshot]
	clock atomic.Int64
	sem   chan struct{}
}

// NewStore creates an empty store with default options.
func NewStore() *Store { return NewStoreWith(Options{}) }

// NewStoreWith creates an empty store with the given options.
func NewStoreWith(opts Options) *Store {
	opts.withDefaults()
	s := &Store{opts: opts, sem: make(chan struct{}, opts.MaxConcurrent)}
	s.snap.Store(&snapshot{maps: map[string]map[string]*entry{}})
	return s
}

// Load atomically parses and installs inline map content for
// (version, artifact), replacing any previous map for the same key.
// Corrupt or truncated content is recorded against this key only: queries
// for it report the failure, other artifacts and versions are unaffected.
func (s *Store) Load(version, artifact string, data []byte) {
	e := &entry{size: int64(len(data))}
	sm, err := sourcemap.Parse(data)
	switch {
	case err == nil:
		e.sm = sm
	case errors.Is(err, sourcemap.ErrTruncated):
		e.err = err
	default:
		e.err = err
	}
	s.install(version, artifact, e)
}

// LoadFile loads map content from a local file. A read or parse failure
// leaves the previous state untouched and is returned to the caller.
func (s *Store) LoadFile(version, artifact, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	s.Load(version, artifact, data)
	return nil
}

// install publishes a new snapshot containing e. Callers never observe any
// intermediate state.
func (s *Store) install(version, artifact string, e *entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.snap.Load()
	maps := make(map[string]map[string]*entry, len(old.maps))
	for v, arts := range old.maps {
		cp := make(map[string]*entry, len(arts))
		for a, ent := range arts {
			cp[a] = ent
		}
		maps[v] = cp
	}
	arts := maps[version]
	if arts == nil {
		arts = map[string]*entry{}
		maps[version] = arts
	}
	if prev, ok := arts[artifact]; ok {
		old.bytes -= prev.size
	}
	e.last.Store(s.clock.Add(1))
	arts[artifact] = e
	old.bytes += e.size
	s.evictLocked(maps, &old.bytes)
	s.snap.Store(&snapshot{maps: maps, bytes: old.bytes})
}

// evictLocked drops least-recently-used entries until the byte budget is
// met. Eviction never changes results for maps still within budget.
func (s *Store) evictLocked(maps map[string]map[string]*entry, total *int64) {
	if *total <= s.opts.MaxMapBytes {
		return
	}
	type cand struct {
		version, artifact string
		last, size        int64
	}
	var cands []cand
	for v, arts := range maps {
		for a, e := range arts {
			cands = append(cands, cand{v, a, e.last.Load(), e.size})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].last < cands[j].last })
	for _, c := range cands {
		if *total <= s.opts.MaxMapBytes {
			return
		}
		delete(maps[c.version], c.artifact)
		if len(maps[c.version]) == 0 {
			delete(maps, c.version)
		}
		*total -= c.size
	}
}

// Stats reports the number of installed maps and their total source bytes.
func (s *Store) Stats() (maps int, bytes int64) {
	snap := s.snap.Load()
	for _, arts := range snap.maps {
		maps += len(arts)
	}
	return maps, snap.bytes
}

// Resolve symbolicates a stack trace produced by the given code version.
// The result is deterministic for a fixed (version, trace) pair as long as
// the corresponding maps stay installed; map replacement changes only
// subsequent calls.
func (s *Store) Resolve(version, trace string) Result {
	res := Result{Version: version}
	if len(trace) > s.opts.MaxTraceBytes {
		res.Err = ErrTraceTooLarge
		return res
	}
	s.sem <- struct{}{}
	defer func() { <-s.sem }()

	snap := s.snap.Load()
	frames := stacktrace.Parse(trace)
	res.Frames = make([]ResolvedFrame, len(frames))
	for i, f := range frames {
		res.Frames[i] = resolveFrame(snap, version, f, s.clock.Add(1))
	}
	return res
}

func resolveFrame(snap *snapshot, version string, f stacktrace.Frame, tick int64) ResolvedFrame {
	rf := ResolvedFrame{
		Raw:      f.Raw,
		Source:   f.File,
		Line:     f.Line,
		Column:   f.Column,
		Function: f.Function,
	}
	if f.File == "" || !f.HasLine {
		rf.Status = StatusUnparseableLine
		return rf
	}
	arts := snap.maps[version]
	if arts == nil {
		rf.Status = StatusNoMap
		return rf
	}
	e := arts[f.File]
	if e == nil {
		// Fall back to matching by path base name so URLs and
		// relative paths both resolve deterministically.
		e = arts[path.Base(f.File)]
	}
	if e == nil {
		rf.Status = StatusNoMap
		return rf
	}
	e.last.Store(tick)
	if e.err != nil {
		if errors.Is(e.err, sourcemap.ErrTruncated) {
			rf.Status = StatusTruncatedMap
		} else {
			rf.Status = StatusCorruptMap
		}
		return rf
	}
	col := f.Column
	if col < 0 {
		col = 0 // column missing: best effort with line start
	}
	mp, ok := e.sm.Lookup(f.Line, col)
	if !ok {
		rf.Status = StatusNoMapping
		return rf
	}
	rf.Status = StatusResolved
	rf.Source = mp.Source
	rf.Line = mp.Line
	rf.Column = mp.Column
	if mp.Function != "" {
		rf.Function = mp.Function
	}
	return rf
}
