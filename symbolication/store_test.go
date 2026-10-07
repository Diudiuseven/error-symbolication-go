package symbolication

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

const b64chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func encodeVLQ(vals ...int) string {
	var sb strings.Builder
	for _, v := range vals {
		n := v << 1
		if v < 0 {
			n = (-v << 1) | 1
		}
		for {
			d := n & 31
			n >>= 5
			if n > 0 {
				d |= 32
			}
			sb.WriteByte(b64chars[d])
			if n == 0 {
				break
			}
		}
	}
	return sb.String()
}

// mapJSON builds a one-line source map. seg is [genCol, srcIdx, srcLine, srcCol, nameIdx].
func mapJSON(source, name string, seg []int) string {
	names := "[]"
	if name != "" {
		names = fmt.Sprintf(`[%q]`, name)
	}
	return fmt.Sprintf(`{"version":3,"file":"app.min.js","sources":[%q],"names":%s,"mappings":"%s"}`,
		source, names, encodeVLQ(seg...))
}

const traceV8 = `Error: boom
    at foo (http://cdn.example.com/app.min.js:1:0)
    at http://cdn.example.com/app.min.js:1:10`

func TestCrossVersionResolution(t *testing.T) {
	s := NewStore()
	// v1: genCol 0 -> old.ts:10:2 "fooV1"; v2: same position -> new.ts:20:4 "fooV2".
	s.Load("v1", "app.min.js", []byte(mapJSON("src/old.ts", "fooV1", []int{0, 0, 9, 2, 0})))
	s.Load("v2", "app.min.js", []byte(mapJSON("src/new.ts", "fooV2", []int{0, 0, 19, 4, 0})))

	r1 := s.Resolve("v1", traceV8)
	r2 := s.Resolve("v2", traceV8)
	f1, f2 := r1.Frames[1], r2.Frames[1]
	if f1.Status != StatusResolved || f1.Source != "src/old.ts" || f1.Line != 10 || f1.Column != 2 || f1.Function != "fooV1" {
		t.Fatalf("v1 frame: %+v", f1)
	}
	if f2.Status != StatusResolved || f2.Source != "src/new.ts" || f2.Line != 20 || f2.Column != 4 || f2.Function != "fooV2" {
		t.Fatalf("v2 frame: %+v", f2)
	}
	// Unknown version: every frame reports no_map, nothing fabricated.
	r3 := s.Resolve("v9", traceV8)
	for _, f := range r3.Frames {
		if f.Status == StatusResolved {
			t.Fatalf("v9 unexpectedly resolved: %+v", f)
		}
	}
	if r3.Frames[1].Status != StatusNoMap {
		t.Fatalf("v9 frame: %+v", r3.Frames[1])
	}
}

func TestCorruptAndTruncatedIsolation(t *testing.T) {
	s := NewStore()
	s.Load("v1", "good.min.js", []byte(mapJSON("src/a.ts", "foo", []int{0, 0, 0, 0, 0})))
	s.Load("v1", "corrupt.min.js", []byte(`{"version":3,"sources":["x"],"names":[],"mappings":"A!A"}`))
	s.Load("v1", "trunc.min.js", []byte(`{"version":3,"sources":["x"],"names":[],"mappings":"g"}`))
	s.Load("v2", "corrupt.min.js", []byte(mapJSON("src/b.ts", "bar", []int{0, 0, 4, 1, 0})))

	trace := `Error: x
    at f (http://e.com/good.min.js:1:0)
    at g (http://e.com/corrupt.min.js:1:0)
    at h (http://e.com/trunc.min.js:1:0)`
	r := s.Resolve("v1", trace)
	if r.Frames[1].Status != StatusResolved || r.Frames[1].Source != "src/a.ts" {
		t.Fatalf("good map polluted: %+v", r.Frames[1])
	}
	if r.Frames[2].Status != StatusCorruptMap {
		t.Fatalf("corrupt: %+v", r.Frames[2])
	}
	if r.Frames[3].Status != StatusTruncatedMap {
		t.Fatalf("truncated: %+v", r.Frames[3])
	}
	// Same artifact name under another version is unaffected.
	r2 := s.Resolve("v2", `at g (http://e.com/corrupt.min.js:1:0)`)
	if r2.Frames[0].Status != StatusResolved || r2.Frames[0].Source != "src/b.ts" || r2.Frames[0].Line != 5 {
		t.Fatalf("v2 corrupt.min.js: %+v", r2.Frames[0])
	}
}

func TestAtomicReplaceConsistency(t *testing.T) {
	s := NewStore()
	s.Load("v1", "app.min.js", []byte(mapJSON("src/v1a.ts", "fnA", []int{0, 0, 0, 0, 0})))
	trace := `at f (http://e.com/app.min.js:1:0)`

	var wg sync.WaitGroup
	stop := make(chan struct{})
	bad := make(chan ResolvedFrame, 1)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				f := s.Resolve("v1", trace).Frames[0]
				// Any single observation must be fully the old map or
				// fully the new one, never a field-level mix.
				okA := f.Source == "src/v1a.ts" && f.Function == "fnA"
				okB := f.Source == "src/v1b.ts" && f.Function == "fnB"
				if f.Status == StatusResolved && !okA && !okB {
					select {
					case bad <- f:
					default:
					}
					return
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		if i%2 == 0 {
			s.Load("v1", "app.min.js", []byte(mapJSON("src/v1b.ts", "fnB", []int{0, 0, 1, 1, 0})))
		} else {
			s.Load("v1", "app.min.js", []byte(mapJSON("src/v1a.ts", "fnA", []int{0, 0, 0, 0, 0})))
		}
	}
	close(stop)
	wg.Wait()
	select {
	case f := <-bad:
		t.Fatalf("observed mixed state: %+v", f)
	default:
	}
}

