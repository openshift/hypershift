package nodepool

import (
	"testing"

	"github.com/blang/semver"
)

func TestGetRHELStream(t *testing.T) {
	v4_17 := semver.MustParse("4.17.0")
	v5_0 := semver.MustParse("5.0.0")
	v5_1 := semver.MustParse("5.1.0")

	tests := []struct {
		name           string
		specStream     string
		releaseVersion semver.Version
		usesRunc       bool
		want           string
		wantErr        bool
	}{
		{
			name:           "When no stream is specified and release is pre-5.0 it should return empty string",
			specStream:     "",
			releaseVersion: v4_17,
			usesRunc:       false,
			want:           "",
		},
		{
			name:           "When no stream is specified and release is 5.0 it should default to rhel-10",
			specStream:     "",
			releaseVersion: v5_0,
			usesRunc:       false,
			want:           RHELStreamRHEL10,
		},
		{
			name:           "When no stream is specified and release is 5.1 it should default to rhel-10",
			specStream:     "",
			releaseVersion: v5_1,
			usesRunc:       false,
			want:           RHELStreamRHEL10,
		},
		{
			name:           "When no stream is specified and release is 5.0 with runc it should fall back to rhel-9",
			specStream:     "",
			releaseVersion: v5_0,
			usesRunc:       true,
			want:           RHELStreamRHEL9,
		},
		{
			name:           "When rhel-9 is explicitly specified and release is pre-5.0 it should return rhel-9",
			specStream:     RHELStreamRHEL9,
			releaseVersion: v4_17,
			usesRunc:       false,
			want:           RHELStreamRHEL9,
		},
		{
			name:           "When rhel-9 is explicitly specified and release is 5.0 it should return rhel-9",
			specStream:     RHELStreamRHEL9,
			releaseVersion: v5_0,
			usesRunc:       false,
			want:           RHELStreamRHEL9,
		},
		{
			name:           "When rhel-9 is explicitly specified with runc it should return rhel-9",
			specStream:     RHELStreamRHEL9,
			releaseVersion: v5_0,
			usesRunc:       true,
			want:           RHELStreamRHEL9,
		},
		{
			name:           "When rhel-10 is explicitly specified and release is 5.0 it should return rhel-10",
			specStream:     RHELStreamRHEL10,
			releaseVersion: v5_0,
			usesRunc:       false,
			want:           RHELStreamRHEL10,
		},
		{
			name:           "When rhel-10 is specified and release is pre-5.0 it should return an error",
			specStream:     RHELStreamRHEL10,
			releaseVersion: v4_17,
			usesRunc:       false,
			wantErr:        true,
		},
		{
			name:           "When rhel-10 is specified with runc it should return an error",
			specStream:     RHELStreamRHEL10,
			releaseVersion: v5_0,
			usesRunc:       true,
			wantErr:        true,
		},
		{
			name:           "When an unsupported stream is specified it should return an error",
			specStream:     "rhel-8",
			releaseVersion: v5_0,
			usesRunc:       false,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getRHELStream(tt.specStream, tt.releaseVersion, tt.usesRunc)
			if (err != nil) != tt.wantErr {
				t.Errorf("getRHELStream() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("getRHELStream() = %v, want %v", got, tt.want)
			}
		})
	}
}
