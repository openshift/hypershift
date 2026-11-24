package assets

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/gmpmonitoring"
)

func TestLoadGMPPodMonitoring(t *testing.T) {
	for _, component := range []string{"operator", "external-dns"} {
		t.Run("When loading the "+component+" monitor, it should be a PodMonitoring in the requested namespace", func(t *testing.T) {
			g := NewWithT(t)

			obj, err := LoadGMPPodMonitoring(component, "custom-ns")

			g.Expect(err).ToNot(HaveOccurred())
			podMonitoring, ok := obj.(*gmpmonitoring.PodMonitoring)
			g.Expect(ok).To(BeTrue(), "expected a PodMonitoring, got %T", obj)
			g.Expect(podMonitoring.Namespace).To(Equal("custom-ns"))
			g.Expect(podMonitoring.Spec.Endpoints).ToNot(BeEmpty())
		})
	}

	t.Run("When the placeholder namespace is replaced, it should not remain anywhere in the staged asset", func(t *testing.T) {
		g := NewWithT(t)

		for _, component := range []string{"operator", "external-dns"} {
			raw, err := gmpMonitoringAssets.ReadFile("gmpmonitoring/" + component + "/" + gmpPodMonitoringManifest)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(strings.Count(string(raw), "MANAGEMENT_NAMESPACE")).To(Equal(1),
				"%s must reference the placeholder only in metadata.namespace, which the loader replaces", component)
		}
	})

	t.Run("When the component has no staged monitor, it should fail", func(t *testing.T) {
		g := NewWithT(t)

		_, err := LoadGMPPodMonitoring("missing", "ns")

		g.Expect(err).To(HaveOccurred())
	})
}
