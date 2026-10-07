package sourcemap

import (
	"errors"
	"reflect"
	"testing"
)

func TestDecodeVLQ(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"AAAA", []int{0, 0, 0, 0}},
		{"A", []int{0}},
		{"C", []int{1}},
		{"D", []int{-1}},
		{"2rBAgB", []int{699, 0, 16}},
	}
	for _, c := range cases {
		got, err := decodeVLQ(c.in)
		if err != nil {
			t.Fatalf("decodeVLQ(%q): %v", c.in, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("decodeVLQ(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestDecodeVLQTruncated(t *testing.T) {
	// "g" has the continuation bit set and ends the input.
	if _, err := decodeVLQ("Ag"); !errors.Is(err, errVLQTruncated) {
		t.Fatalf("want truncated error, got %v", err)
	}
}

func TestDecodeVLQInvalidChar(t *testing.T) {
	if _, err := decodeVLQ("A!A"); err == nil {
		t.Fatal("want error for invalid character")
	}
}
