package stacktrace

import "testing"

func TestParseV8(t *testing.T) {
	trace := `Error: boom
    at foo (http://cdn.example.com/app.min.js:1:1234)
    at http://cdn.example.com/app.min.js:2:5
    at bar (http://cdn.example.com/app.min.js:3)
    at baz (native)`
	frames := Parse(trace)
	if len(frames) != 5 {
		t.Fatalf("want 5 frames, got %d: %+v", len(frames), frames)
	}
	if frames[0].File != "" || frames[0].Raw != "Error: boom" {
		t.Fatalf("message line: %+v", frames[0])
	}
	f := frames[1]
	if f.Function != "foo" || f.File != "http://cdn.example.com/app.min.js" || f.Line != 1 || f.Column != 1234 {
		t.Fatalf("frame 1: %+v", f)
	}
	f = frames[2]
	if f.Function != "" || f.File != "http://cdn.example.com/app.min.js" || f.Line != 2 || f.Column != 5 {
		t.Fatalf("frame 2: %+v", f)
	}
	// Missing column.
	f = frames[3]
	if f.Function != "bar" || f.Line != 3 || f.Column != -1 || !f.HasLine {
		t.Fatalf("frame 3: %+v", f)
	}
	// No location at all.
	f = frames[4]
	if f.File != "" || f.HasLine {
		t.Fatalf("frame 4: %+v", f)
	}
}

func TestParseFirefox(t *testing.T) {
	trace := `foo@http://example.com/app.min.js:1:100
@http://example.com/app.min.js:2:7
bar@http://example.com/app.min.js:3`
	frames := Parse(trace)
	if len(frames) != 3 {
		t.Fatalf("want 3 frames, got %d", len(frames))
	}
	if frames[0].Function != "foo" || frames[0].Line != 1 || frames[0].Column != 100 {
		t.Fatalf("frame 0: %+v", frames[0])
	}
	if frames[1].Function != "" || frames[1].Line != 2 || frames[1].Column != 7 {
		t.Fatalf("frame 1: %+v", frames[1])
	}
	if frames[2].Function != "bar" || frames[2].Line != 3 || frames[2].Column != -1 {
		t.Fatalf("frame 2: %+v", frames[2])
	}
}

func TestParseEmptyAndGarbage(t *testing.T) {
	if got := Parse(""); len(got) != 0 {
		t.Fatalf("empty trace: %+v", got)
	}
	frames := Parse("some random line\n\n   \n")
	if len(frames) != 1 || frames[0].Raw != "some random line" || frames[0].File != "" {
		t.Fatalf("garbage: %+v", frames)
	}
}
