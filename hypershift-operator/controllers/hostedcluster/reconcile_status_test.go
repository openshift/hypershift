package hostedcluster

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestPatchHostedClusterCondition(t *testing.T) {
	t.Run("When a conflict changes the status used to compute a condition, it should recompute from the latest object", func(t *testing.T) {
		g := NewWithT(t)
		hc := credentialHostedCluster()
		hc.Status.CustomKubeconfig = &corev1.LocalObjectReference{Name: "initial"}
		patches := 0
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc).WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subresource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				patches++
				if patches == 1 {
					fresh := &hyperv1.HostedCluster{}
					g.Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), fresh)).To(Succeed())
					fresh.Status.CustomKubeconfig.Name = "concurrent"
					g.Expect(c.Status().Update(ctx, fresh)).To(Succeed())
					return apierrors.NewConflict(hyperv1.SchemeGroupVersion.WithResource("hostedclusters").GroupResource(), hc.Name, errors.New("concurrent status"))
				}
				return c.SubResource(subresource).Patch(ctx, obj, patch, opts...)
			},
		}).Build()
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
		resourceVersion := hc.ResourceVersion
		hc.Status.CustomKubeconfig.Name = "pending"
		r := &HostedClusterReconciler{Client: c}
		compute := func(target *hyperv1.HostedCluster) metav1.Condition {
			return metav1.Condition{Type: string(hyperv1.ReconciliationSucceeded), Status: metav1.ConditionTrue, Reason: "Computed", Message: target.Status.CustomKubeconfig.Name, ObservedGeneration: target.Generation}
		}
		g.Expect(r.patchHostedClusterCondition(t.Context(), hc, hc.Generation, compute)).To(Succeed())
		g.Expect(hc.Status.CustomKubeconfig.Name).To(Equal("pending"))
		g.Expect(meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ReconciliationSucceeded)).Message).To(Equal("concurrent"))
		fresh := &hyperv1.HostedCluster{}
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), fresh)).To(Succeed())
		g.Expect(fresh.Status.CustomKubeconfig.Name).To(Equal("concurrent"))
		g.Expect(meta.FindStatusCondition(fresh.Status.Conditions, string(hyperv1.ReconciliationSucceeded)).Message).To(Equal("concurrent"))
		g.Expect(patches).To(Equal(2))
		g.Expect(hc.ResourceVersion).To(Equal(resourceVersion))
		g.Expect(c.Status().Update(t.Context(), hc)).To(Satisfy(apierrors.IsConflict))
		// Repeating the same condition is a no-op, despite different pending status.
		g.Expect(r.patchHostedClusterCondition(t.Context(), hc, hc.Generation, compute)).To(Succeed())
		g.Expect(patches).To(Equal(2))
	})
}
