package sourcemap

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Kind classifies a parse failure so callers can distinguish a corrupt
// map from a truncated one.
type Kind string

const (
	KindCorrupt   Kind = "corrupt"
	KindTruncated Kind = "truncated"
)

// ParseError describes why a source map failed to parse.
type ParseError struct {
	Kind Kind
	Err  error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("sourcemap: %s map: %v", e.Kind, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

// IsTruncated reports whether err indicates a truncated map.
func IsTruncated(err error) bool {
	var pe *ParseError
	return errors.As(err, &pe) && pe.Kind == KindTruncated
}

// IsCorrupt reports whether err indicates a corrupt (malformed) map.
func IsCorrupt(err error) bool {
	var pe *ParseError
	return errors.As(err, &pe) && pe.Kind == KindCorrupt
}

// SourceMap represents a parsed Source Map v3 document.
// It is immutable after Parse returns and safe for concurrent use.
type SourceMap struct {
	Version int
	File    string
	Sources []string
	Names   []string
	// lines holds decoded mapping segments per generated line (0-based),
	// each sorted by generated column.
	lines [][]segment
}

// segment is a single decoded mapping segment.
type segment struct {
	genCol  int
	srcIdx  int
	srcLine int // 0-based
	srcCol  int
	nameIdx int
	hasName bool
}

type rawMap struct {
	Version  int      `json:"version"`
	File     string   `json:"file"`
	Sources  []string `json:"sources"`
	Names    []string `json:"names"`
	Mappings string   `json:"mappings"`
}

// Parse parses a Source Map v3 JSON document. The returned map is
// immutable. Failures are classified as corrupt or truncated via
// *ParseError.
func Parse(data []byte) (*SourceMap, error) {
	var raw rawMap
	if err := json.Unmarshal(data, &raw); err != nil {
		kind := KindCorrupt
		if strings.Contains(err.Error(), "unexpected end of JSON input") {
			kind = KindTruncated
		}
		return nil, &ParseError{Kind: kind, Err: err}
	}
	if raw.Version != 3 {
		return nil, &ParseError{Kind: KindCorrupt,
			Err: fmt.Errorf("unsupported version %d (want 3)", raw.Version)}
	}
	m := &SourceMap{
		Version: raw.Version,
		File:    raw.File,
		Sources: raw.Sources,
		Names:   raw.Names,
	}
	if err := m.decodeMappings(raw.Mappings); err != nil {
		return nil, err
	}
	return m, nil
}

// decodeMappings decodes the VLQ "mappings" field into per-line segments.
func (m *SourceMap) decodeMappings(mappings string) error {
	lines := strings.Split(mappings, ";")
	m.lines = make([][]segment, len(lines))
	var srcIdx, srcLine, srcCol, nameIdx int
	for i, line := range lines {
		if line == "" {
			continue
		}
		var genCol int
		segs := []segment{}
		for _, segStr := range strings.Split(line, ",") {
			vals, err := decodeVLQ(segStr)
			if err != nil {
				kind := KindCorrupt
				if strings.Contains(err.Error(), "truncated") {
					kind = KindTruncated
				}
				return &ParseError{Kind: kind,
					Err: fmt.Errorf("line %d: %w", i+1, err)}
			}
			if len(vals) != 1 && len(vals) != 4 && len(vals) != 5 {
				return &ParseError{Kind: KindCorrupt,
					Err: fmt.Errorf("line %d: segment has %d fields (want 1, 4 or 5)", i+1, len(vals))}
			}
			genCol += vals[0]
			seg := segment{genCol: genCol, srcIdx: -1, nameIdx: -1}
			if len(vals) >= 4 {
				srcIdx += vals[1]
				srcLine += vals[2]
				srcCol += vals[3]
				if srcIdx < 0 || srcIdx >= len(m.Sources) {
					return &ParseError{Kind: KindCorrupt,
						Err: fmt.Errorf("line %d: source index %d out of range", i+1, srcIdx)}
				}
				seg.srcIdx = srcIdx
				seg.srcLine = srcLine
				seg.srcCol = srcCol
			}
			if len(vals) == 5 {
				nameIdx += vals[4]
				if nameIdx < 0 || nameIdx >= len(m.Names) {
					return &ParseError{Kind: KindCorrupt,
						Err: fmt.Errorf("line %d: name index %d out of range", i+1, nameIdx)}
				}
				seg.nameIdx = nameIdx
				seg.hasName = true
			}
			segs = append(segs, seg)
		}
		// Segments are generated in column order by spec; sort defensively
		// to keep lookups deterministic even for non-conforming input.
		sort.Slice(segs, func(a, b int) bool { return segs[a].genCol < segs[b].genCol })
		m.lines[i] = segs
	}
	return nil
}
