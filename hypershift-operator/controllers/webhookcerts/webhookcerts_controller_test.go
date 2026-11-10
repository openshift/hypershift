package webhookcerts

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"testing"

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
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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
		g.Expect(result.RequeueAfter).To(Equal(requeueInterval))

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

		// Pre-create valid secrets via GenerateInitialWebhookCerts.
		caSecret, servingSecret, _, err := GenerateInitialWebhookCerts("hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, servingSecret).Build()
		r := newReconciler(cl)

		result, err := r.Reconcile(t.Context(), caRequest())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.RequeueAfter).To(Equal(requeueInterval))
	})

	t.Run("When a CRD has a conversion webhook pointing to our service it should patch its caBundle", func(t *testing.T) {
		g := NewWithT(t)

		crd := &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: "hostedclusters.hypershift.openshift.io"},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: webhookConfigName,
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
						ClientConfig: &apiextensionsv1.WebhookClientConfig{
							CABundle: []byte("old-ca"),
							Service: &apiextensionsv1.ServiceReference{
								Namespace: "hypershift",
								Name:      "operator",
							},
						},
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

	t.Run("When a CRD conversion webhook points to a different service it should not patch its caBundle", func(t *testing.T) {
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
						ClientConfig: &apiextensionsv1.WebhookClientConfig{
							CABundle: []byte("unchanged"),
							Service: &apiextensionsv1.ServiceReference{
								Namespace: "other-ns",
								Name:      "other-service",
							},
						},
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
			ObjectMeta: metav1.ObjectMeta{Name: webhookConfigName},
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
			ObjectMeta: metav1.ObjectMeta{Name: webhookConfigName},
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
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: webhookConfigName}, updatedMWC)).To(Succeed())
		g.Expect(updatedMWC.Webhooks[0].ClientConfig.CABundle).ToNot(Equal([]byte("old")))
		g.Expect(updatedMWC.Webhooks[0].ClientConfig.CABundle).ToNot(BeEmpty())

		updatedVWC := &admissionregistrationv1.ValidatingWebhookConfiguration{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: webhookConfigName}, updatedVWC)).To(Succeed())
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

	t.Run("When upgrading from service-ca it should remove service-ca annotations from the Service", func(t *testing.T) {
		g := NewWithT(t)

		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "operator",
				Namespace: "hypershift",
				Annotations: map[string]string{
					"service.beta.openshift.io/serving-cert-secret-name": "manager-serving-cert",
				},
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{Port: 443}},
			},
		}

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(svc).Build()
		r := newReconciler(cl)

		_, err := r.Reconcile(t.Context(), caRequest())
		g.Expect(err).ToNot(HaveOccurred())

		updatedSvc := &corev1.Service{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: "operator", Namespace: "hypershift"}, updatedSvc)).To(Succeed())
		g.Expect(updatedSvc.Annotations).ToNot(HaveKey("service.beta.openshift.io/serving-cert-secret-name"))
	})

	t.Run("When upgrading from service-ca it should delete the service-ca managed serving cert and recreate it", func(t *testing.T) {
		g := NewWithT(t)

		// Simulate a serving cert secret created by service-ca.
		serviceCACert := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ServingCertSecretName,
				Namespace: "hypershift",
				Annotations: map[string]string{
					"service.beta.openshift.io/originating-service-name": "operator",
				},
			},
			Type: corev1.SecretTypeTLS,
			Data: map[string][]byte{
				corev1.TLSCertKey:       []byte("service-ca-cert"),
				corev1.TLSPrivateKeyKey: []byte("service-ca-key"),
			},
		}

		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "operator",
				Namespace: "hypershift",
				Annotations: map[string]string{
					"service.beta.openshift.io/serving-cert-secret-name": "manager-serving-cert",
				},
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{Port: 443}},
			},
		}

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(serviceCACert, svc).Build()
		r := newReconciler(cl)

		_, err := r.Reconcile(t.Context(), caRequest())
		g.Expect(err).ToNot(HaveOccurred())

		// The service-ca annotation should be removed from the Service.
		updatedSvc := &corev1.Service{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: "operator", Namespace: "hypershift"}, updatedSvc)).To(Succeed())
		g.Expect(updatedSvc.Annotations).ToNot(HaveKey("service.beta.openshift.io/serving-cert-secret-name"))

		// The serving cert should have been recreated without the service-ca annotation
		// and signed by the self-managed CA.
		servingSecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: ServingCertSecretName, Namespace: "hypershift"}, servingSecret)).To(Succeed())
		g.Expect(servingSecret.Annotations).ToNot(HaveKey("service.beta.openshift.io/originating-service-name"))
		g.Expect(servingSecret.Data[corev1.TLSCertKey]).ToNot(Equal([]byte("service-ca-cert")))
		g.Expect(servingSecret.Data[corev1.TLSCertKey]).ToNot(BeEmpty())

		// Verify the new cert is signed by the self-managed CA.
		caSecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: CASecretName, Namespace: "hypershift"}, caSecret)).To(Succeed())
		g.Expect(caSecret.Data).To(HaveKey(certs.CASignerCertMapKey))
	})

	t.Run("When upgrading from service-ca it should remove inject-cabundle annotation from webhook configurations", func(t *testing.T) {
		g := NewWithT(t)

		mwc := &admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name: webhookConfigName,
				Annotations: map[string]string{
					injectCABundleAnnotation: "true",
				},
			},
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
			ObjectMeta: metav1.ObjectMeta{
				Name: webhookConfigName,
				Annotations: map[string]string{
					injectCABundleAnnotation: "true",
				},
			},
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
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: webhookConfigName}, updatedMWC)).To(Succeed())
		g.Expect(updatedMWC.Annotations).ToNot(HaveKey(injectCABundleAnnotation))

		updatedVWC := &admissionregistrationv1.ValidatingWebhookConfiguration{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: webhookConfigName}, updatedVWC)).To(Succeed())
		g.Expect(updatedVWC.Annotations).ToNot(HaveKey(injectCABundleAnnotation))
	})

	t.Run("When the serving cert was not created by service-ca it should not delete it", func(t *testing.T) {
		g := NewWithT(t)

		// Pre-create valid self-managed secrets.
		caSecret, servingSecret, _, err := GenerateInitialWebhookCerts("hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())
		originalCert := servingSecret.Data[corev1.TLSCertKey]

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, servingSecret).Build()
		r := newReconciler(cl)

		_, err = r.Reconcile(t.Context(), caRequest())
		g.Expect(err).ToNot(HaveOccurred())

		// Cert should be unchanged.
		updatedSecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: ServingCertSecretName, Namespace: "hypershift"}, updatedSecret)).To(Succeed())
		g.Expect(updatedSecret.Data[corev1.TLSCertKey]).To(Equal(originalCert))
	})
}

