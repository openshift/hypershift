package hostedcontrolplane

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestReconcileGMPCollectorAccess(t *testing.T) {
	newHCP := func() *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-example", Name: "example", UID: "hcp-uid"},
		}
	}
	newReconciler := func(client crclient.Client) *HostedControlPlaneReconciler {
		r := &HostedControlPlaneReconciler{Client: client}
		r.setup(upsert.New(false).CreateOrUpdate)
		return r
	}

	t.Run("When the collector access is reconciled, it should grant the collector read access to exactly the monitored TLS secrets", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hcp := newHCP()
		client := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		r := newReconciler(client)

		g.Expect(r.reconcileGMPCollectorAccess(t.Context(), hcp, upsert.New(false).CreateOrUpdate)).To(Succeed())

		role := manifests.GMPCollectorRole(hcp.Namespace)
		g.Expect(client.Get(t.Context(), crclient.ObjectKeyFromObject(role), role)).To(Succeed())
		g.Expect(role.OwnerReferences).To(Equal([]metav1.OwnerReference{*config.ControllerOwnerRef(hcp)}))
		g.Expect(role.Rules).To(Equal([]rbacv1.PolicyRule{{
			APIGroups:     []string{""},
			Resources:     []string{"secrets"},
			ResourceNames: manifests.GMPCollectorSecretNames,
			Verbs:         []string{"get", "list", "watch"},
		}}))

		binding := manifests.GMPCollectorRoleBinding(hcp.Namespace)
		g.Expect(client.Get(t.Context(), crclient.ObjectKeyFromObject(binding), binding)).To(Succeed())
		g.Expect(binding.OwnerReferences).To(Equal([]metav1.OwnerReference{*config.ControllerOwnerRef(hcp)}))
		g.Expect(binding.RoleRef).To(Equal(rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     role.Name,
		}))
		// The subject namespace must stay gke-gmp-system: rewriting it to the
		// control-plane namespace would bind a ServiceAccount that does not exist.
		g.Expect(binding.Subjects).To(Equal([]rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      manifests.GMPCollectorServiceAccountName,
			Namespace: manifests.GMPCollectorNamespace,
		}}))
	})

	t.Run("When the collector access already exists, it should reconcile it again without error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hcp := newHCP()
		client := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		r := newReconciler(client)

		g.Expect(r.reconcileGMPCollectorAccess(t.Context(), hcp, upsert.New(false).CreateOrUpdate)).To(Succeed())
		g.Expect(r.reconcileGMPCollectorAccess(t.Context(), hcp, upsert.New(false).CreateOrUpdate)).To(Succeed())
	})

	t.Run("When the Role cannot be written, it should return an error naming the Role", func(t *testing.T) {
		g := NewGomegaWithT(t)
		client := fake.NewClientBuilder().WithScheme(api.Scheme).WithInterceptorFuncs(interceptor.Funcs{
			Create: func(_ context.Context, _ crclient.WithWatch, obj crclient.Object, _ ...crclient.CreateOption) error {
				if _, ok := obj.(*rbacv1.Role); ok {
					return errors.New("role write failed")
				}
				return nil
			},
		}).Build()
		r := newReconciler(client)

		err := r.reconcileGMPCollectorAccess(t.Context(), newHCP(), upsert.New(false).CreateOrUpdate)
		g.Expect(err).To(MatchError(ContainSubstring("failed to reconcile GMP collector role")))
	})

	t.Run("When the RoleBinding cannot be written, it should return an error naming the RoleBinding", func(t *testing.T) {
		g := NewGomegaWithT(t)
		client := fake.NewClientBuilder().WithScheme(api.Scheme).WithInterceptorFuncs(interceptor.Funcs{
			Create: func(_ context.Context, _ crclient.WithWatch, obj crclient.Object, _ ...crclient.CreateOption) error {
				if _, ok := obj.(*rbacv1.RoleBinding); ok {
					return errors.New("role binding write failed")
				}
				return nil
			},
		}).Build()
		r := newReconciler(client)

		err := r.reconcileGMPCollectorAccess(t.Context(), newHCP(), upsert.New(false).CreateOrUpdate)
		g.Expect(err).To(MatchError(ContainSubstring("failed to reconcile GMP collector role binding")))
	})
}
