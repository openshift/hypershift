package conditions

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestSupportsGCPRuntimeCredentialValidation(t *testing.T) {
	for _, tc := range []struct {
		name, version    string
		supported, known bool
	}{
		{"When the version is 4.23, it should be unsupported", "4.23.0", false, true},
		{"When the version is 5.0, it should be unsupported", "5.0.99", false, true},
		{"When the version is a 5.1 CI prerelease, it should be supported", "5.1.0-0.ci-20260909", true, true},
		{"When the version has build metadata, it should be supported", "5.1.0-rc.1+build.42", true, true},
		{"When the minor version is later, it should be supported", "5.2.0", true, true},
		{"When the major version is later, it should be supported", "6.0.0", true, true},
		{"When the version is empty, it should be unknown", "", false, false},
		{"When the version is malformed, it should be unknown", "not-a-version", false, false},
		{"When the version is incomplete, it should be unknown", "5.1", false, false},
		{"When the version has an invalid suffix, it should be unknown", "5.1.0-", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			supported, known := SupportsGCPRuntimeCredentialValidation(tc.version)
			g.Expect(supported).To(Equal(tc.supported))
			g.Expect(known).To(Equal(tc.known))
		})
	}
}
