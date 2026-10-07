package sourcemap

import (
	"errors"
	"fmt"
)

// ErrInvalidVLQ is returned when a VLQ string contains invalid characters
// or ends in the middle of a multi-byte value (truncated).
var ErrInvalidVLQ = errors.New("sourcemap: invalid base64 VLQ encoding")

const b64chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var b64rev [128]int8

func init() {
	for i := range b64rev {
		b64rev[i] = -1
	}
	for i := 0; i < len(b64chars); i++ {
		b64rev[b64chars[i]] = int8(i)
	}
}

// decodeVLQ decodes a base64 VLQ encoded string into a slice of integers.
// It returns ErrInvalidVLQ if the input contains invalid characters or is
// truncated (continuation bit set on the final character).
func decodeVLQ(s string) ([]int, error) {
	out := make([]int, 0, len(s)/2)
	var result, shift int
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 128 || b64rev[c] < 0 {
			return nil, fmt.Errorf("%w: bad character %q at offset %d", ErrInvalidVLQ, c, i)
		}
		digit := int(b64rev[c])
		continuation := digit & 32
		digit &= 31
		result += digit << shift
		if continuation != 0 {
			shift += 5
			continue
		}
		// Sign bit is the least significant bit; value is the rest.
		neg := result & 1
		result >>= 1
		if neg != 0 {
			result = -result
		}
		out = append(out, result)
		result, shift = 0, 0
	}
	if shift != 0 {
		return nil, fmt.Errorf("%w: truncated value at end of input", ErrInvalidVLQ)
	}
	return out, nil
}
