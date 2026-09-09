package util

import "math"

// ClampToInt32 clamps a float64 value into a valid non-negative int32 range.
// Negative values are clamped to 0 and values exceeding math.MaxInt32 are capped.
func ClampToInt32(v float64) int32 {
	if v < 0 {
		return 0
	}
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(v)
}
