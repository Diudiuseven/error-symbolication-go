package symbolicate

import (
	"testing"

	"github.com/Diudiuseven/error-symbolication-go/sourcemap"
)

// test map: generated line 1 col 0 -> src/app.js:1:0 name "greet".
const testMap = `{"version":3,"file":"bundle.js","sources":["src/app.js"],"names":["greet"],"mappings":"AAAAA"}`

type fakeResolver map[string]*sourcemap.SourceMap

func (f fakeResolver) Resolve(version, file string) (*sourcemap.SourceMap, bool) {
	m, ok := f[version+"|"+file]
	return m, ok
}

func newResolver(t *testing.T) fakeResolver {
	t.Helper()
	m, err := sourcemap.Parse([]byte(testMap))
	if err != nil {
		t.Fatal(err)
	}
	return fakeResolver{"v1|bundle.js": m}
}

func TestParseV8Formats(t *testing.T) {
	trace := `Error: boom
    at greet (http://x/bundle.js:1:10)
    at http://x/bundle.js:2:5
    at async run (http://x/bundle.js:3)`
	frames := ParseStack(trace)
	if len(frames) != 3 {
		t.Fatalf("want 3 frames, got %d: %+v", len(frames), frames)
	}
	if frames[0].Function != "greet" || frames[0].File != "http://x/bundle.js" ||
		frames[0].Line != 1 || frames[0].Column != 10 || !frames[0].HasCol {
		t.Fatalf("frame 0: %+v", frames[0])
	}
	if frames[1].Function != "" || frames[1].Line != 2 || frames[1].Column != 5 {
		t.Fatalf("frame 1: %+v", frames[1])
	}
	// Column missing.
	if frames[2].Function != "run" || frames[2].Line != 3 || frames[2].HasCol || frames[2].Column != -1 {
		t.Fatalf("frame 2: %+v", frames[2])
	}
}

func TestParseGeckoFormat(t *testing.T) {
	trace := "greet@http://x/bundle.js:1:10\n@http://x/bundle.js:2:5\nnoop@http://x/bundle.js:3"
	frames := ParseStack(trace)
	if len(frames) != 3 {
		t.Fatalf("want 3 frames, got %d", len(frames))
	}
	if frames[0].Function != "greet" || frames[0].Column != 10 {
		t.Fatalf("frame 0: %+v", frames[0])
	}
	if frames[1].Function != "" || frames[1].Line != 2 {
		t.Fatalf("frame 1: %+v", frames[1])
	}
	if frames[2].HasCol {
		t.Fatalf("frame 2 should lack column: %+v", frames[2])
	}
}

func TestSymbolicateHit(t *testing.T) {
	r := newResolver(t)
	frames := ParseStack("    at minified (bundle.js:1:0)")
	res := SymbolicateStack("v1", frames, r)
	if len(res) != 1 || res[0].Status != StatusOK {
		t.Fatalf("want ok, got %+v", res)
	}
	p := res[0].Position
	if p.Source != "src/app.js" || p.Line != 1 || p.Column != 0 || p.Name != "greet" {
		t.Fatalf("position: %+v", p)
	}
}

func TestSymbolicateMissingColumnBestEffort(t *testing.T) {
	r := newResolver(t)
	frames := ParseStack("    at minified (bundle.js:1)")
	res := SymbolicateStack("v1", frames, r)
	if res[0].Status != StatusOK {
		t.Fatalf("missing column should still resolve, got %+v", res[0])
	}
}

func TestSymbolicateFailuresKeepRawInfo(t *testing.T) {
	r := newResolver(t)
	trace := `    at a (bundle.js:1:0)
    at b (other.js:1:0)
    at c (bundle.js:99:0)
garbage line without frame`
	frames := ParseStack(trace)
	if len(frames) != 3 {
		t.Fatalf("want 3 frames, got %d", len(frames))
	}
	res := SymbolicateStack("v1", frames, r)
	if res[0].Status != StatusOK {
		t.Fatalf("frame 0: %+v", res[0])
	}
	// No map for other.js: kept with raw info.
	if res[1].Status != StatusNoMap || res[1].Frame.File != "other.js" || res[1].Frame.Line != 1 {
		t.Fatalf("frame 1: %+v", res[1])
	}
	// Map exists but line 99 unmapped.
	if res[2].Status != StatusMiss || res[2].Reason != sourcemap.MissLineOutOfRange {
		t.Fatalf("frame 2: %+v", res[2])
	}
	// One failing frame must not drop the rest of the stack.
	if len(res) != 3 {
		t.Fatalf("stack truncated: %+v", res)
	}
}

func TestSymbolicateVersionPinned(t *testing.T) {
	r := newResolver(t)
	frames := ParseStack("    at a (bundle.js:1:0)")
	// v2 has no maps registered: must not fall back to v1.
	res := SymbolicateStack("v2", frames, r)
	if res[0].Status != StatusNoMap {
		t.Fatalf("cross-version fallback happened: %+v", res[0])
	}
}
