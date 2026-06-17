package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/blang/semver"
)

func TestGetRHELStream(t *testing.T) {
	tests := []struct {
		name           string
		explicitStream string
		releaseVersion semver.Version
		usesRunc       bool
		expectResult   string
		expectError    bool
	}{
		// --- Implicit stream (explicitStream = "") ---
		{
			name:           "When no explicit stream and release is 4.x it should return empty string",
			explicitStream: "",
			releaseVersion: semver.MustParse("4.18.0"),
			expectResult:   "",
		},
		{
			name:           "When no explicit stream and release is 4.x with runc it should return empty string",
			explicitStream: "",
			releaseVersion: semver.MustParse("4.19.0"),
			usesRunc:       true,
			expectResult:   "",
		},
		{
			name:           "When no explicit stream and release is 5.0 it should return rhel-10",
			explicitStream: "",
			releaseVersion: semver.MustParse("5.0.0"),
			expectResult:   "rhel-10",
		},
		{
			name:           "When no explicit stream and release is 5.0 with runc it should return rhel-9",
			explicitStream: "",
			releaseVersion: semver.MustParse("5.0.0"),
			usesRunc:       true,
			expectResult:   "rhel-9",
		},
		{
			name:           "When no explicit stream and release is 5.1 it should return rhel-10",
			explicitStream: "",
			releaseVersion: semver.MustParse("5.1.0"),
			expectResult:   "rhel-10",
		},
		{
			name:           "When no explicit stream and release is 5.1 with runc it should return rhel-9",
			explicitStream: "",
			releaseVersion: semver.MustParse("5.1.0"),
			usesRunc:       true,
			expectResult:   "rhel-9",
		},

		// --- Explicit rhel-9 ---
		{
			name:           "When explicit rhel-9 and release is 4.x it should return rhel-9",
			explicitStream: "rhel-9",
			releaseVersion: semver.MustParse("4.18.0"),
			expectResult:   "rhel-9",
		},
		{
			name:           "When explicit rhel-9 and release is 4.x with runc it should return rhel-9",
			explicitStream: "rhel-9",
			releaseVersion: semver.MustParse("4.18.0"),
			usesRunc:       true,
			expectResult:   "rhel-9",
		},
		{
			name:           "When explicit rhel-9 and release is 5.0 it should return rhel-9",
			explicitStream: "rhel-9",
			releaseVersion: semver.MustParse("5.0.0"),
			expectResult:   "rhel-9",
		},
		{
			name:           "When explicit rhel-9 and release is 5.0 with runc it should return rhel-9",
			explicitStream: "rhel-9",
			releaseVersion: semver.MustParse("5.0.0"),
			usesRunc:       true,
			expectResult:   "rhel-9",
		},
		{
			name:           "When explicit rhel-9 and release is 5.1 it should return rhel-9",
			explicitStream: "rhel-9",
			releaseVersion: semver.MustParse("5.1.0"),
			expectResult:   "rhel-9",
		},
		{
			name:           "When explicit rhel-9 and release is 5.1 with runc it should return rhel-9",
			explicitStream: "rhel-9",
			releaseVersion: semver.MustParse("5.1.0"),
			usesRunc:       true,
			expectResult:   "rhel-9",
		},

		// --- Explicit rhel-10 ---
		{
			name:           "When explicit rhel-10 and release is 4.x it should return error",
			explicitStream: "rhel-10",
			releaseVersion: semver.MustParse("4.19.0"),
			expectError:    true,
		},
		{
			name:           "When explicit rhel-10 and release is 4.x with runc it should return error",
			explicitStream: "rhel-10",
			releaseVersion: semver.MustParse("4.19.0"),
			usesRunc:       true,
			expectError:    true,
		},
		{
			name:           "When explicit rhel-10 and release is 5.0 it should return rhel-10",
			explicitStream: "rhel-10",
			releaseVersion: semver.MustParse("5.0.0"),
			expectResult:   "rhel-10",
		},
		{
			name:           "When explicit rhel-10 and release is 5.0 with runc it should return error",
			explicitStream: "rhel-10",
			releaseVersion: semver.MustParse("5.0.0"),
			usesRunc:       true,
			expectError:    true,
		},
		{
			name:           "When explicit rhel-10 and release is 5.1 it should return rhel-10",
			explicitStream: "rhel-10",
			releaseVersion: semver.MustParse("5.1.0"),
			expectResult:   "rhel-10",
		},
		{
			name:           "When explicit rhel-10 and release is 5.1 with runc it should return error",
			explicitStream: "rhel-10",
			releaseVersion: semver.MustParse("5.1.0"),
			usesRunc:       true,
			expectError:    true,
		},

		// --- Unknown stream ---
		{
			name:           "When explicit unknown stream and release is 4.x it should return error",
			explicitStream: "rhel-8",
			releaseVersion: semver.MustParse("4.18.0"),
			expectError:    true,
		},
		{
			name:           "When explicit unknown stream and release is 5.0 it should return error",
			explicitStream: "rhel-8",
			releaseVersion: semver.MustParse("5.0.0"),
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			result, err := GetRHELStream(tt.explicitStream, tt.releaseVersion, tt.usesRunc)
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tt.expectResult))
		})
	}
}
