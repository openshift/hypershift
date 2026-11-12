//go:build e2ev2

package tests

import "testing"

func TestEventTTLMatches(t *testing.T) {
	tests := []struct {
		name      string
		got       string
		want      string
		wantMatch bool
		wantErr   bool
	}{
		{
			name:      "When durations are equivalent but spelled differently, it should match",
			got:       "3h",
			want:      "180m",
			wantMatch: true,
		},
		{
			name:      "When durations are identical, it should match",
			got:       "180m",
			want:      "180m",
			wantMatch: true,
		},
		{
			name:      "When durations differ, it should not match",
			got:       "30m",
			want:      "180m",
			wantMatch: false,
		},
		{
			name:    "When the rendered value is not a duration, it should return an error",
			got:     "not-a-duration",
			want:    "180m",
			wantErr: true,
		},
		{
			name:    "When the expected value is not a duration, it should return an error",
			got:     "180m",
			want:    "not-a-duration",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, err := eventTTLMatches(tt.got, tt.want)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("eventTTLMatches(%q, %q) expected an error, got nil", tt.got, tt.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("eventTTLMatches(%q, %q) returned unexpected error: %v", tt.got, tt.want, err)
			}
			if matched != tt.wantMatch {
				t.Errorf("eventTTLMatches(%q, %q) = %v, want %v", tt.got, tt.want, matched, tt.wantMatch)
			}
		})
	}
}
