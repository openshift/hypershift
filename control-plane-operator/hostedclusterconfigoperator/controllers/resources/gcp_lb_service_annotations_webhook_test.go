package resources

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileGCPLBServiceAnnotationsWebhook(t *testing.T) {
	g := NewWithT(t)
	guestClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
	r := &reconciler{
		client:                 guestClient,
		CreateOrUpdateProvider: &simpleCreateOrUpdater{},
		rootCA:                 "test-ca",
	}
	g.Expect(r.reconcileGCPLBServiceAnnotationsWebhook(t.Context())).To(BeEmpty())

	webhook := &admissionregistrationv1.MutatingWebhookConfiguration{}
	g.Expect(guestClient.Get(t.Context(), client.ObjectKeyFromObject(manifests.GCPLBServiceAnnotationsWebhook()), webhook)).To(Succeed())
	g.Expect(webhook.Webhooks).To(HaveLen(1))
	g.Expect(webhook.Webhooks[0].Name).To(Equal("lb-service-annotations.gcp.hypershift.openshift.io"))
	g.Expect(webhook.Webhooks[0].ClientConfig.URL).To(HaveValue(Equal("https://127.0.0.1:8443/mutate")))
	g.Expect(webhook.Webhooks[0].ClientConfig.CABundle).To(Equal([]byte("test-ca")))
	g.Expect(webhook.Webhooks[0].Rules).To(ConsistOf(admissionregistrationv1.RuleWithOperations{
		Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create, admissionregistrationv1.Update},
		Rule: admissionregistrationv1.Rule{
			APIGroups:   []string{""},
			APIVersions: []string{"v1"},
			Resources:   []string{"services"},
		},
	}))
}
