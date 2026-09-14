package scraper

import (
	"testing"
)

func TestParseGocycloOutput(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    float64
		wantErr bool
	}{
		{
			name:    "When gocyclo output is valid it should parse average complexity",
			output:  "Average: 3.45 (over 100 functions)",
			want:    3.45,
			wantErr: false,
		},
		{
			name:    "When gocyclo output has integer average it should parse correctly",
			output:  "Average: 5 (over 42 functions)",
			want:    5.0,
			wantErr: false,
		},
		{
			name:    "When gocyclo output has single function it should parse correctly",
			output:  "Average: 2.5 (over 1 function)",
			want:    2.5,
			wantErr: false,
		},
		{
			name:    "When gocyclo output is empty it should return error",
			output:  "",
			want:    0,
			wantErr: true,
		},
		{
			name:    "When gocyclo output is malformed it should return error",
			output:  "No average found here",
			want:    0,
			wantErr: true,
		},
		{
			name:    "When gocyclo output has invalid number it should return error",
			output:  "Average: invalid (over 10 functions)",
			want:    0,
			wantErr: true,
		},
		{
			name:    "When gocyclo output has zero functions it should return zero",
			output:  "Average: 0 (over 0 functions)",
			want:    0,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseGocycloOutput(tt.output)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseGocycloOutput() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseGocycloOutput() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseGocognitOutput(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    float64
		wantErr bool
	}{
		{
			name:    "When gocognit output is valid it should parse average complexity",
			output:  "Average: 7.82 (over 150 functions)",
			want:    7.82,
			wantErr: false,
		},
		{
			name:    "When gocognit output has integer average it should parse correctly",
			output:  "Average: 10 (over 25 functions)",
			want:    10.0,
			wantErr: false,
		},
		{
			name:    "When gocognit output is empty it should return error",
			output:  "",
			want:    0,
			wantErr: true,
		},
		{
			name:    "When gocognit output is malformed it should return error",
			output:  "Nothing here",
			want:    0,
			wantErr: true,
		},
		{
			name:    "When gocognit output has zero functions it should return zero",
			output:  "Average: 0 (over 0 functions)",
			want:    0,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseGocognitOutput(tt.output)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseGocognitOutput() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseGocognitOutput() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComputeComplexityDelta(t *testing.T) {
	tests := []struct {
		name string
		base float64
		head float64
		want float64
	}{
		{
			name: "When head complexity is higher than base it should report positive delta",
			base: 3.5,
			head: 5.2,
			want: 1.7,
		},
		{
			name: "When head complexity is lower than base it should report negative delta",
			base: 8.0,
			head: 6.5,
			want: -1.5,
		},
		{
			name: "When complexities are equal it should report zero delta",
			base: 4.2,
			head: 4.2,
			want: 0.0,
		},
		{
			name: "When base is zero it should report head as delta",
			base: 0,
			head: 3.5,
			want: 3.5,
		},
		{
			name: "When both are zero it should report zero delta",
			base: 0,
			head: 0,
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeComplexityDelta(tt.base, tt.head)
			// Use approximate comparison for floating point
			if diff := got - tt.want; diff < -0.01 || diff > 0.01 {
				t.Errorf("computeComplexityDelta(%v, %v) = %v, want %v", tt.base, tt.head, got, tt.want)
			}
		})
	}
}
