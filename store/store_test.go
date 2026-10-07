package store

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Diudiuseven/error-symbolication-go/sourcemap"
)

// mapFor builds a map resolving generated line 1 -> src/<srcLine> col 0.
// VLQ: genCol 0, srcIdx 0, srcLine delta, srcCol 0.
func mapFor(t *testing.T, src string, srcLineDelta int) []byte {
	t.Helper()
	// Encode [0, 0, srcLineDelta, 0] as VLQ.
	enc := func(v int) string {
		const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
		u := v << 1
		if v < 0 {
			u = (-v << 1) | 1
		}
		out := ""
		for {
			d := u & 31
			u >>= 5
			if u > 0 {
				d |= 32
			}
			out += string(chars[d])
			if u == 0 {
				break
			}
		}
		return out
	}
	mappings := enc(0) + enc(0) + enc(srcLineDelta) + enc(0)
	return []byte(fmt.Sprintf(`{"version":3,"sources":[%q],"names":[],"mappings":%q}`, src, mappings))
}

func TestCrossVersionIsolation(t *testing.T) {
	s := New(Config{})
	if err := s.Load("v1", "bundle.js", mapFor(t, "a.js", 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Load("v2", "bundle.js", mapFor(t, "b.js", 5)); err != nil {
		t.Fatal(err)
	}
	m1, ok := s.Resolve("v1", "bundle.js")
	if !ok {
		t.Fatal("v1 missing")
	}
	m2, ok := s.Resolve("v2", "bundle.js")
	if !ok {
		t.Fatal("v2 missing")
	}
	p1, _, _ := m1.Lookup(1, 0)
	p2, _, _ := m2.Lookup(1, 0)
	if p1.Source != "a.js" || p1.Line != 1 {
		t.Fatalf("v1 resolved wrong: %+v", p1)
	}
	if p2.Source != "b.js" || p2.Line != 6 {
		t.Fatalf("v2 resolved wrong: %+v", p2)
	}
	// Same error must resolve per its own version, never mixed.
	if _, ok := s.Resolve("v3", "bundle.js"); ok {
		t.Fatal("unregistered version must not resolve")
	}
}

func TestAtomicReplaceOnFailure(t *testing.T) {
	s := New(Config{})
	good := mapFor(t, "a.js", 0)
	if err := s.Load("v1", "bundle.js", good); err != nil {
		t.Fatal(err)
	}
	// A failed replacement must leave the old map fully intact.
	if err := s.Load("v1", "bundle.js", []byte(`{"version":3,`)); err == nil {
		t.Fatal("expected error for truncated map")
	}
	m, ok := s.Resolve("v1", "bundle.js")
	if !ok {
		t.Fatal("old map vanished after failed load")
	}
	p, ok, _ := m.Lookup(1, 0)
	if !ok || p.Source != "a.js" {
		t.Fatalf("old map corrupted by failed load: %+v", p)
	}
	// A successful replacement must be fully visible.
	if err := s.Load("v1", "bundle.js", mapFor(t, "c.js", 2)); err != nil {
		t.Fatal(err)
	}
	m, _ = s.Resolve("v1", "bundle.js")
	p, _, _ = m.Lookup(1, 0)
	if p.Source != "c.js" || p.Line != 3 {
		t.Fatalf("replacement not visible: %+v", p)
	}
}

func TestCorruptMapDoesNotPollute(t *testing.T) {
	s := New(Config{})
	if err := s.Load("v1", "ok.js", mapFor(t, "a.js", 0)); err != nil {
		t.Fatal(err)
	}
	// Corrupt map for a different file of the same version.
	err := s.Load("v1", "bad.js", []byte(`garbage`))
	if err == nil || !sourcemap.IsCorrupt(err) {
		t.Fatalf("want corrupt, got %v", err)
	}
	// Truncated map for a different version.
	err = s.Load("v2", "bad.js", []byte(`{"version":3,"sources":["a.js"],"mappings":"g"}`))
	if err == nil || !sourcemap.IsTruncated(err) {
		t.Fatalf("want truncated, got %v", err)
	}
	// The good map must be unaffected.
	m, ok := s.Resolve("v1", "ok.js")
	if !ok {
		t.Fatal("good map polluted by bad loads")
	}
	if _, ok, _ := m.Lookup(1, 0); !ok {
		t.Fatal("good map lookup broken")
	}
	if _, ok := s.Resolve("v1", "bad.js"); ok {
		t.Fatal("corrupt map must not be installed")
	}
	if _, ok := s.Resolve("v2", "bad.js"); ok {
		t.Fatal("truncated map must not be installed")
	}
}

func TestLoadInline(t *testing.T) {
	s := New(Config{})
	url := "data:application/json;base64," + base64.StdEncoding.EncodeToString(mapFor(t, "inl.js", 0))
	if err := s.LoadInline("v1", "bundle.js", url); err != nil {
		t.Fatal(err)
	}
	m, ok := s.Resolve("v1", "bundle.js")
	if !ok {
		t.Fatal("inline map not installed")
	}
	p, _, _ := m.Lookup(1, 0)
	if p.Source != "inl.js" {
		t.Fatalf("wrong source: %+v", p)
	}
	// Bad base64 must fail without state change.
	if err := s.LoadInline("v1", "x.js", "data:application/json;base64,!!!"); err == nil {
		t.Fatal("expected base64 error")
	}
	if _, ok := s.Resolve("v1", "x.js"); ok {
		t.Fatal("bad inline map installed")
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.js.map")
	if err := os.WriteFile(path, mapFor(t, "file.js", 1), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(Config{})
	if err := s.LoadFile("v1", "app.js", path); err != nil {
		t.Fatal(err)
	}
	m, ok := s.Resolve("v1", "app.js")
	if !ok {
		t.Fatal("file map not installed")
	}
	p, _, _ := m.Lookup(1, 0)
	if p.Source != "file.js" || p.Line != 2 {
		t.Fatalf("wrong position: %+v", p)
	}
	if err := s.LoadFile("v1", "missing.js", filepath.Join(dir, "nope")); err == nil {
		t.Fatal("expected read error")
	}
}

func TestCapacityEviction(t *testing.T) {
	// Tiny budget forces eviction of least-recently-used maps.
	s := New(Config{MaxBytes: 400})
	for i := 0; i < 10; i++ {
		if err := s.Load("v1", fmt.Sprintf("f%d.js", i), mapFor(t, "x.js", 0)); err != nil {
			t.Fatal(err)
		}
	}
	count, total := s.Stats()
	if total > 400 {
		t.Fatalf("budget exceeded: %d bytes", total)
	}
	if count == 10 {
		t.Fatal("no eviction happened")
	}
	// Oldest entry must be gone, newest must remain.
	if _, ok := s.Resolve("v1", "f0.js"); ok {
		t.Fatal("LRU entry should have been evicted")
	}
	if _, ok := s.Resolve("v1", "f9.js"); !ok {
		t.Fatal("newest entry should remain")
	}
}

func TestConcurrentLoadAndQuery(t *testing.T) {
	s := New(Config{MaxBytes: 1 << 20})
	const versions = 4
	for v := 0; v < versions; v++ {
		if err := s.Load(fmt.Sprintf("v%d", v), "bundle.js", mapFor(t, "a.js", v)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	// Writers: keep replacing maps.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				v := fmt.Sprintf("v%d", (w+i)%versions)
				_ = s.Load(v, "bundle.js", mapFor(t, "a.js", i%7))
			}
		}(w)
	}
	// Readers: every observation must be self-consistent.
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				m, ok := s.Resolve(fmt.Sprintf("v%d", i%versions), "bundle.js")
				if !ok {
					continue
				}
				p, ok, _ := m.Lookup(1, 0)
				if !ok {
					t.Error("installed map must always resolve line 1")
					return
				}
				if p.Source != "a.js" || p.Line < 1 || p.Line > 7 {
					t.Errorf("inconsistent read: %+v", p)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestDeterministicRepeatedQueries(t *testing.T) {
	s := New(Config{})
	if err := s.Load("v1", "bundle.js", mapFor(t, "a.js", 3)); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Resolve("v1", "bundle.js")
	first, _, _ := m.Lookup(1, 0)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				p, ok, _ := m.Lookup(1, 0)
				if !ok || p != first {
					t.Errorf("nondeterministic result: %+v vs %+v", p, first)
					return
				}
			}
		}()
	}
	wg.Wait()
}
