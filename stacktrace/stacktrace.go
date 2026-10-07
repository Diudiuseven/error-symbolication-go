// Package stacktrace parses error stack traces from multiple runtimes
// (V8/Chrome/Node, Firefox/Safari style) into structured frames.
package stacktrace

import (
	"regexp"
	"strconv"
	"strings"
)

// Frame is a single parsed stack frame. Raw always keeps the original text.
type Frame struct {
	Raw      string
	Function string // empty when the runtime omitted it
	File     string // empty when the line is not a recognizable frame
	Line     int
	Column   int  // -1 when the runtime omitted the column
	HasLine  bool // true when a line number was present
}

var (
	// V8: "at fn (file:line:col)", "at file:line:col", column optional.
	v8WithFn = regexp.MustCompile(`^\s*at\s+(.+?)\s+\((.+?):(\d+)(?::(\d+))?\)\s*$`)
	v8NoFn   = regexp.MustCompile(`^\s*at\s+(.+?):(\d+)(?::(\d+))?\s*$`)
	// Firefox/Safari: "fn@file:line:col", "@file:line:col", column optional.
	firefox = regexp.MustCompile(`^(.*?)@(.+?):(\d+)(?::(\d+))?\s*$`)
)

// Parse parses a multi-line stack trace into frames. Lines that do not look
// like frames (e.g. the leading error message) are preserved as frames with
// only Raw set, so no information is ever dropped.
func Parse(trace string) []Frame {
	lines := strings.Split(strings.ReplaceAll(trace, "\r\n", "\n"), "\n")
	frames := make([]Frame, 0, len(lines))
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		frames = append(frames, parseLine(ln))
	}
	return frames
}

func parseLine(ln string) Frame {
	f := Frame{Raw: ln, Column: -1}
	if m := v8WithFn.FindStringSubmatch(ln); m != nil {
		f.Function = m[1]
		fillLoc(&f, m[2], m[3], m[4])
		return f
	}
	if m := v8NoFn.FindStringSubmatch(ln); m != nil {
		fillLoc(&f, m[1], m[2], m[3])
		return f
	}
	if m := firefox.FindStringSubmatch(ln); m != nil {
		f.Function = m[1]
		fillLoc(&f, m[2], m[3], m[4])
		return f
	}
	return f
}

func fillLoc(f *Frame, file, line, col string) {
	f.File = file
	if n, err := strconv.Atoi(line); err == nil {
		f.Line, f.HasLine = n, true
	}
	if col != "" {
		if n, err := strconv.Atoi(col); err == nil {
			f.Column = n
		}
	}
}
