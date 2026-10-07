package sourcemap

import "sort"

// MissReason describes why a lookup did not produce a mapping.
type MissReason string

const (
	MissNone           MissReason = ""
	MissLineOutOfRange MissReason = "line_out_of_range"
	MissNoSegment      MissReason = "no_segment"
	MissUnmapped       MissReason = "unmapped_segment"
)

// Position is an original source position.
type Position struct {
	Source string
	Line   int // 1-based
	Column int // 0-based
	Name   string
}

// Lookup resolves a generated position (1-based line, 0-based column)
// to its original position using greatest-lower-bound segment search.
// ok is false when no mapping exists, in which case reason explains why.
// Lookup never fabricates a position.
func (m *SourceMap) Lookup(line, column int) (Position, bool, MissReason) {
	if line < 1 || line > len(m.lines) {
		return Position{}, false, MissLineOutOfRange
	}
	segs := m.lines[line-1]
	if len(segs) == 0 {
		return Position{}, false, MissNoSegment
	}
	// Greatest lower bound: last segment with genCol <= column.
	idx := sort.Search(len(segs), func(i int) bool {
		return segs[i].genCol > column
	}) - 1
	if idx < 0 {
		return Position{}, false, MissNoSegment
	}
	seg := segs[idx]
	if seg.srcIdx < 0 {
		// Segment exists but carries no original source info.
		return Position{}, false, MissUnmapped
	}
	pos := Position{
		Source: m.Sources[seg.srcIdx],
		Line:   seg.srcLine + 1,
		Column: seg.srcCol,
	}
	if seg.hasName {
		pos.Name = m.Names[seg.nameIdx]
	}
	return pos, true, MissNone
}

// SegmentCount returns the total number of decoded mapping segments.
// It is intended for memory accounting by containers of SourceMap.
func (m *SourceMap) SegmentCount() int {
	n := 0
	for _, line := range m.lines {
		n += len(line)
	}
	return n
}

// LineCount returns the number of generated lines in the map.
func (m *SourceMap) LineCount() int {
	return len(m.lines)
}
