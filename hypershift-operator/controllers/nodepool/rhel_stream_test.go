package nodepool

import (
	"testing"

	"github.com/blang/semver"
	. "github.com/onsi/gomega"
)

func TestGetRHELStream(t *testing.T) {
	v419 := semver.MustParse("4.19.0")
	v500 := semver.MustParse("5.0.0")
	v510 := semver.MustParse("5.1.0")

	tests := []struct {
		name            string
		specStream      string
		releaseVersion  semver.Version
		usesRunc        bool
		expectedStream  string
		expectedFallback string
		expectError     bool
	}{
		{
			name:           "When explicit rhel-10 with release < 5.0 it should return an error",
			specStream:     RHELStream10,
			releaseVersion: v419,
			usesRunc:       false,
			expectError:    true,
		},
		{
			name:           "When explicit rhel-10 with runc it should return an error",
			specStream:     RHELStream10,
			releaseVersion: v500,
			usesRunc:       true,
			expectError:    true,
		},
		{
			name:           "When explicit rhel-9 it should return rhel-9",
			specStream:     RHELStream9,
			releaseVersion: v500,
			usesRunc:       false,
			expectedStream: RHELStream9,
		},
		{
			name:           "When explicit rhel-10 with release >= 5.0 and no runc it should return rhel-10",
			specStream:     RHELStream10,
			releaseVersion: v510,
			usesRunc:       false,
			expectedStream: RHELStream10,
		},
		{
			name:             "When unset with release >= 5.0 and runc it should fall back to rhel-9 with message",
			specStream:       "",
			releaseVersion:   v500,
			usesRunc:         true,
			expectedStream:   RHELStream9,
			expectedFallback: "OS stream defaulted to rhel-9: RHEL 10 is incompatible with default_runtime=runc",
		},
		{
			name:           "When unset with release >= 5.0 and no runc it should return rhel-10",
			specStream:     "",
			releaseVersion: v500,
			usesRunc:       false,
			expectedStream: RHELStream10,
		},
		{
			name:           "When unset with release < 5.0 it should return empty string",
			specStream:     "",
			releaseVersion: v419,
			usesRunc:       false,
			expectedStream: "",
		},
		{
			name:           "When unset with release < 5.0 and runc it should return empty string",
			specStream:     "",
			releaseVersion: v419,
			usesRunc:       true,
			expectedStream: "",
		},
		{
			name:           "When explicit rhel-9 with release < 5.0 it should return rhel-9",
			specStream:     RHELStream9,
			releaseVersion: v419,
			usesRunc:       false,
			expectedStream: RHELStream9,
		},
		{
			name:           "When explicit rhel-9 with runc it should return rhel-9",
			specStream:     RHELStream9,
			releaseVersion: v500,
			usesRunc:       true,
			expectedStream: RHELStream9,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			stream, fallbackMsg, err := getRHELStream(tc.specStream, tc.releaseVersion, tc.usesRunc)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(stream).To(Equal(tc.expectedStream))
			g.Expect(fallbackMsg).To(Equal(tc.expectedFallback))
		})
	}
}
