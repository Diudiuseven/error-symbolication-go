package symbolication

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	good := mapJSON("src/a.ts", "foo", []int{0, 0, 0, 0, 0})
	p := filepath.Join(dir, "app.min.js.map")
	if err := os.WriteFile(p, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore()
	if err := s.LoadFile("v1", "app.min.js", p); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	r := s.Resolve("v1", `at f (http://e.com/app.min.js:1:0)`)
	if r.Frames[0].Status != StatusResolved || r.Frames[0].Source != "src/a.ts" {
		t.Fatalf("frame: %+v", r.Frames[0])
	}

	// Missing file: error returned, previous map untouched.
	if err := s.LoadFile("v1", "app.min.js", filepath.Join(dir, "nope.map")); err == nil {
		t.Fatal("want error for missing file")
	}
	r = s.Resolve("v1", `at f (http://e.com/app.min.js:1:0)`)
	if r.Frames[0].Status != StatusResolved {
		t.Fatalf("previous map lost after failed load: %+v", r.Frames[0])
	}
}
