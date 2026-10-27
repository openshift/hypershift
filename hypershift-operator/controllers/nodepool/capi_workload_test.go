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
				switch previousRequest {
				case "config":
					c.mcoRawConfig = "user-config-B + haproxy-A"
					c.rolloutMcoRawConfig = "user-config-B"
					g.Expect(f.reconcile(t)).To(BeTrue())
				case "version":
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
				f.observeDeployedState()
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
				f.observeDeployedState()
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
	for _, strategy := range []hyperv1.UpgradeType{hyperv1.UpgradeTypeReplace, hyperv1.UpgradeTypeInPlace} {
		t.Run("When a stable "+string(strategy)+" workload lacks a target annotation, it should record metadata without changing the machine template", func(t *testing.T) {
			g := NewWithT(t)
			f := newRolloutFixture(t, strategy)
			c := f.capi
			activeHash := f.bootstrapHash()
			c.mcoRawConfig = "user-config-A + haproxy-B"
			f.observeDeployedState()
			g.Expect(c.Token.Reconcile(t.Context())).To(Succeed())
			seedRolloutAnnotation(c.Token)
			if strategy == hyperv1.UpgradeTypeReplace {
				original := f.md.Spec.DeepCopy()
				g.Expect(c.Create(t.Context(), f.md)).To(Succeed())
				_, err := c.reconcileWorkload(t.Context(), f.md, func() error {
					g.Expect(c.propagateVersionAndTemplate(logr.Discard(), f.md, f.template)).To(BeFalse())
					return nil
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(f.md), f.md)).To(Succeed())
				g.Expect(f.md.Spec).To(Equal(*original))
				g.Expect(f.md.Annotations[nodePoolAnnotationTargetRolloutConfig]).To(Equal(c.RolloutHash()))
			} else {
				original := f.ms.Spec.DeepCopy()
				g.Expect(c.Create(t.Context(), f.ms)).To(Succeed())
				_, err := c.reconcileWorkload(t.Context(), f.ms, func() error {
					g.Expect(c.propagateVersionAndTemplateToMachineSet(logr.Discard(), f.ms, f.template)).To(BeFalse())
					return nil
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(f.ms), f.ms)).To(Succeed())
				g.Expect(f.ms.Spec).To(Equal(*original))
				g.Expect(f.ms.Annotations[nodePoolAnnotationTargetRolloutConfig]).To(Equal(c.RolloutHash()))
			}
			g.Expect(f.bootstrapHash()).To(Equal(activeHash))
			g.Expect(c.nodePool.Annotations).NotTo(HaveKey(nodePoolAnnotationInProgressRolloutConfig))
			f.expectActiveToken(t, activeHash, c.Version())
		})
	}
	testReconcileWorkloadAfterNodePoolWriteFailure(t)
}

func testReconcileWorkloadAfterNodePoolWriteFailure(t *testing.T) {
	t.Helper()
	for _, strategy := range []hyperv1.UpgradeType{hyperv1.UpgradeTypeReplace, hyperv1.UpgradeTypeInPlace} {
		for _, previousRequest := range []string{"none", "version", "unseeded"} {
			for _, nextRequest := range []string{"unchanged", "config", "version", "revert"} {
				t.Run("When a "+string(strategy)+" workload write succeeds with a previous "+previousRequest+" request but the NodePool patch fails, it should recover the target for an "+nextRequest+" request", func(t *testing.T) {
					g := NewWithT(t)
					ctx := t.Context()
					f := newRolloutFixture(t, strategy)
					c := f.capi
					if previousRequest == "unseeded" {
						delete(c.nodePool.Annotations, nodePoolAnnotationCurrentRolloutConfig)
					}
					if previousRequest == "version" {
						c.releaseImage.ImageStream.Name = "4.18.6"
						c.nodePool.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.18.6-x86_64"
						g.Expect(f.reconcile(t)).To(BeTrue())
					}
					c.releaseImage.ImageStream.Name = "4.18.7"
					c.nodePool.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.18.7-x86_64"
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
					g.Expect(c.Create(ctx, c.nodePool)).To(Succeed())
					g.Expect(c.Create(ctx, workload)).To(Succeed())
					original := c.nodePool.DeepCopy()
					c.Client = interceptor.NewClient(c.Client.(client.WithWatch), interceptor.Funcs{
						Patch: func(ctx context.Context, underlying client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
							if _, ok := obj.(*hyperv1.NodePool); ok {
								return apierrors.NewServiceUnavailable("NodePool patch failed after the workload write")
							}
							return underlying.Patch(ctx, obj, patch, opts...)
						},
					})
					f.observeDeployedState()
					g.Expect(c.Token.Reconcile(ctx)).To(Succeed())
					seedRolloutAnnotation(c.Token)
					_, err := c.reconcileWorkload(ctx, workload, propagate)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(c.Get(ctx, client.ObjectKeyFromObject(workload), workload)).To(Succeed())
					g.Expect(f.bootstrapHash()).To(Equal(c.Hash()))
					g.Expect(workload.GetAnnotations()[nodePoolAnnotationTargetRolloutConfig]).To(Equal(c.RolloutHash()))
					g.Expect(apierrors.IsServiceUnavailable(c.Patch(ctx, c.nodePool, client.MergeFrom(original)))).To(BeTrue())
					g.Expect(c.Get(ctx, client.ObjectKeyFromObject(c.nodePool), c.nodePool)).To(Succeed())
					g.Expect(c.nodePool.Annotations).To(Equal(original.Annotations))

					switch nextRequest {
					case "unchanged":
						// Management drift must retain the successfully written target
						// while recovering the missing or stale NodePool marker.
						activeHash := f.bootstrapHash()
						c.mcoRawConfig = "user-config-A + haproxy-B"
						g.Expect(f.reconcile(t)).To(BeFalse())
						g.Expect(f.bootstrapHash()).To(Equal(activeHash))
						g.Expect(c.nodePool.Annotations[nodePoolAnnotationInProgressRolloutConfig]).To(Equal(c.RolloutHash()))
						f.expectActiveToken(t, activeHash, c.Version())
						// A later config request must still supersede the recovered target.
						c.mcoRawConfig = "user-config-C + haproxy-B"
						c.rolloutMcoRawConfig = "user-config-C"
					case "config":
						c.mcoRawConfig = "user-config-C + haproxy-A"
						c.rolloutMcoRawConfig = "user-config-C"
					default:
						c.releaseImage.ImageStream.Name = "4.18.8"
						if nextRequest == "revert" {
							c.releaseImage.ImageStream.Name = "4.18.5"
						}
						c.nodePool.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:" + c.Version() + "-x86_64"
					}
					wanted := c.Hash()
					g.Expect(f.reconcile(t)).To(BeTrue(), "a new request must propagate even before an unchanged retry")
					g.Expect(f.bootstrapHash()).To(Equal(wanted))
					f.expectActiveToken(t, wanted, c.Version())
					g.Expect(f.reconcile(t)).To(BeFalse())
					f.complete(t)
					g.Expect(c.nodePool.Status.Version).To(Equal(c.Version()))
					g.Expect(c.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]).To(Equal(wanted))
					g.Expect(c.nodePool.Annotations).NotTo(HaveKey(nodePoolAnnotationInProgressRolloutConfig))
					g.Expect(f.reconcile(t)).To(BeFalse())
					g.Expect(c.nodePool.Annotations).NotTo(HaveKey(nodePoolAnnotationInProgressRolloutConfig))
				})
			}
		}
	}
}
