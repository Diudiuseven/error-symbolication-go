// Package symbolication resolves minified stack traces back to original
// source positions using versioned source maps.
package symbolication

import "errors"

// ErrTraceTooLarge is returned when an input trace exceeds the configured
// per-request size limit.
var ErrTraceTooLarge = errors.New("symbolication: trace exceeds size limit")

// Status describes the outcome of resolving a single frame.
type Status string

const (
	StatusResolved        Status = "resolved"         // mapped to an original position
	StatusNoMap           Status = "no_map"           // no map for version/artifact
	StatusNoMapping       Status = "no_mapping"       // position not covered by the map
	StatusCorruptMap      Status = "corrupt_map"      // map content malformed
	StatusTruncatedMap    Status = "truncated_map"    // map content truncated
	StatusUnparseableLine Status = "unparseable_line" // frame line not recognized
)

// ResolvedFrame is the result for one stack frame. Raw always preserves the
// original frame text; unresolved frames keep their parsed fields untouched.
type ResolvedFrame struct {
	Raw      string
	Status   Status
	Source   string // original file (resolved), else the generated file
	Line     int    // original line (resolved), else the generated line
	Column   int    // original column (resolved), else the generated column
	Function string // original function name; falls back to the frame's name
}

// Result is the resolved form of one error stack trace.
type Result struct {
	Version string
	Frames  []ResolvedFrame
	Err     error // non-nil when the whole trace could not be processed
}
