package metrics

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestValidateGMPMonitoring(t *testing.T) {
	for _, tc := range []struct {
		name          string
		gmp, rhobs    bool
		errorContains string
	}{
		{name: "When neither GMP nor RHOBS is enabled, it should succeed"},
		{name: "When only GMP is enabled, it should succeed", gmp: true},
		{name: "When only RHOBS is enabled, it should succeed", rhobs: true},
		{name: "When GMP and RHOBS are both enabled, it should reject the conflict", gmp: true, rhobs: true, errorContains: "cannot be enabled together with RHOBS monitoring"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			err := ValidateGMPMonitoring(tc.gmp, tc.rhobs)
			if tc.errorContains != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.errorContains)))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}
