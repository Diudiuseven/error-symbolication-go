package symbolicate

import (
	"regexp"
	"strconv"
	"strings"
)

// Frame is a single parsed stack frame. Column may be absent in some
// runtimes; Function may be absent for anonymous frames.
type Frame struct {
	Raw      string
	Function string
	File     string
	Line     int // 1-based; 0 means absent
	Column   int // 0-based; -1 means absent
	HasCol   bool
}

var (
	// V8/Chrome: "    at funcName (file:line:col)" or "    at file:line:col"
	v8Paren = regexp.MustCompile(`^\s*at\s+(?:async\s+)?(.+?)\s+\((.+?):(\d+)(?::(\d+))?\)\s*$`)
	v8Bare  = regexp.MustCompile(`^\s*at\s+(?:async\s+)?(.+?):(\d+)(?::(\d+))?\s*$`)
	// Firefox/Safari: "funcName@file:line:col" or "@file:line:col"
	gecko = regexp.MustCompile(`^(.*?)@(.+?):(\d+)(?::(\d+))?\s*$`)
)

// ParseStack parses a multi-line stack trace into frames, supporting
// V8/Chrome and Firefox/Safari formats, with or without column numbers
// and function names. Unparseable lines are skipped; a header line such
// as "Error: boom" is ignored.
func ParseStack(trace string) []Frame {
	var frames []Frame
	for _, line := range strings.Split(trace, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if f, ok := parseLine(line); ok {
			frames = append(frames, f)
		}
	}
	return frames
}

func parseLine(line string) (Frame, bool) {
	if m := v8Paren.FindStringSubmatch(line); m != nil {
		return makeFrame(line, m[1], m[2], m[3], m[4]), true
	}
	if m := v8Bare.FindStringSubmatch(line); m != nil {
		// Bare form has no function name.
		return makeFrame(line, "", m[1], m[2], m[3]), true
	}
	if m := gecko.FindStringSubmatch(line); m != nil {
		return makeFrame(line, m[1], m[2], m[3], m[4]), true
	}
	return Frame{}, false
}

func makeFrame(raw, fn, file, lineStr, colStr string) Frame {
	ln, err := strconv.Atoi(lineStr)
	if err != nil {
		ln = 0
	}
	f := Frame{
		Raw:      raw,
		Function: strings.TrimSpace(fn),
		File:     file,
		Line:     ln,
		Column:   -1,
	}
	if colStr != "" {
		if c, err := strconv.Atoi(colStr); err == nil {
			f.Column = c
			f.HasCol = true
		}
	}
	return f
}
