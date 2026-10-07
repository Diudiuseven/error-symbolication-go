package symbolicate_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/Diudiuseven/error-symbolication-go/sourcemap"
	"github.com/Diudiuseven/error-symbolication-go/store"
	"github.com/Diudiuseven/error-symbolication-go/symbolicate"
)

// mapJSON builds a minimal map: generated line 1 col 0 -> src:srcLine col 0.
func mapJSON(src string, srcLineDelta int) []byte {
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
	return []byte(fmt.Sprintf(`{"version":3,"sources":[%q],"names":[],"mappings":%q}`,
		src, enc(0)+enc(0)+enc(srcLineDelta)+enc(0)))
}

// End-to-end: the same stack symbolicated against two coexisting
// versions must yield each version's own originals, reproducibly,
// under any concurrency.
func TestEndToEndVersionedSymbolication(t *testing.T) {
	s := store.New(store.Config{MaxBytes: 1 << 20})
	if err := s.Load("v1", "bundle.js", mapJSON("old/app.js", 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Load("v2", "bundle.js", mapJSON("new/app.js", 9)); err != nil {
		t.Fatal(err)
	}
	trace := "Error: boom\n    at a (bundle.js:1:0)\n    at b (unknown.js:5:2)"
	frames := symbolicate.ParseStack(trace)

	want := map[string]struct {
		src  string
		line int
	}{"v1": {"old/app.js", 1}, "v2": {"new/app.js", 10}}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ver := []string{"v1", "v2"}[i%2]
			for j := 0; j < 200; j++ {
				res := symbolicate.SymbolicateStack(ver, frames, s)
				if len(res) != 2 {
					t.Errorf("stack dropped: %d frames", len(res))
					return
				}
				if res[0].Status != symbolicate.StatusOK ||
					res[0].Position.Source != want[ver].src ||
					res[0].Position.Line != want[ver].line {
					t.Errorf("%s: wrong resolution: %+v", ver, res[0])
					return
				}
				if res[1].Status != symbolicate.StatusNoMap {
					t.Errorf("unknown.js should be no_map: %+v", res[1])
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

// A bad map loaded for one version must not change results of another.
func TestBadMapIsolationEndToEnd(t *testing.T) {
	s := store.New(store.Config{})
	if err := s.Load("v1", "bundle.js", mapJSON("app.js", 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Load("v2", "bundle.js", []byte(`{"version":3,`)); !sourcemap.IsTruncated(err) {
		t.Fatalf("want truncated error, got %v", err)
	}
	frames := symbolicate.ParseStack("    at a (bundle.js:1:0)")
	res := symbolicate.SymbolicateStack("v1", frames, s)
	if res[0].Status != symbolicate.StatusOK || res[0].Position.Source != "app.js" {
		t.Fatalf("v1 polluted by bad v2 map: %+v", res[0])
	}
	res = symbolicate.SymbolicateStack("v2", frames, s)
	if res[0].Status != symbolicate.StatusNoMap {
		t.Fatalf("bad v2 map must not resolve: %+v", res[0])
	}
}
