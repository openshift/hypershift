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
)

func TestResolveDeployedBootstrapHash(t *testing.T) {
	for _, strategy := range []hyperv1.UpgradeType{hyperv1.UpgradeTypeReplace, hyperv1.UpgradeTypeInPlace} {
		for _, state := range []string{"recorded", "legacy", "missing", "no bootstrap", "no infra", "lookup failure"} {
			t.Run("When the "+string(strategy)+" workload state is "+state+", it should resolve the persisted rollout or return the lookup error", func(t *testing.T) {
				g := NewWithT(t)
				f := newRolloutFixture(t, strategy)
				token := f.capi.Token
				hc := token.hostedCluster
				hc.Spec.InfraID = "infra"
				capi, err := newCAPI(token, hc.Spec.InfraID)
				g.Expect(err).NotTo(HaveOccurred())
				var workload client.Object
				if strategy == hyperv1.UpgradeTypeReplace {
					md := capi.machineDeployment()
					md.Spec.Template = *f.md.Spec.Template.DeepCopy()
					if state == "no bootstrap" {
						md.Spec.Template.Spec.Bootstrap.DataSecretName = nil
					}
					workload = md
				} else {
					ms := capi.machineSet()
					ms.Spec.Template = *f.ms.Spec.Template.DeepCopy()
					if state == "no bootstrap" {
						ms.Spec.Template.Spec.Bootstrap.DataSecretName = nil
					}
					workload = ms
				}
				if state != "legacy" {
					workload.SetAnnotations(map[string]string{nodePoolAnnotationTargetRolloutConfig: token.RolloutHash()})
				}
				if state != "missing" {
					g.Expect(token.Create(t.Context(), workload)).To(Succeed())
				}
				if state == "no infra" {
					hc.Spec.InfraID = ""
				}
				if state == "lookup failure" {
					token.Client = interceptor.NewClient(token.Client.(client.WithWatch), interceptor.Funcs{
						Get: func(ctx context.Context, underlying client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
							switch obj.(type) {
							case *capiv1.MachineDeployment, *capiv1.MachineSet:
								return apierrors.NewServiceUnavailable("workload lookup failed")
							}
							return underlying.Get(ctx, key, obj, opts...)
						},
					})
				}
				r := &NodePoolReconciler{Client: token.Client}
				err = r.resolveDeployedBootstrapHash(t.Context(), token, token.nodePool, hc)
				if state == "lookup failure" {
					g.Expect(apierrors.IsServiceUnavailable(err)).To(BeTrue())
				} else {
					g.Expect(err).NotTo(HaveOccurred())
				}
				if state == "recorded" || state == "legacy" {
					g.Expect(token.deployedBootstrapHash).To(Equal(token.Hash()))
					g.Expect(token.deployedVersion).To(Equal(token.Version()))
				} else {
					g.Expect(token.deployedBootstrapHash).To(BeEmpty())
					g.Expect(token.deployedVersion).To(BeEmpty())
				}
				if state == "recorded" {
					g.Expect(token.deployedRolloutConfig).To(Equal(token.RolloutHash()))
				} else {
					g.Expect(token.deployedRolloutConfig).To(BeEmpty())
				}
			})
		}
	}
}

func TestRolloutTargetHash(t *testing.T) {
	for _, tc := range []struct {
		name, workload, nodePool, expected string
	}{
		{name: "When no request is recorded, it should return empty"},
		{name: "When only NodePool tracking exists, it should use the legacy request", nodePool: "legacy", expected: "legacy"},
		{name: "When only the workload target exists, it should use the persisted request", workload: "persisted", expected: "persisted"},
		{name: "When NodePool tracking is stale, it should prefer the workload target", workload: "persisted", nodePool: "stale", expected: "persisted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := &Token{
				ConfigGenerator:       &ConfigGenerator{nodePool: &hyperv1.NodePool{}},
				deployedRolloutConfig: tc.workload,
			}
			if tc.nodePool != "" {
				token.nodePool.Annotations = map[string]string{nodePoolAnnotationInProgressRolloutConfig: tc.nodePool}
			}
			NewWithT(t).Expect(token.rolloutTargetHash()).To(Equal(tc.expected))
		})
	}
}