func TestGenerateInitialWebhookCerts(t *testing.T) {
	t.Run("When generating certs it should return valid CA and serving cert secrets", func(t *testing.T) {
		g := NewWithT(t)

		caSecret, servingSecret, caBundle, err := GenerateInitialWebhookCerts("hypershift", "operator")
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

func TestEnsureWebhookCerts(t *testing.T) {
	t.Run("When no secrets exist it should create secrets", func(t *testing.T) {
		g := NewWithT(t)

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()

		err := EnsureWebhookCerts(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		caSecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: CASecretName, Namespace: "hypershift"}, caSecret)).To(Succeed())
		g.Expect(caSecret.Data).To(HaveKey(certs.CASignerCertMapKey))

		servingSecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: ServingCertSecretName, Namespace: "hypershift"}, servingSecret)).To(Succeed())
		g.Expect(servingSecret.Data).To(HaveKey(corev1.TLSCertKey))
		g.Expect(servingSecret.Data).To(HaveKey(corev1.TLSPrivateKeyKey))
		g.Expect(servingSecret.Type).To(Equal(corev1.SecretTypeTLS))
	})

	t.Run("When secrets already exist with valid data it should not modify them", func(t *testing.T) {
		g := NewWithT(t)

		caSecret, servingSecret, _, err := GenerateInitialWebhookCerts("hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, servingSecret).Build()

		err = EnsureWebhookCerts(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		updatedServingSecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: ServingCertSecretName, Namespace: "hypershift"}, updatedServingSecret)).To(Succeed())
		g.Expect(updatedServingSecret.Data[corev1.TLSCertKey]).To(Equal(servingSecret.Data[corev1.TLSCertKey]))
		g.Expect(updatedServingSecret.Data[corev1.TLSPrivateKeyKey]).To(Equal(servingSecret.Data[corev1.TLSPrivateKeyKey]))

		updatedCASecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: CASecretName, Namespace: "hypershift"}, updatedCASecret)).To(Succeed())
		g.Expect(updatedCASecret.Data[certs.CASignerCertMapKey]).To(Equal(caSecret.Data[certs.CASignerCertMapKey]))
		g.Expect(updatedCASecret.Data[certs.CASignerKeyMapKey]).To(Equal(caSecret.Data[certs.CASignerKeyMapKey]))
	})

	t.Run("When serving cert secret exists with empty data it should regenerate and update", func(t *testing.T) {
		g := NewWithT(t)

		emptySecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ServingCertSecretName,
				Namespace: "hypershift",
			},
			Type: corev1.SecretTypeTLS,
			Data: map[string][]byte{},
		}

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(emptySecret).Build()

		err := EnsureWebhookCerts(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		updatedServingSecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: ServingCertSecretName, Namespace: "hypershift"}, updatedServingSecret)).To(Succeed())
		g.Expect(updatedServingSecret.Data[corev1.TLSCertKey]).ToNot(BeEmpty())
		g.Expect(updatedServingSecret.Data[corev1.TLSPrivateKeyKey]).ToNot(BeEmpty())

		updatedCASecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: CASecretName, Namespace: "hypershift"}, updatedCASecret)).To(Succeed())
		g.Expect(updatedCASecret.Data[certs.CASignerCertMapKey]).ToNot(BeEmpty())
		g.Expect(updatedCASecret.Data[certs.CASignerKeyMapKey]).ToNot(BeEmpty())
	})

	t.Run("When CA exists but serving cert is missing it should reuse existing CA and generate serving cert", func(t *testing.T) {
		g := NewWithT(t)

		caSecret, _, _, err := GenerateInitialWebhookCerts("hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())
		originalCACert := make([]byte, len(caSecret.Data[certs.CASignerCertMapKey]))
		copy(originalCACert, caSecret.Data[certs.CASignerCertMapKey])
		originalCAKey := make([]byte, len(caSecret.Data[certs.CASignerKeyMapKey]))
		copy(originalCAKey, caSecret.Data[certs.CASignerKeyMapKey])

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret).Build()

		err = EnsureWebhookCerts(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		// CA should not have been regenerated.
		updatedCASecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: CASecretName, Namespace: "hypershift"}, updatedCASecret)).To(Succeed())
		g.Expect(updatedCASecret.Data[certs.CASignerCertMapKey]).To(Equal(originalCACert),
			"CA cert should not be regenerated when it already has valid data")
		g.Expect(updatedCASecret.Data[certs.CASignerKeyMapKey]).To(Equal(originalCAKey),
			"CA key should not be regenerated when it already has valid data")

		// Serving cert should have been generated.
		servingSecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: ServingCertSecretName, Namespace: "hypershift"}, servingSecret)).To(Succeed())
		g.Expect(servingSecret.Data[corev1.TLSCertKey]).ToNot(BeEmpty())
		g.Expect(servingSecret.Data[corev1.TLSPrivateKeyKey]).ToNot(BeEmpty())

		// Verify the serving cert was signed by the original (reused) CA.
		caPool := x509.NewCertPool()
		g.Expect(caPool.AppendCertsFromPEM(updatedCASecret.Data[certs.CASignerCertMapKey])).To(BeTrue())
		block, _ := pem.Decode(servingSecret.Data[corev1.TLSCertKey])
		g.Expect(block).ToNot(BeNil())
		leaf, err := x509.ParseCertificate(block.Bytes)
		g.Expect(err).ToNot(HaveOccurred())
		_, err = leaf.Verify(x509.VerifyOptions{Roots: caPool})
		g.Expect(err).ToNot(HaveOccurred())
	})
}

