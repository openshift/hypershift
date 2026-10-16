package nodepool

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/go-logr/logr"
)

func TestReconcileWorkload(t *testing.T) {
	for _, strategy := range []hyperv1.UpgradeType{hyperv1.UpgradeTypeReplace, hyperv1.UpgradeTypeInPlace} {
		for _, previousRequest := range []string{"none", "config", "version"} {
			t.Run("When a "+string(strategy)+" version update fails with a previous "+previousRequest+" request, it should preserve tracking and retry the desired payload", func(t *testing.T) {
				g := NewWithT(t)
				ctx := t.Context()
				f := newRolloutFixture(t, strategy)
				c := f.capi
				if previousRequest == "config" {
					c.mcoRawConfig = "user-config-B + haproxy-A"
					c.rolloutMcoRawConfig = "user-config-B"
					g.Expect(f.reconcile(t)).To(BeTrue())
				} else if previousRequest == "version" {
					c.releaseImage.ImageStream.Name = "4.18.6"
					c.nodePool.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.18.6-x86_64"
					g.Expect(f.reconcile(t)).To(BeTrue())
				}
				previousHash := f.bootstrapHash()
				c.releaseImage.ImageStream.Name = "4.18.7"
				c.nodePool.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.18.7-x86_64"
				wanted := c.Hash()
				originalNodePool := c.nodePool.DeepCopy()

				var workload client.Object
				var propagate func() error
				if strategy == hyperv1.UpgradeTypeReplace {
					workload = f.md
					propagate = func() error {
						c.propagateVersionAndTemplate(logr.Discard(), f.md, f.template)
						return nil
					}
				} else {
					workload = f.ms
					propagate = func() error {
						c.propagateVersionAndTemplateToMachineSet(logr.Discard(), f.ms, f.template)
						return nil
					}
				}
				g.Expect(c.Create(ctx, workload)).To(Succeed())
				g.Expect(c.Create(ctx, c.nodePool)).To(Succeed())
				failPatch := true
				c.Client = interceptor.NewClient(c.Client.(client.WithWatch), interceptor.Funcs{
					Patch: func(ctx context.Context, underlying client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						switch obj.(type) {
						case *capiv1.MachineDeployment, *capiv1.MachineSet:
							if failPatch {
								return apierrors.NewServiceUnavailable("workload write failed")
							}
						}
						return underlying.Patch(ctx, obj, patch, opts...)
					},
				})
				c.SetDeployedBootstrapHash(previousHash)
				g.Expect(c.Token.Reconcile(ctx)).To(Succeed())
				_, err := c.reconcileWorkload(ctx, workload, propagate)
				g.Expect(apierrors.IsServiceUnavailable(err)).To(BeTrue())
				g.Expect(c.nodePool.Annotations).To(Equal(originalNodePool.Annotations))
				g.Expect(c.nodePool.Status).To(Equal(originalNodePool.Status))

				// The main reconciler still patches the NodePool on error. Verify
				// that persisting it leaves the previous request intact, then retry
				// with the actual workload state from the failed API write.
				g.Expect(c.Patch(ctx, c.nodePool, client.MergeFrom(originalNodePool))).To(Succeed())
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(c.nodePool), c.nodePool)).To(Succeed())
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(workload), workload)).To(Succeed())
				g.Expect(f.bootstrapHash()).To(Equal(previousHash))
				failPatch = false
				c.effectiveHash = ""
				c.SetDeployedBootstrapHash(f.bootstrapHash())
				g.Expect(c.Token.Reconcile(ctx)).To(Succeed())
				_, err = c.reconcileWorkload(ctx, workload, propagate)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(workload), workload)).To(Succeed())
				g.Expect(f.bootstrapHash()).To(Equal(wanted))
				g.Expect(c.nodePool.Annotations[nodePoolAnnotationInProgressRolloutConfig]).To(Equal(c.RolloutHash()))
				f.expectActiveToken(t, wanted, "4.18.7")
				g.Expect(f.reconcile(t)).To(BeFalse())
				f.complete(t)
				g.Expect(c.nodePool.Status.Version).To(Equal("4.18.7"))
				g.Expect(c.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]).To(Equal(wanted))
				g.Expect(c.nodePool.Annotations).NotTo(HaveKey(nodePoolAnnotationInProgressRolloutConfig))
			})
		}
	}
}
