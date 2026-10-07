package symbolicate

import "github.com/Diudiuseven/error-symbolication-go/sourcemap"

// FrameStatus describes the outcome of symbolicating a frame.
type FrameStatus string

const (
	// StatusOK: frame resolved to an original position.
	StatusOK FrameStatus = "ok"
	// StatusNoMap: no source map is registered for the frame's file.
	StatusNoMap FrameStatus = "no_map"
	// StatusMiss: a map exists but contains no mapping for the position.
	StatusMiss FrameStatus = "miss"
	// StatusUnparsed: the frame carries no usable file/line information.
	StatusUnparsed FrameStatus = "unparsed"
)

// Result is the symbolication outcome for one frame. When Status is not
// StatusOK, Frame keeps its original (generated) information untouched.
type Result struct {
	Frame    Frame
	Status   FrameStatus
	Position sourcemap.Position
	Reason   sourcemap.MissReason
}

// Resolver resolves a generated file name to a SourceMap for a given
// version. The returned map must remain valid for the duration of the
// SymbolicateStack call.
type Resolver interface {
	Resolve(version, file string) (*sourcemap.SourceMap, bool)
}

// SymbolicateStack symbolicates every frame against the maps of one
// pinned version. A frame that cannot be resolved is kept with its raw
// info and a distinguishing status; one failing frame never drops the
// rest of the stack. Results are deterministic for fixed input.
func SymbolicateStack(version string, frames []Frame, r Resolver) []Result {
	results := make([]Result, 0, len(frames))
	for _, f := range frames {
		results = append(results, SymbolicateFrame(version, f, r))
	}
	return results
}

// SymbolicateFrame symbolicates a single frame.
func SymbolicateFrame(version string, f Frame, r Resolver) Result {
	res := Result{Frame: f}
	if f.File == "" || f.Line <= 0 {
		res.Status = StatusUnparsed
		return res
	}
	m, ok := r.Resolve(version, f.File)
	if !ok {
		res.Status = StatusNoMap
		return res
	}
	// Column may be absent; fall back to column 0 of the generated line
	// (greatest-lower-bound lookup then finds the line's first segment).
	col := f.Column
	if !f.HasCol {
		col = 0
	}
	pos, ok, reason := m.Lookup(f.Line, col)
	if !ok {
		res.Status = StatusMiss
		res.Reason = reason
		return res
	}
	// If the original function name is unknown from the map, keep the
	// generated one as a best-effort hint.
	if pos.Name == "" {
		pos.Name = f.Function
	}
	res.Status = StatusOK
	res.Position = pos
	return res
}
