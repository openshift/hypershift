package dnsoperator

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestComponentOptions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		validate func(*testing.T, *dnsOperator)
	}{
		{
			name: "When checking IsRequestServing, it should return false",
			validate: func(t *testing.T, d *dnsOperator) {
				g := NewWithT(t)
				g.Expect(d.IsRequestServing()).To(BeFalse())
			},
		},
		{
			name: "When checking MultiZoneSpread, it should return true",
			validate: func(t *testing.T, d *dnsOperator) {
				g := NewWithT(t)
				g.Expect(d.MultiZoneSpread()).To(BeTrue())
			},
		},
		{
			name: "When checking NeedsManagementKASAccess, it should return false",
			validate: func(t *testing.T, d *dnsOperator) {
				g := NewWithT(t)
				g.Expect(d.NeedsManagementKASAccess()).To(BeFalse())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := &dnsOperator{}
			tc.validate(t, d)
		})
	}
}