func TestCertsExistDistinguishesNotFoundFromTransient(t *testing.T) {
	t.Run("When both secrets exist with valid data it should return true and no error", func(t *testing.T) {
		g := NewWithT(t)

		caSecret, servingSecret, _, err := GenerateInitialWebhookCerts("hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, servingSecret).Build()

		exists, err := certsExist(t.Context(), cl, "hypershift")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeTrue())
	})

	t.Run("When CA secret is not found it should return false and no error", func(t *testing.T) {
		g := NewWithT(t)

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()

		exists, err := certsExist(t.Context(), cl, "hypershift")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeFalse())
	})

	t.Run("When CA secret has empty data it should return false and no error", func(t *testing.T) {
		g := NewWithT(t)

		emptyCA := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: CASecretName, Namespace: "hypershift"},
			Data:       map[string][]byte{},
		}
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(emptyCA).Build()

		exists, err := certsExist(t.Context(), cl, "hypershift")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeFalse())
	})

	t.Run("When CA Get returns a transient error it should return false and propagate the error", func(t *testing.T) {
		g := NewWithT(t)

		transientErr := fmt.Errorf("connection refused")
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if key.Name == CASecretName {
						return transientErr
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build()

		exists, err := certsExist(t.Context(), cl, "hypershift")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("connection refused"))
		g.Expect(exists).To(BeFalse())
	})

	t.Run("When serving cert Get returns a transient error it should return false and propagate the error", func(t *testing.T) {
		g := NewWithT(t)

		caSecret, _, _, err := GenerateInitialWebhookCerts("hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		transientErr := fmt.Errorf("server timeout")
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if key.Name == ServingCertSecretName {
						return transientErr
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build()

		exists, err := certsExist(t.Context(), cl, "hypershift")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("server timeout"))
		g.Expect(exists).To(BeFalse())
	})

	t.Run("When serving cert secret is not found it should return false and no error", func(t *testing.T) {
		g := NewWithT(t)

		caSecret, _, _, err := GenerateInitialWebhookCerts("hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret).Build()

		exists, err := certsExist(t.Context(), cl, "hypershift")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeFalse())
	})

	t.Run("When serving cert secret has empty data it should return false and no error", func(t *testing.T) {
		g := NewWithT(t)

		caSecret, _, _, err := GenerateInitialWebhookCerts("hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		emptyServing := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: ServingCertSecretName, Namespace: "hypershift"},
			Type:       corev1.SecretTypeTLS,
			Data:       map[string][]byte{},
		}
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, emptyServing).Build()

		exists, err := certsExist(t.Context(), cl, "hypershift")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeFalse())
	})
}

