package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"

	imagev1 "github.com/openshift/api/image/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSeedRolloutAnnotation(t *testing.T) {
	config := &rolloutConfig{
		releaseImage: &releaseinfo.ReleaseImage{ImageStream: &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.18.5"}}},
	}
	cg := &ConfigGenerator{rolloutConfig: config}
	rolloutHash := cg.RolloutHashWithoutVersion()
	rolloutHashWithVersion := cg.RolloutHash()
	testCases := []struct {
		name         string
		annotations  map[string]string
		deployedHash string
		expected     map[string]string
	}{
		{
			name:     "When annotations are absent, it should seed the rollout baseline without recording completion",
			expected: map[string]string{nodePoolAnnotationCurrentRolloutConfig: rolloutHash},
		},
		{
			name:        "When a completed hash exists, it should preserve it while seeding the rollout baseline",
			annotations: map[string]string{nodePoolAnnotationCurrentConfigVersion: "completed-hash"},
			expected: map[string]string{
				nodePoolAnnotationCurrentConfigVersion: "completed-hash",
				nodePoolAnnotationCurrentRolloutConfig: rolloutHash,
			},
		},
		{
			name:         "When the deployed payload has completed, it should seed without an active request",
			annotations:  map[string]string{nodePoolAnnotationCurrentConfigVersion: "completed-hash"},
			deployedHash: "completed-hash",
			expected: map[string]string{
				nodePoolAnnotationCurrentConfigVersion: "completed-hash",
				nodePoolAnnotationCurrentRolloutConfig: rolloutHash,
			},
		},
		{
			name:         "When a legacy rollout is active, it should record the adopted request without recording completion",
			annotations:  map[string]string{nodePoolAnnotationCurrentConfigVersion: "completed-hash"},
			deployedHash: "active-hash",
			expected: map[string]string{
				nodePoolAnnotationCurrentConfigVersion:    "completed-hash",
				nodePoolAnnotationCurrentRolloutConfig:    rolloutHash,
				nodePoolAnnotationInProgressRolloutConfig: rolloutHashWithVersion,
			},
		},
		{
			name:         "When an initial legacy rollout has no completed payload, it should record the adopted request",
			deployedHash: "active-hash",
			expected: map[string]string{
				nodePoolAnnotationCurrentRolloutConfig:    rolloutHash,
				nodePoolAnnotationInProgressRolloutConfig: rolloutHashWithVersion,
			},
		},
		{
			name:         "When an active request already exists without a rollout baseline, it should preserve that request",
			annotations:  map[string]string{nodePoolAnnotationInProgressRolloutConfig: "active-request"},
			deployedHash: "active-hash",
			expected: map[string]string{
				nodePoolAnnotationCurrentRolloutConfig:    rolloutHash,
				nodePoolAnnotationInProgressRolloutConfig: "active-request",
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
			token := &Token{
				ConfigGenerator:       &ConfigGenerator{nodePool: nodePool, rolloutConfig: config},
				deployedBootstrapHash: tc.deployedHash,
			}
			seedRolloutAnnotation(token)
			NewWithT(t).Expect(nodePool.Annotations).To(Equal(tc.expected))
		})
	}

	for _, strategy := range []hyperv1.UpgradeType{hyperv1.UpgradeTypeReplace, hyperv1.UpgradeTypeInPlace} {
		for _, kind := range []string{"config", "version"} {
			for _, requestedVersion := range []string{"4.18.7", "4.18.5"} {
				t.Run("When migration finds an active "+string(strategy)+" "+kind+" rollout and release "+requestedVersion+" is already requested, it should deliver that release", func(t *testing.T) {
					g := NewWithT(t)
					f := newRolloutFixture(t, strategy)
					c := f.capi
					if kind == "config" {
						c.mcoRawConfig = "user-config-B + haproxy-A"
						c.rolloutMcoRawConfig = "user-config-B"
					} else {
						c.releaseImage.ImageStream.Name = "4.18.6"
						c.nodePool.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.18.6-x86_64"
					}
					g.Expect(f.reconcile(t)).To(BeTrue())
					activeHash := f.bootstrapHash()
					activeVersion := c.Version()
					delete(c.nodePool.Annotations, nodePoolAnnotationCurrentRolloutConfig)
					delete(c.nodePool.Annotations, nodePoolAnnotationInProgressRolloutConfig)
					delete(f.md.Annotations, nodePoolAnnotationTargetRolloutConfig)
					delete(f.ms.Annotations, nodePoolAnnotationTargetRolloutConfig)
					c.releaseImage.ImageStream.Name = requestedVersion
					c.nodePool.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:" + requestedVersion + "-x86_64"
					wanted := c.Hash()
					g.Expect(f.reconcile(t)).To(Equal(activeVersion != requestedVersion))
					g.Expect(f.bootstrapHash()).To(Equal(wanted))
					if activeVersion == requestedVersion {
						g.Expect(f.bootstrapHash()).To(Equal(activeHash), "migration must preserve a legacy rollout already using the requested release")
					}
					f.expectActiveToken(t, wanted, requestedVersion)
					g.Expect(f.reconcile(t)).To(BeFalse())
					f.complete(t)
					g.Expect(c.nodePool.Status.Version).To(Equal(requestedVersion))
					g.Expect(c.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]).To(Equal(wanted))
					g.Expect(c.nodePool.Annotations).NotTo(HaveKey(nodePoolAnnotationInProgressRolloutConfig))
					g.Expect(f.reconcile(t)).To(BeFalse())
				})
			}
			for _, nextChange := range []string{"config", "version"} {
				t.Run("When a new "+nextChange+" request follows migration of an active "+string(strategy)+" "+kind+" rollout, it should supersede the adopted target", func(t *testing.T) {
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
					delete(c.nodePool.Annotations, nodePoolAnnotationCurrentRolloutConfig)
					delete(c.nodePool.Annotations, nodePoolAnnotationInProgressRolloutConfig)
					delete(f.md.Annotations, nodePoolAnnotationTargetRolloutConfig)
					delete(f.ms.Annotations, nodePoolAnnotationTargetRolloutConfig)
					g.Expect(f.reconcile(t)).To(BeFalse(), "migration must preserve the deployed target")
					g.Expect(f.bootstrapHash()).To(Equal(activeHash))
					g.Expect(c.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]).To(Equal(completedHash))
					g.Expect(c.nodePool.Annotations[nodePoolAnnotationInProgressRolloutConfig]).To(Equal(c.RolloutHash()))

					// This request arrives after the migration baseline was persisted,
					// while the adopted rollout is still incomplete.
					if nextChange == "config" {
						c.mcoRawConfig = "user-config-C + haproxy-A"
						c.rolloutMcoRawConfig = "user-config-C"
					} else {
						c.releaseImage.ImageStream.Name = "4.18.7"
						c.nodePool.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.18.7-x86_64"
					}
					wanted := c.Hash()
					g.Expect(f.reconcile(t)).To(BeTrue(), "the new request must update the bootstrap target")
					g.Expect(f.bootstrapHash()).To(Equal(wanted))
					f.expectActiveToken(t, wanted, c.Version())
					g.Expect(f.reconcile(t)).To(BeFalse())
					f.complete(t)
					g.Expect(c.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]).To(Equal(wanted))
					g.Expect(c.nodePool.Annotations[nodePoolAnnotationCurrentRolloutConfig]).To(Equal(c.RolloutHashWithoutVersion()))
					g.Expect(c.nodePool.Annotations).NotTo(HaveKey(nodePoolAnnotationInProgressRolloutConfig))
					g.Expect(f.reconcile(t)).To(BeFalse())
					f.expectActiveToken(t, wanted, c.Version())
				})
			}
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
					delete(f.md.Annotations, nodePoolAnnotationTargetRolloutConfig)
					delete(f.ms.Annotations, nodePoolAnnotationTargetRolloutConfig)
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
