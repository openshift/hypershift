package webhookcerts

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/certs"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	g := NewWithT(t)
	s := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(s)).To(Succeed())
	g.Expect(admissionregistrationv1.AddToScheme(s)).To(Succeed())
	g.Expect(apiextensionsv1.AddToScheme(s)).To(Succeed())
	return s
}

func newReconciler(cl client.Client) *WebhookCertReconciler {
	return &WebhookCertReconciler{
		Client:      cl,
		Namespace:   "hypershift",
		ServiceName: "operator",
		createOrUpdate: func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
			return controllerutil.CreateOrUpdate(ctx, c, obj, f)
		},
	}
}

func caRequest() ctrl.Request {
	return ctrl.Request{NamespacedName: client.ObjectKey{Name: CASecretName, Namespace: "hypershift"}}
}

func TestReconcile(t *testing.T) {
	t.Run("When no secrets exist it should create the CA and serving cert secrets", func(t *testing.T) {
		g := NewWithT(t)

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
		r := newReconciler(cl)

		result, err := r.Reconcile(t.Context(), caRequest())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.RequeueAfter).To(Equal(12 * time.Hour))

		// CA secret should exist with CA cert data.
		caSecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: CASecretName, Namespace: "hypershift"}, caSecret)).To(Succeed())
		g.Expect(caSecret.Data).To(HaveKey(certs.CASignerCertMapKey))
		g.Expect(caSecret.Type).To(Equal(corev1.SecretTypeOpaque))

		// Serving cert secret should exist with TLS cert data.
		servingSecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: ServingCertSecretName, Namespace: "hypershift"}, servingSecret)).To(Succeed())
		g.Expect(servingSecret.Data).To(HaveKey(corev1.TLSCertKey))
		g.Expect(servingSecret.Data).To(HaveKey(corev1.TLSPrivateKeyKey))
		g.Expect(servingSecret.Type).To(Equal(corev1.SecretTypeTLS))
	})

	t.Run("When secrets already exist it should not error and should requeue", func(t *testing.T) {
		g := NewWithT(t)

		// Pre-create valid secrets via GenerateWebhookCerts.
		caSecret, servingSecret, _, err := GenerateWebhookCerts("hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, servingSecret).Build()
		r := newReconciler(cl)

		result, err := r.Reconcile(t.Context(), caRequest())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.RequeueAfter).To(Equal(12 * time.Hour))
	})

	t.Run("When a request is for a different namespace it should skip reconciliation", func(t *testing.T) {
		g := NewWithT(t)

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
		r := newReconciler(cl)

		req := ctrl.Request{NamespacedName: client.ObjectKey{Name: CASecretName, Namespace: "other-ns"}}
		result, err := r.Reconcile(t.Context(), req)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))
	})

	t.Run("When a request is for an unrelated secret it should skip reconciliation", func(t *testing.T) {
		g := NewWithT(t)

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
		r := newReconciler(cl)

		req := ctrl.Request{NamespacedName: client.ObjectKey{Name: "some-other-secret", Namespace: "hypershift"}}
		result, err := r.Reconcile(t.Context(), req)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))
	})

	t.Run("When a CRD has a conversion webhook it should patch its caBundle", func(t *testing.T) {
		g := NewWithT(t)

		crd := &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: "hostedclusters.hypershift.openshift.io"},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: "hypershift.openshift.io",
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural:   "hostedclusters",
					Singular: "hostedcluster",
					Kind:     "HostedCluster",
				},
				Scope: apiextensionsv1.NamespaceScoped,
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{Name: "v1beta1", Served: true, Storage: true, Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{Type: "object"},
					}},
				},
				Conversion: &apiextensionsv1.CustomResourceConversion{
					Strategy: apiextensionsv1.WebhookConverter,
					Webhook: &apiextensionsv1.WebhookConversion{
						ClientConfig:             &apiextensionsv1.WebhookClientConfig{CABundle: []byte("old-ca")},
						ConversionReviewVersions: []string{"v1beta1"},
					},
				},
			},
		}

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd).Build()
		r := newReconciler(cl)

		_, err := r.Reconcile(t.Context(), caRequest())
		g.Expect(err).ToNot(HaveOccurred())

		updatedCRD := &apiextensionsv1.CustomResourceDefinition{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: crd.Name}, updatedCRD)).To(Succeed())
		g.Expect(updatedCRD.Spec.Conversion.Webhook.ClientConfig.CABundle).ToNot(Equal([]byte("old-ca")))
		g.Expect(updatedCRD.Spec.Conversion.Webhook.ClientConfig.CABundle).ToNot(BeEmpty())
	})

	t.Run("When a CRD is not in the hypershift group it should not patch its caBundle", func(t *testing.T) {
		g := NewWithT(t)

		crd := &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: "others.example.io"},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: "example.io",
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural:   "others",
					Singular: "other",
					Kind:     "Other",
				},
				Scope: apiextensionsv1.NamespaceScoped,
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{Name: "v1", Served: true, Storage: true, Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{Type: "object"},
					}},
				},
				Conversion: &apiextensionsv1.CustomResourceConversion{
					Strategy: apiextensionsv1.WebhookConverter,
					Webhook: &apiextensionsv1.WebhookConversion{
						ClientConfig:             &apiextensionsv1.WebhookClientConfig{CABundle: []byte("unchanged")},
						ConversionReviewVersions: []string{"v1"},
					},
				},
			},
		}

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd).Build()
		r := newReconciler(cl)

		_, err := r.Reconcile(t.Context(), caRequest())
		g.Expect(err).ToNot(HaveOccurred())

		updatedCRD := &apiextensionsv1.CustomResourceDefinition{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: crd.Name}, updatedCRD)).To(Succeed())
		g.Expect(updatedCRD.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal([]byte("unchanged")))
	})

	t.Run("When webhook configurations exist it should patch their caBundle", func(t *testing.T) {
		g := NewWithT(t)

		mwc := &admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "hypershift.openshift.io"},
			Webhooks: []admissionregistrationv1.MutatingWebhook{
				{
					Name:                    "defaulting.hypershift.openshift.io",
					ClientConfig:            admissionregistrationv1.WebhookClientConfig{CABundle: []byte("old")},
					SideEffects:             sideEffectNone(),
					AdmissionReviewVersions: []string{"v1"},
				},
			},
		}
		vwc := &admissionregistrationv1.ValidatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "hypershift.openshift.io"},
			Webhooks: []admissionregistrationv1.ValidatingWebhook{
				{
					Name:                    "validating.hypershift.openshift.io",
					ClientConfig:            admissionregistrationv1.WebhookClientConfig{CABundle: []byte("old")},
					SideEffects:             sideEffectNone(),
					AdmissionReviewVersions: []string{"v1"},
				},
			},
		}

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(mwc, vwc).Build()
		r := newReconciler(cl)

		_, err := r.Reconcile(t.Context(), caRequest())
		g.Expect(err).ToNot(HaveOccurred())

		updatedMWC := &admissionregistrationv1.MutatingWebhookConfiguration{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: "hypershift.openshift.io"}, updatedMWC)).To(Succeed())
		g.Expect(updatedMWC.Webhooks[0].ClientConfig.CABundle).ToNot(Equal([]byte("old")))
		g.Expect(updatedMWC.Webhooks[0].ClientConfig.CABundle).ToNot(BeEmpty())

		updatedVWC := &admissionregistrationv1.ValidatingWebhookConfiguration{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: "hypershift.openshift.io"}, updatedVWC)).To(Succeed())
		g.Expect(updatedVWC.Webhooks[0].ClientConfig.CABundle).ToNot(Equal([]byte("old")))
		g.Expect(updatedVWC.Webhooks[0].ClientConfig.CABundle).ToNot(BeEmpty())
	})

	t.Run("When webhook configurations do not exist it should not error", func(t *testing.T) {
		g := NewWithT(t)

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
		r := newReconciler(cl)

		_, err := r.Reconcile(t.Context(), caRequest())
		g.Expect(err).ToNot(HaveOccurred())
	})
}

