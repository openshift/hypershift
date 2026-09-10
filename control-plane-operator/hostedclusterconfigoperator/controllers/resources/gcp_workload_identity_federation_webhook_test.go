package resources

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileGCPIdentityWebhook(t *testing.T) {
	t.Run("When reconciling the GCP identity webhook, it should create all required resources", func(t *testing.T) {
		g := NewWithT(t)
		rootCA := "test-root-ca-bundle"

		fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		r := &reconciler{
			client:                 fakeClient,
			CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			rootCA:                 rootCA,
			platformType:           hyperv1.GCPPlatform,
		}

		errs := r.reconcileGCPIdentityWebhook(t.Context())
		g.Expect(errs).To(BeEmpty())

		clusterRole := manifests.GCPWorkloadIdentityFederationWebhookClusterRole()
		err := fakeClient.Get(t.Context(), client.ObjectKeyFromObject(clusterRole), clusterRole)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(clusterRole.Rules).To(HaveLen(1))
		g.Expect(clusterRole.Rules[0].APIGroups).To(Equal([]string{""}))
		g.Expect(clusterRole.Rules[0].Resources).To(Equal([]string{"serviceaccounts"}))
		g.Expect(clusterRole.Rules[0].Verbs).To(Equal([]string{"get", "list", "watch"}))

		crb := manifests.GCPWorkloadIdentityFederationWebhookClusterRoleBinding()
		err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(crb), crb)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(crb.RoleRef.Kind).To(Equal("ClusterRole"))
		g.Expect(crb.RoleRef.Name).To(Equal("gcp-workload-identity-federation-webhook"))
		g.Expect(crb.Subjects).To(HaveLen(1))
		g.Expect(crb.Subjects[0].Kind).To(Equal("ServiceAccount"))
		g.Expect(crb.Subjects[0].Name).To(Equal("gcp-workload-identity-federation-webhook"))
		g.Expect(crb.Subjects[0].Namespace).To(Equal("openshift-authentication"))

		webhook := manifests.GCPWorkloadIdentityFederationWebhook()
		err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(webhook), webhook)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(webhook.Webhooks).To(HaveLen(1))

		wh := webhook.Webhooks[0]
		g.Expect(wh.Name).To(Equal("pod-identity-webhook.gcp.mutate.io"))
		g.Expect(wh.AdmissionReviewVersions).To(Equal([]string{"v1"}))
		g.Expect(*wh.FailurePolicy).To(Equal(admissionregistrationv1.Ignore))
		g.Expect(*wh.MatchPolicy).To(Equal(admissionregistrationv1.Equivalent))
		g.Expect(*wh.ReinvocationPolicy).To(Equal(admissionregistrationv1.IfNeededReinvocationPolicy))
		g.Expect(*wh.SideEffects).To(Equal(admissionregistrationv1.SideEffectClassNone))
		g.Expect(wh.ObjectSelector).To(BeNil())

		g.Expect(wh.ClientConfig.URL).ToNot(BeNil())
		g.Expect(*wh.ClientConfig.URL).To(Equal("https://127.0.0.1:9443/mutate-v1-pod"))
		g.Expect(string(wh.ClientConfig.CABundle)).To(Equal(rootCA))

		g.Expect(wh.Rules).To(HaveLen(1))
		g.Expect(wh.Rules[0].Operations).To(Equal([]admissionregistrationv1.OperationType{admissionregistrationv1.Create}))
		g.Expect(wh.Rules[0].Rule.APIGroups).To(Equal([]string{""}))
		g.Expect(wh.Rules[0].Rule.APIVersions).To(Equal([]string{"v1"}))
		g.Expect(wh.Rules[0].Rule.Resources).To(Equal([]string{"pods"}))
	})
}

func TestReconcileGCPIdentityWebhookIdempotent(t *testing.T) {
	t.Run("When reconciling the GCP identity webhook twice, it should not produce errors", func(t *testing.T) {
		g := NewWithT(t)

		fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		r := &reconciler{
			client:                 fakeClient,
			CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			rootCA:                 "test-ca",
			platformType:           hyperv1.GCPPlatform,
		}

		errs := r.reconcileGCPIdentityWebhook(t.Context())
		g.Expect(errs).To(BeEmpty())

		errs = r.reconcileGCPIdentityWebhook(t.Context())
		g.Expect(errs).To(BeEmpty())

		clusterRole := &rbacv1.ClusterRole{}
		err := fakeClient.Get(t.Context(), client.ObjectKeyFromObject(manifests.GCPWorkloadIdentityFederationWebhookClusterRole()), clusterRole)
		g.Expect(err).ToNot(HaveOccurred())

		crb := &rbacv1.ClusterRoleBinding{}
		err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(manifests.GCPWorkloadIdentityFederationWebhookClusterRoleBinding()), crb)
		g.Expect(err).ToNot(HaveOccurred())

		webhook := &admissionregistrationv1.MutatingWebhookConfiguration{}
		err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(manifests.GCPWorkloadIdentityFederationWebhook()), webhook)
		g.Expect(err).ToNot(HaveOccurred())
	})
}
