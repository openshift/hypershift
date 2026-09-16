package util

import (
	"math"
	"testing"
)

func TestClampToInt32(t *testing.T) {
	tests := []struct {
		name string
		v    float64
		want int32
	}{
		{name: "When value is zero, it should return zero", v: 0, want: 0},
		{name: "When value is a positive integer, it should return that integer", v: 42, want: 42},
		{name: "When value is a positive float, it should truncate to integer", v: 3.9, want: 3},
		{name: "When value is negative, it should clamp to zero", v: -1, want: 0},
		{name: "When value is a large negative, it should clamp to zero", v: -1e18, want: 0},
		{name: "When value equals max int32, it should return max int32", v: math.MaxInt32, want: math.MaxInt32},
		{name: "When value exceeds max int32, it should cap at max int32", v: math.MaxInt32 + 1, want: math.MaxInt32},
		{name: "When value is very large, it should cap at max int32", v: 1e18, want: math.MaxInt32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClampToInt32(tt.v); got != tt.want {
				t.Errorf("ClampToInt32(%v) = %v, want %v", tt.v, got, tt.want)
			}
		})
	}
}