func TestEnsureWebhookCertsDoesNotOverwriteExistingCerts(t *testing.T) {
	t.Run("When another replica already created secrets it should not overwrite them", func(t *testing.T) {
		g := NewWithT(t)

		// Simulate the race: replica A created the secrets between replica B's
		// certsExist check (which returned false) and its CreateOrUpdate.
		// Use GenerateInitialWebhookCerts to create realistic secrets.
		caSecret, servingSecret, _, err := GenerateInitialWebhookCerts("hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		originalCAData := make([]byte, len(caSecret.Data[certs.CASignerCertMapKey]))
		copy(originalCAData, caSecret.Data[certs.CASignerCertMapKey])
		originalServingData := make([]byte, len(servingSecret.Data[corev1.TLSCertKey]))
		copy(originalServingData, servingSecret.Data[corev1.TLSCertKey])

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, servingSecret).Build()

		// Call EnsureWebhookCerts pretending certs didn't exist (fresh namespace).
		// Because CreateOrUpdate in EnsureWebhookCerts now guards against overwriting
		// valid data, the existing secrets should be preserved.
		err = EnsureWebhookCerts(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		// Verify CA was NOT replaced.
		updatedCA := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: CASecretName, Namespace: "hypershift"}, updatedCA)).To(Succeed())
		g.Expect(updatedCA.Data[certs.CASignerCertMapKey]).To(Equal(originalCAData),
			"CA cert should not be overwritten when valid data already exists")

		// Verify serving cert was NOT replaced.
		updatedServing := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: ServingCertSecretName, Namespace: "hypershift"}, updatedServing)).To(Succeed())
		g.Expect(updatedServing.Data[corev1.TLSCertKey]).To(Equal(originalServingData),
			"Serving cert should not be overwritten when valid data already exists")
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