func TestGenerateWebhookCerts(t *testing.T) {
	t.Run("When generating certs it should return valid CA and serving cert secrets", func(t *testing.T) {
		g := NewWithT(t)

		caSecret, servingSecret, caBundle, err := GenerateWebhookCerts("hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		// CA secret
		g.Expect(caSecret.Name).To(Equal(CASecretName))
		g.Expect(caSecret.Namespace).To(Equal("hypershift"))
		g.Expect(caSecret.Data).To(HaveKey(certs.CASignerCertMapKey))
		g.Expect(caBundle).ToNot(BeEmpty())
		g.Expect(caBundle).To(Equal(caSecret.Data[certs.CASignerCertMapKey]))

		// Serving cert secret
		g.Expect(servingSecret.Name).To(Equal(ServingCertSecretName))
		g.Expect(servingSecret.Namespace).To(Equal("hypershift"))
		g.Expect(servingSecret.Type).To(Equal(corev1.SecretTypeTLS))
		g.Expect(servingSecret.Data).To(HaveKey(corev1.TLSCertKey))
		g.Expect(servingSecret.Data).To(HaveKey(corev1.TLSPrivateKeyKey))

	})
}

func TestWebhookDNSNames(t *testing.T) {
	t.Run("When given service name and namespace it should return correct DNS names", func(t *testing.T) {
		g := NewWithT(t)

		names := webhookDNSNames("operator", "hypershift")
		g.Expect(names).To(ConsistOf(
			"operator.hypershift.svc",
			"operator.hypershift.svc.cluster.local",
		))
	})
}

func sideEffectNone() *admissionregistrationv1.SideEffectClass {
	se := admissionregistrationv1.SideEffectClassNone
	return &se
}
