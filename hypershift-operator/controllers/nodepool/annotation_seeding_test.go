package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSeedRolloutAnnotation(t *testing.T) {
	testCases := []struct {
		name        string
		annotations map[string]string
		expected    map[string]string
	}{
		{
			name:     "When annotations are absent, it should seed the rollout baseline without recording completion",
			expected: map[string]string{nodePoolAnnotationCurrentRolloutConfig: "rollout-hash"},
		},
		{
			name:        "When a completed hash exists, it should preserve it while seeding the rollout baseline",
			annotations: map[string]string{nodePoolAnnotationCurrentConfigVersion: "completed-hash"},
			expected: map[string]string{
				nodePoolAnnotationCurrentConfigVersion: "completed-hash",
				nodePoolAnnotationCurrentRolloutConfig: "rollout-hash",
			},
		},
		{
			name: "When the rollout baseline exists, it should preserve the baseline and active request",
			annotations: map[string]string{
				nodePoolAnnotationCurrentRolloutConfig:    "existing-baseline",
				nodePoolAnnotationCurrentConfigVersion:    "completed-hash",
				nodePoolAnnotationInProgressRolloutConfig: "active-request",
			},
			expected: map[string]string{
				nodePoolAnnotationCurrentRolloutConfig:    "existing-baseline",
				nodePoolAnnotationCurrentConfigVersion:    "completed-hash",
				nodePoolAnnotationInProgressRolloutConfig: "active-request",
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			nodePool := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Annotations: tc.annotations}}
			seedRolloutAnnotation(nodePool, "rollout-hash")
			NewWithT(t).Expect(nodePool.Annotations).To(Equal(tc.expected))
		})
	}

	for _, strategy := range []hyperv1.UpgradeType{hyperv1.UpgradeTypeReplace, hyperv1.UpgradeTypeInPlace} {
		for _, kind := range []string{"config", "version"} {
			for _, driftTiming := range []string{"before", "after"} {
				t.Run("When management drift arrives "+driftTiming+" migration during a "+string(strategy)+" "+kind+" rollout, it should preserve the active token until completion", func(t *testing.T) {
					g := NewWithT(t)
					f := newRolloutFixture(t, strategy)
					c := f.capi
					completedHash := c.Hash()
					if kind == "config" {
						c.mcoRawConfig = "user-config-B + haproxy-A"
						c.rolloutMcoRawConfig = "user-config-B"
					} else {
						c.releaseImage.ImageStream.Name = "4.18.6"
						c.nodePool.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.18.6-x86_64"
					}
					g.Expect(f.reconcile(t)).To(BeTrue())
					activeHash := f.bootstrapHash()
					// A legacy rollout has neither of the new tracking annotations.
					delete(c.nodePool.Annotations, nodePoolAnnotationCurrentRolloutConfig)
					delete(c.nodePool.Annotations, nodePoolAnnotationInProgressRolloutConfig)
					drift := func() { c.mcoRawConfig = c.rolloutMcoRawConfig + " + haproxy-B" }
					if driftTiming == "before" {
						drift()
					}
					g.Expect(f.reconcile(t)).To(BeFalse(), "migration should retain the active workload")
					g.Expect(c.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]).To(Equal(completedHash))
					if driftTiming == "after" {
						drift()
					}
					for i := 0; i < 3; i++ {
						g.Expect(f.reconcile(t)).To(BeFalse(), "management drift should not restart the rollout")
						g.Expect(c.EffectiveHash()).To(Equal(activeHash))
						g.Expect(f.bootstrapHash()).To(Equal(activeHash))
						g.Expect(c.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]).To(Equal(completedHash))
						f.expectActiveToken(t, activeHash, c.Version())
					}
					f.complete(t)
					g.Expect(c.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]).To(Equal(activeHash))
					g.Expect(c.nodePool.Status.Version).To(Equal(c.Version()))
					g.Expect(f.reconcile(t)).To(BeFalse())
					f.expectActiveToken(t, activeHash, c.Version())

					// Completion must also allow the next user request to propagate.
					c.mcoRawConfig = "user-config-C + haproxy-B"
					c.rolloutMcoRawConfig = "user-config-C"
					g.Expect(f.reconcile(t)).To(BeTrue())
					g.Expect(f.bootstrapHash()).To(Equal(c.Hash()))
					f.expectActiveToken(t, c.Hash(), c.Version())
				})
			}
		}
	}
}
