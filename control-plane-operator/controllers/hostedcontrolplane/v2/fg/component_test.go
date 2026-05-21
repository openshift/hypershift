package fg

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestIsRequestServing(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	fgg := &FeatureGateGenerator{}

	g.Expect(fgg.IsRequestServing()).To(BeFalse())
}

func TestMultiZoneSpread(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	fgg := &FeatureGateGenerator{}

	g.Expect(fgg.MultiZoneSpread()).To(BeFalse())
}

func TestNeedsManagementKASAccess(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	fgg := &FeatureGateGenerator{}

	g.Expect(fgg.NeedsManagementKASAccess()).To(BeTrue())
}
