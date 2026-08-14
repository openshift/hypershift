package scheduler

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestKubeSchedulerOptions(t *testing.T) {
	t.Parallel()

	t.Run("When IsRequestServing is called it should return false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		ks := &kubeScheduler{}
		g.Expect(ks.IsRequestServing()).To(BeFalse())
	})

	t.Run("When MultiZoneSpread is called it should return true", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		ks := &kubeScheduler{}
		g.Expect(ks.MultiZoneSpread()).To(BeTrue())
	})

	t.Run("When NeedsManagementKASAccess is called it should return false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		ks := &kubeScheduler{}
		g.Expect(ks.NeedsManagementKASAccess()).To(BeFalse())
	})
}
