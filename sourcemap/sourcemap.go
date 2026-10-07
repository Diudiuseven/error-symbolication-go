// Package sourcemap parses source map v3 files and resolves generated
// positions back to original source positions.
package sourcemap

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ErrCorrupt indicates the mapping content is malformed.
var ErrCorrupt = errors.New("sourcemap: corrupt mapping data")

// ErrTruncated indicates the mapping content ends unexpectedly.
var ErrTruncated = errors.New("sourcemap: truncated mapping data")

// Mapping is a single resolved position mapping.
type Mapping struct {
	Source   string // original file name
	Line     int    // original line, 1-based
	Column   int    // original column, 0-based
	Function string // original function name, may be empty
}

// segment is one decoded mapping segment on a generated line.
type segment struct {
	genCol  int
	source  int // index into sources, -1 if unmapped
	srcLine int // 0-based
	srcCol  int
	name    int // index into names, -1 if absent
}

// SourceMap is a parsed source map for one generated artifact.
// It is immutable after Parse returns and safe for concurrent use.
type SourceMap struct {
	Version int
	File    string

	sources []string
	names   []string
	// lines[i] holds the sorted segments of generated line i (0-based).
	lines [][]segment
}

type rawMap struct {
	Version  int      `json:"version"`
	File     string   `json:"file"`
	Sources  []string `json:"sources"`
	Names    []string `json:"names"`
	Mappings string   `json:"mappings"`
}

// Parse parses source map v3 JSON content. Content that ends unexpectedly
// (cut JSON, cut VLQ sequence) yields ErrTruncated; malformed but complete
// content yields ErrCorrupt.
func Parse(data []byte) (*SourceMap, error) {
	var raw rawMap
	dec := json.NewDecoder(strings.NewReader(string(data)))
	if err := dec.Decode(&raw); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: %v", ErrTruncated, err)
		}
		var se *json.SyntaxError
		if errors.As(err, &se) && strings.Contains(se.Error(), "unexpected end") {
			return nil, fmt.Errorf("%w: %v", ErrTruncated, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if raw.Version != 3 {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrCorrupt, raw.Version)
	}
	m := &SourceMap{
		Version: raw.Version,
		File:    raw.File,
		sources: raw.Sources,
		names:   raw.Names,
	}
	if err := m.decodeMappings(raw.Mappings); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *SourceMap) decodeMappings(mappings string) error {
	rawLines := strings.Split(mappings, ";")
	m.lines = make([][]segment, len(rawLines))
	var src, srcLine, srcCol, name int
	for ln, rawLine := range rawLines {
		if rawLine == "" {
			continue
		}
		genCol := 0
		segs := make([]segment, 0, strings.Count(rawLine, ",")+1)
		for _, rawSeg := range strings.Split(rawLine, ",") {
			vals, err := decodeVLQ(rawSeg)
			if err != nil {
				if errors.Is(err, errVLQTruncated) {
					return fmt.Errorf("%w: line %d: %v", ErrTruncated, ln+1, err)
				}
				return fmt.Errorf("%w: line %d: %v", ErrCorrupt, ln+1, err)
			}
			if len(vals) != 1 && len(vals) != 4 && len(vals) != 5 {
				return fmt.Errorf("%w: line %d: segment has %d fields", ErrCorrupt, ln+1, len(vals))
			}
			genCol += vals[0]
			seg := segment{genCol: genCol, source: -1, name: -1}
			if len(vals) >= 4 {
				src += vals[1]
				srcLine += vals[2]
				srcCol += vals[3]
				if src < 0 || src >= len(m.sources) {
					return fmt.Errorf("%w: line %d: source index %d out of range", ErrCorrupt, ln+1, src)
				}
				seg.source, seg.srcLine, seg.srcCol = src, srcLine, srcCol
				if len(vals) == 5 {
					name += vals[4]
					if name < 0 || name >= len(m.names) {
						return fmt.Errorf("%w: line %d: name index %d out of range", ErrCorrupt, ln+1, name)
					}
					seg.name = name
				}
			}
			segs = append(segs, seg)
		}
		m.lines[ln] = segs
	}
	return nil
}

// Lookup resolves a generated position (1-based line, 0-based column) to the
// original position. Returns false when the position is not covered by any
// mapping segment.
func (m *SourceMap) Lookup(line, column int) (Mapping, bool) {
	if line < 1 || line > len(m.lines) {
		return Mapping{}, false
	}
	segs := m.lines[line-1]
	if len(segs) == 0 || column < segs[0].genCol {
		return Mapping{}, false
	}
	// Greatest segment with genCol <= column.
	i := sort.Search(len(segs), func(i int) bool { return segs[i].genCol > column }) - 1
	seg := segs[i]
	if seg.source < 0 {
		return Mapping{}, false
	}
	mp := Mapping{
		Source: m.sources[seg.source],
		Line:   seg.srcLine + 1,
		Column: seg.srcCol,
	}
	if seg.name >= 0 {
		mp.Function = m.names[seg.name]
	}
	return mp, true
}
