package hostedcluster

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestReconcileCustomKubeconfigSync(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		disabled     bool
		dns          bool
		stale        bool
		retry        bool
		deleteDenied bool
	}{
		{name: "When custom DNS is removed, it should delete the Secret and persist the cleared reference"},
		{name: "When a conflict changes the custom reference, it should recompute cleanup from the latest status", retry: true},
		{name: "When custom kubeconfigs are unsupported, it should preserve the existing Secret and status", disabled: true},
		{name: "When the HostedCluster still requests custom DNS, it should not clear status from an older HCP", dns: true},
		{name: "When the generation changes before cleanup, it should preserve the Secret and reject stale status", stale: true},
		{name: "When Secret deletion fails, it should propagate the error and retain the Secret and both custom references", deleteDenied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			hc := credentialHostedCluster()
			hc.Spec.Platform = hyperv1.PlatformSpec{Type: hyperv1.NonePlatform}
			hc.Status.CustomKubeconfig = &corev1.LocalObjectReference{Name: "old-custom"}
			hc.Status.LastSuccessfulEtcdBackupURL = "s3://initial"
			if tc.dns {
				hc.Spec.KubeAPIServerDNSName = "api.custom.cluster.test"
			}
			oldSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "old-custom"}}
			newSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "new-custom"}}
			patches := 0
			deletionErr := errors.New("custom kubeconfig deletion denied")
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc, oldSecret, newSecret).WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if tc.deleteDenied {
						return deletionErr
					}
					return c.Delete(ctx, obj, opts...)
				},
				SubResourcePatch: func(ctx context.Context, c client.Client, subresource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					patches++
					if tc.retry && patches == 1 {
						fresh := &hyperv1.HostedCluster{}
						g.Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), fresh)).To(Succeed())
						fresh.Status.CustomKubeconfig.Name = newSecret.Name
						fresh.Status.LastSuccessfulEtcdBackupURL = "s3://concurrent"
						g.Expect(c.Status().Update(ctx, fresh)).To(Succeed())
						return apierrors.NewConflict(hyperv1.SchemeGroupVersion.WithResource("hostedclusters").GroupResource(), hc.Name, errors.New("concurrent status"))
					}
					return c.SubResource(subresource).Patch(ctx, obj, patch, opts...)
				},
			}).Build()
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
			resourceVersion := hc.ResourceVersion
			if tc.stale {
				fresh := hc.DeepCopy()
				fresh.Generation++
				fresh.Spec.KubeAPIServerDNSName = "api.custom.cluster.test"
				g.Expect(c.Update(t.Context(), fresh)).To(Succeed())
			}
			r := &HostedClusterReconciler{Client: c}
			requeue, err := r.reconcileCustomKubeconfigSync(t.Context(), hc, &hyperv1.HostedControlPlane{}, upsert.New(false).CreateOrUpdate, !tc.disabled)
			g.Expect(requeue).To(BeNil())
			fresh := &hyperv1.HostedCluster{}
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), fresh)).To(Succeed())
			if tc.stale {
				g.Expect(err).To(MatchError(ContainSubstring("changed")))
			} else if tc.deleteDenied {
				g.Expect(errors.Is(err, deletionErr)).To(BeTrue())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
			if tc.disabled || tc.dns || tc.stale || tc.deleteDenied {
				g.Expect(fresh.Status.CustomKubeconfig).To(Equal(&corev1.LocalObjectReference{Name: oldSecret.Name}))
				g.Expect(hc.Status.CustomKubeconfig).To(Equal(&corev1.LocalObjectReference{Name: oldSecret.Name}))
				g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(oldSecret), oldSecret)).To(Succeed())
			} else {
				g.Expect(fresh.Status.CustomKubeconfig).To(BeNil())
				g.Expect(hc.Status.CustomKubeconfig).To(BeNil())
				g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(oldSecret), oldSecret)).To(Satisfy(apierrors.IsNotFound))
				if tc.retry {
					g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(newSecret), newSecret)).To(Satisfy(apierrors.IsNotFound))
					g.Expect(fresh.Status.LastSuccessfulEtcdBackupURL).To(Equal("s3://concurrent"))
				}
				g.Expect(hc.Status.LastSuccessfulEtcdBackupURL).To(Equal("s3://initial"))
				g.Expect(hc.ResourceVersion).To(Equal(resourceVersion))
				patchCount := patches
				_, err = r.reconcileCustomKubeconfigSync(t.Context(), hc, &hyperv1.HostedControlPlane{}, upsert.New(false).CreateOrUpdate, true)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(patches).To(Equal(patchCount))
			}
		})
	}
}
