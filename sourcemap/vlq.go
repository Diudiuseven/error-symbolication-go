package sourcemap

import (
	"errors"
	"fmt"
)

var b64rev [256]int8

func init() {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	for i := range b64rev {
		b64rev[i] = -1
	}
	for i := 0; i < len(chars); i++ {
		b64rev[chars[i]] = int8(i)
	}
}

// errVLQTruncated is returned when the input ends mid-sequence.
var errVLQTruncated = errors.New("vlq: truncated sequence")

// decodeVLQ decodes a base64 VLQ encoded string into signed integers.
// A truncated final sequence yields errVLQTruncated; an invalid character
// yields a descriptive corruption error.
func decodeVLQ(s string) ([]int, error) {
	out := make([]int, 0, len(s)/2)
	var result, shift int
	for i := 0; i < len(s); i++ {
		d := b64rev[s[i]]
		if d < 0 {
			return nil, fmt.Errorf("vlq: invalid character %q at offset %d", s[i], i)
		}
		digit := int(d)
		cont := digit & 32
		result += (digit & 31) << shift
		if cont != 0 {
			shift += 5
			if i == len(s)-1 {
				return nil, errVLQTruncated
			}
			continue
		}
		neg := result & 1
		result >>= 1
		if neg != 0 {
			result = -result
		}
		out = append(out, result)
		result, shift = 0, 0
	}
	return out, nil
}
