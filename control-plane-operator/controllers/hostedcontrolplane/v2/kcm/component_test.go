package kcm

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestKubeControllerManagerOptions(t *testing.T) {
	t.Parallel()

	t.Run("When IsRequestServing is called, it should return false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		kcm := &KubeControllerManager{}
		g.Expect(kcm.IsRequestServing()).To(BeFalse())
	})

	t.Run("When MultiZoneSpread is called, it should return true", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		kcm := &KubeControllerManager{}
		g.Expect(kcm.MultiZoneSpread()).To(BeTrue())
	})

	t.Run("When NeedsManagementKASAccess is called, it should return false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		kcm := &KubeControllerManager{}
		g.Expect(kcm.NeedsManagementKASAccess()).To(BeFalse())
	})
}