func TestDeterminismUnderConcurrency(t *testing.T) {
	s := NewStore()
	s.Load("v1", "app.min.js", []byte(mapJSON("src/a.ts", "foo", []int{0, 0, 9, 2, 0})))
	want := s.Resolve("v1", traceV8)

	const n = 64
	results := make([]Result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = s.Resolve("v1", traceV8)
		}(i)
	}
	wg.Wait()
	for i, r := range results {
		if len(r.Frames) != len(want.Frames) {
			t.Fatalf("run %d: frame count differs", i)
		}
		for j := range r.Frames {
			if r.Frames[j] != want.Frames[j] {
				t.Fatalf("run %d frame %d: got %+v want %+v", i, j, r.Frames[j], want.Frames[j])
			}
		}
	}
}

func TestMissingInfoFrames(t *testing.T) {
	s := NewStore()
	s.Load("v1", "app.min.js", []byte(mapJSON("src/a.ts", "", []int{0, 0, 0, 0})))
	trace := `Error: boom
    at bar (http://e.com/app.min.js:1)
    at baz (native)
some unparseable line`
	r := s.Resolve("v1", trace)
	if len(r.Frames) != 4 {
		t.Fatalf("want 4 frames, got %d", len(r.Frames))
	}
	// Missing column: best-effort resolve with column 0.
	if r.Frames[1].Status != StatusResolved || r.Frames[1].Source != "src/a.ts" {
		t.Fatalf("missing column frame: %+v", r.Frames[1])
	}
	// No location: kept raw, marked, stack not dropped.
	if r.Frames[2].Status != StatusUnparseableLine || r.Frames[2].Raw == "" {
		t.Fatalf("native frame: %+v", r.Frames[2])
	}
	if r.Frames[3].Status != StatusUnparseableLine || r.Frames[3].Raw != "some unparseable line" {
		t.Fatalf("garbage frame: %+v", r.Frames[3])
	}
}

func TestNoMappingPosition(t *testing.T) {
	s := NewStore()
	s.Load("v1", "app.min.js", []byte(mapJSON("src/a.ts", "", []int{0, 0, 0, 0})))
	// Line 5 is not in the map at all.
	r := s.Resolve("v1", `at f (http://e.com/app.min.js:5:0)`)
	if r.Frames[0].Status != StatusNoMapping {
		t.Fatalf("want no_mapping, got %+v", r.Frames[0])
	}
	if r.Frames[0].Line != 5 || r.Frames[0].Source != "http://e.com/app.min.js" {
		t.Fatalf("original info not preserved: %+v", r.Frames[0])
	}
}

func TestMapByteBudgetEviction(t *testing.T) {
	good := mapJSON("src/a.ts", "foo", []int{0, 0, 0, 0, 0})
	opts := Options{MaxMapBytes: int64(len(good))*2 + 10, MaxConcurrent: 4}
	s := NewStoreWith(opts)
	s.Load("v1", "a.min.js", []byte(good))
	s.Load("v1", "b.min.js", []byte(good))
	s.Load("v1", "c.min.js", []byte(good)) // exceeds budget, evicts LRU (a)
	maps, bytes := s.Stats()
	if maps > 2 || bytes > opts.MaxMapBytes {
		t.Fatalf("budget violated: maps=%d bytes=%d", maps, bytes)
	}
	// Evicted map reports no_map; survivors still resolve.
	if got := s.Resolve("v1", `at f (http://e.com/a.min.js:1:0)`).Frames[0].Status; got != StatusNoMap {
		t.Fatalf("evicted a.min.js: %v", got)
	}
	if got := s.Resolve("v1", `at f (http://e.com/c.min.js:1:0)`).Frames[0].Status; got != StatusResolved {
		t.Fatalf("surviving c.min.js: %v", got)
	}
}

func TestTraceSizeLimit(t *testing.T) {
	s := NewStoreWith(Options{MaxTraceBytes: 16})
	r := s.Resolve("v1", strings.Repeat("x", 32))
	if r.Err != ErrTraceTooLarge {
		t.Fatalf("want ErrTraceTooLarge, got %v", r.Err)
	}
}

func TestConcurrencyLimitDoesNotDeadlock(t *testing.T) {
	s := NewStoreWith(Options{MaxConcurrent: 2})
	s.Load("v1", "app.min.js", []byte(mapJSON("src/a.ts", "foo", []int{0, 0, 0, 0, 0})))
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := s.Resolve("v1", `at f (http://e.com/app.min.js:1:0)`)
			if r.Frames[0].Status != StatusResolved {
				t.Errorf("status: %v", r.Frames[0].Status)
			}
		}()
	}
	wg.Wait()
}
