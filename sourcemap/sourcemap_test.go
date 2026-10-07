package sourcemap

import "testing"

// validMap maps:
//   generated line 1, col 0 -> src/app.js:1:0 name "greet"
//   generated line 2        -> (no segments)
//   generated line 3, col 0 -> src/app.js:2:4 name "greet"
const validMap = `{"version":3,"file":"bundle.js","sources":["src/app.js"],"names":["greet"],"mappings":"AAAAA;;AACIA"}`

func mustParse(t *testing.T, data string) *SourceMap {
	t.Helper()
	m, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return m
}

func TestLookupHit(t *testing.T) {
	m := mustParse(t, validMap)
	pos, ok, reason := m.Lookup(1, 0)
	if !ok {
		t.Fatalf("Lookup(1,0) missed: %v", reason)
	}
	if pos.Source != "src/app.js" || pos.Line != 1 || pos.Column != 0 || pos.Name != "greet" {
		t.Fatalf("unexpected position: %+v", pos)
	}
}

func TestLookupGreatestLowerBound(t *testing.T) {
	m := mustParse(t, validMap)
	// Column 9 has no exact segment; must resolve to the segment at col 0.
	pos, ok, _ := m.Lookup(3, 9)
	if !ok {
		t.Fatal("Lookup(3,9) missed")
	}
	if pos.Line != 2 || pos.Column != 4 {
		t.Fatalf("unexpected position: %+v", pos)
	}
}

func TestLookupMissReasons(t *testing.T) {
	m := mustParse(t, validMap)
	if _, ok, r := m.Lookup(99, 0); ok || r != MissLineOutOfRange {
		t.Fatalf("line out of range: ok=%v reason=%v", ok, r)
	}
	if _, ok, r := m.Lookup(2, 0); ok || r != MissNoSegment {
		t.Fatalf("empty line: ok=%v reason=%v", ok, r)
	}
	if _, ok, r := m.Lookup(0, 0); ok || r != MissLineOutOfRange {
		t.Fatalf("line 0: ok=%v reason=%v", ok, r)
	}
}

func TestParseCorruptJSON(t *testing.T) {
	_, err := Parse([]byte(`{"version":3,`))
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsTruncated(err) {
		t.Fatalf("truncated JSON should be classified truncated, got: %v", err)
	}
	_, err = Parse([]byte(`not json at all`))
	if err == nil || !IsCorrupt(err) {
		t.Fatalf("garbage should be corrupt, got: %v", err)
	}
}

func TestParseBadVersion(t *testing.T) {
	_, err := Parse([]byte(`{"version":2,"mappings":""}`))
	if err == nil || !IsCorrupt(err) {
		t.Fatalf("bad version should be corrupt, got: %v", err)
	}
}

func TestParseTruncatedVLQ(t *testing.T) {
	// "g" has the continuation bit set but the input ends: truncated.
	_, err := Parse([]byte(`{"version":3,"sources":["a.js"],"mappings":"g"}`))
	if err == nil || !IsTruncated(err) {
		t.Fatalf("truncated VLQ should be truncated, got: %v", err)
	}
}

func TestParseCorruptVLQ(t *testing.T) {
	// "!" is not a valid base64 VLQ character.
	_, err := Parse([]byte(`{"version":3,"sources":["a.js"],"mappings":"A!AA"}`))
	if err == nil || !IsCorrupt(err) {
		t.Fatalf("bad VLQ char should be corrupt, got: %v", err)
	}
	// Source index out of range.
	_, err = Parse([]byte(`{"version":3,"sources":["a.js"],"mappings":"AMAA"}`))
	if err == nil || !IsCorrupt(err) {
		t.Fatalf("bad source index should be corrupt, got: %v", err)
	}
}

func TestParsedMapDeterministic(t *testing.T) {
	a := mustParse(t, validMap)
	b := mustParse(t, validMap)
	for line := 1; line <= 3; line++ {
		for col := 0; col < 10; col++ {
			pa, oka, ra := a.Lookup(line, col)
			pb, okb, rb := b.Lookup(line, col)
			if pa != pb || oka != okb || ra != rb {
				t.Fatalf("nondeterministic lookup at %d:%d", line, col)
			}
		}
	}
}
