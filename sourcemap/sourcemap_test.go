package sourcemap

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const b64chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// encodeVLQ encodes signed integers as base64 VLQ (test helper).
func encodeVLQ(vals ...int) string {
	var sb strings.Builder
	for _, v := range vals {
		n := v << 1
		if v < 0 {
			n = (-v << 1) | 1
		}
		for {
			d := n & 31
			n >>= 5
			if n > 0 {
				d |= 32
			}
			sb.WriteByte(b64chars[d])
			if n == 0 {
				break
			}
		}
	}
	return sb.String()
}

// buildMap constructs a source map JSON with one generated line whose
// segments are given as [genCol, srcIdx, srcLine, srcCol, nameIdx] deltas.
func buildMap(sources, names []string, segs ...[]int) string {
	var parts []string
	prev := make([]int, 5)
	for _, s := range segs {
		d := make([]int, len(s))
		for i := range s {
			d[i] = s[i] - prev[i]
			prev[i] = s[i]
		}
		parts = append(parts, encodeVLQ(d...))
	}
	srcJSON, _ := jsonMarshal(sources)
	nameJSON, _ := jsonMarshal(names)
	return fmt.Sprintf(`{"version":3,"file":"app.min.js","sources":%s,"names":%s,"mappings":"%s"}`,
		srcJSON, nameJSON, strings.Join(parts, ","))
}

func jsonMarshal(ss []string) (string, error) {
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range ss {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%q", s)
	}
	b.WriteByte(']')
	return b.String(), nil
}

func TestParseAndLookup(t *testing.T) {
	// One generated line, two segments:
	//   genCol 0  -> src/a.ts line 1 (0-based 0) col 0, name "foo"
	//   genCol 10 -> src/b.ts line 3 (0-based 2) col 4, no name
	data := buildMap(
		[]string{"src/a.ts", "src/b.ts"},
		[]string{"foo"},
		[]int{0, 0, 0, 0, 0},
		[]int{10, 1, 2, 4},
	)
	m, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	mp, ok := m.Lookup(1, 0)
	if !ok {
		t.Fatal("Lookup(1,0) missed")
	}
	if mp.Source != "src/a.ts" || mp.Line != 1 || mp.Column != 0 || mp.Function != "foo" {
		t.Fatalf("got %+v", mp)
	}
	// Column between segments resolves to the first segment.
	mp, ok = m.Lookup(1, 9)
	if !ok || mp.Source != "src/a.ts" {
		t.Fatalf("Lookup(1,9) = %+v, %v", mp, ok)
	}
	mp, ok = m.Lookup(1, 10)
	if !ok || mp.Source != "src/b.ts" || mp.Line != 3 || mp.Column != 4 || mp.Function != "" {
		t.Fatalf("Lookup(1,10) = %+v, %v", mp, ok)
	}
	// Unmapped line.
	if _, ok := m.Lookup(2, 0); ok {
		t.Fatal("Lookup(2,0) should miss")
	}
}

func TestParseTruncated(t *testing.T) {
	full := buildMap([]string{"a.ts"}, nil, []int{0, 0, 0, 0})
	// Cut the JSON mid-way.
	if _, err := Parse([]byte(full[:len(full)-5])); !errors.Is(err, ErrTruncated) {
		t.Fatalf("cut json: want ErrTruncated, got %v", err)
	}
	// Cut a VLQ sequence: mappings value ends with a continuation char.
	cut := `{"version":3,"sources":["a.ts"],"names":[],"mappings":"g"}`
	if _, err := Parse([]byte(cut)); !errors.Is(err, ErrTruncated) {
		t.Fatalf("cut vlq: want ErrTruncated, got %v", err)
	}
}

func TestParseCorrupt(t *testing.T) {
	cases := map[string]string{
		"bad json":       `{not json}`,
		"wrong version":  `{"version":2,"sources":[],"names":[],"mappings":""}`,
		"bad vlq char":   `{"version":3,"sources":["a"],"names":[],"mappings":"A!A"}`,
		"bad seg fields": `{"version":3,"sources":["a"],"names":[],"mappings":"` + encodeVLQ(1, 2, 3) + `"}`,
		"source oob":     `{"version":3,"sources":["a"],"names":[],"mappings":"` + encodeVLQ(0, 5, 0, 0) + `"}`,
		"name oob":       `{"version":3,"sources":["a"],"names":[],"mappings":"` + encodeVLQ(0, 0, 0, 0, 3) + `"}`,
	}
	for name, data := range cases {
		if _, err := Parse([]byte(data)); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("%s: want ErrCorrupt, got %v", name, err)
		}
	}
}

func TestParseEmptyMappings(t *testing.T) {
	m, err := Parse([]byte(`{"version":3,"sources":[],"names":[],"mappings":""}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, ok := m.Lookup(1, 0); ok {
		t.Fatal("empty map should miss")
	}
}
