package webhookcerts

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/certs"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
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

type caBundles struct {
	webhookCA []byte
	foreignCA []byte
}

// generateCABundles generates the CA bundles once per test binary; key generation is slow and the
// bundles are only read by tests.
var generateCABundles = sync.OnceValues(func() (caBundles, error) {
	_, _, webhookCA, err := GenerateInitialWebhookCerts("hypershift", "operator")
	if err != nil {
		return caBundles{}, err
	}
	_, _, foreignCA, err := GenerateInitialWebhookCerts("openshift-service-ca", "service-ca")
	if err != nil {
		return caBundles{}, err
	}
	return caBundles{webhookCA: webhookCA, foreignCA: foreignCA}, nil
})

// testCABundles returns two distinct PEM CA bundles: the bundle of the operator's self-managed
// webhook CA and a bundle from a different signer, standing in for the service-ca signer.
func testCABundles(t *testing.T) (webhookCA []byte, foreignCA []byte) {
	t.Helper()
	bundles, err := generateCABundles()
	NewWithT(t).Expect(err).ToNot(HaveOccurred())
	return bundles.webhookCA, bundles.foreignCA
}

// newConversionCRD returns a CAPI CRD whose conversion webhook targets the given Service, modeled on the
// conversion config rendered by hypershift install. Names.Kind is not significant for these fake-client tests.
func newConversionCRD(plural, svcNamespace, svcName string, caBundle []byte, annotations map[string]string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: plural + ".cluster.x-k8s.io", Annotations: annotations},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "cluster.x-k8s.io",
			Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: plural, Kind: "Cluster"},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{Name: "v1beta1", Served: true, Storage: true, Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{Type: "object"},
				}},
				{Name: "v1beta2", Served: true, Storage: false, Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{Type: "object"},
				}},
			},
			Conversion: &apiextensionsv1.CustomResourceConversion{
				Strategy: apiextensionsv1.WebhookConverter,
				Webhook: &apiextensionsv1.WebhookConversion{
					ClientConfig: &apiextensionsv1.WebhookClientConfig{
						CABundle: caBundle,
						Service: &apiextensionsv1.ServiceReference{
							Namespace: svcNamespace,
							Name:      svcName,
							Port:      ptr.To[int32](443),
							Path:      ptr.To("/convert"),
						},
					},
					ConversionReviewVersions: []string{"v1beta1", "v1beta2"},
				},
			},
		},
	}
}

// countPatches returns interceptor funcs that count Patch calls before forwarding them.
func countPatches(count *int) interceptor.Funcs {
	return interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			*count++
			return c.Patch(ctx, obj, patch, opts...)
		},
	}
}

func getCRD(t *testing.T, cl client.Client, name string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	crd := &apiextensionsv1.CustomResourceDefinition{}
	NewWithT(t).Expect(cl.Get(t.Context(), client.ObjectKey{Name: name}, crd)).To(Succeed())
	return crd
}

// mutateCRDBeforeFirstPatch returns interceptor funcs that, before forwarding the first CRD patch,
// simulate another writer changing the CRD after it was listed. patchCalls counts every CRD patch attempt.
func mutateCRDBeforeFirstPatch(patchCalls *int, mutate func(*apiextensionsv1.CustomResourceDefinition)) interceptor.Funcs {
	return interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if _, ok := obj.(*apiextensionsv1.CustomResourceDefinition); !ok {
				return c.Patch(ctx, obj, patch, opts...)
			}
			*patchCalls++
			if *patchCalls == 1 {
				latest := &apiextensionsv1.CustomResourceDefinition{}
				if err := c.Get(ctx, client.ObjectKeyFromObject(obj), latest); err != nil {
					return err
				}
				mutate(latest)
				if err := c.Update(ctx, latest); err != nil {
					return err
				}
			}
			return c.Patch(ctx, obj, patch, opts...)
		},
	}
}

// retargetToCAPIWebhookService retargets the CRD's conversion webhook to the CAPI webhook Service.
func retargetToCAPIWebhookService(crd *apiextensionsv1.CustomResourceDefinition) {
	crd.Spec.Conversion.Webhook.ClientConfig.Service.Namespace = "capi-system"
	crd.Spec.Conversion.Webhook.ClientConfig.Service.Name = "capi-webhook-service"
}

// expectRetargetedCRDUntouched asserts that a CRD retargeted to the CAPI webhook Service kept its
// annotation and caBundle.
func expectRetargetedCRDUntouched(t *testing.T, cl client.Client, name string, caBundle []byte) {
	t.Helper()
	g := NewWithT(t)
	crd := getCRD(t, cl, name)
	g.Expect(crd.Spec.Conversion.Webhook.ClientConfig.Service.Namespace).To(Equal("capi-system"))
	g.Expect(crd.Spec.Conversion.Webhook.ClientConfig.Service.Name).To(Equal("capi-webhook-service"))
	g.Expect(crd.Annotations).To(HaveKeyWithValue(injectCABundleAnnotation, "true"))
	g.Expect(crd.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(caBundle))
}

// newStaleWebhookConfigs returns the operator's mutating and validating webhook configurations with a stale caBundle.
func newStaleWebhookConfigs() (*admissionregistrationv1.MutatingWebhookConfiguration, *admissionregistrationv1.ValidatingWebhookConfiguration) {
	mwc := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: webhookConfigName},
		Webhooks: []admissionregistrationv1.MutatingWebhook{{
			Name:                    "defaulting.hypershift.openshift.io",
			ClientConfig:            admissionregistrationv1.WebhookClientConfig{CABundle: []byte("stale")},
			SideEffects:             sideEffectNone(),
			AdmissionReviewVersions: []string{"v1"},
		}},
	}
	vwc := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: webhookConfigName},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{{
			Name:                    "validating.hypershift.openshift.io",
			ClientConfig:            admissionregistrationv1.WebhookClientConfig{CABundle: []byte("stale")},
			SideEffects:             sideEffectNone(),
			AdmissionReviewVersions: []string{"v1"},
		}},
	}
	return mwc, vwc
}

// expectWebhookConfigsCABundle asserts the caBundle of the operator's mutating and validating webhook configurations.
func expectWebhookConfigsCABundle(t *testing.T, cl client.Client, caBundle []byte) {
	t.Helper()
	g := NewWithT(t)
	mwc := &admissionregistrationv1.MutatingWebhookConfiguration{}
	g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: webhookConfigName}, mwc)).To(Succeed())
	g.Expect(mwc.Webhooks).To(HaveLen(1))
	g.Expect(mwc.Webhooks[0].ClientConfig.CABundle).To(Equal(caBundle))
	vwc := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: webhookConfigName}, vwc)).To(Succeed())
	g.Expect(vwc.Webhooks).To(HaveLen(1))
	g.Expect(vwc.Webhooks[0].ClientConfig.CABundle).To(Equal(caBundle))
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

		// Pre-create valid secrets via GenerateInitialWebhookCerts.
		caSecret, servingSecret, _, err := GenerateInitialWebhookCerts("hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, servingSecret).Build()
		r := newReconciler(cl)

		result, err := r.Reconcile(t.Context(), caRequest())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.RequeueAfter).To(Equal(12 * time.Hour))
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

	t.Run("When a CRD targeting the operator service has the inject-cabundle annotation and a foreign caBundle, it should remove the annotation and set the webhook CA bundle", func(t *testing.T) {
		g := NewWithT(t)

		_, foreignCA := testCABundles(t)
		crd := newConversionCRD("clusters", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd).Build()
		r := newReconciler(cl)

		_, err := r.Reconcile(t.Context(), caRequest())
		g.Expect(err).ToNot(HaveOccurred())

		caSecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: CASecretName, Namespace: "hypershift"}, caSecret)).To(Succeed())
		updated := getCRD(t, cl, crd.Name)
		g.Expect(updated.Annotations).ToNot(HaveKey(injectCABundleAnnotation))
		g.Expect(updated.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(caSecret.Data[certs.CASignerCertMapKey]))
	})

	t.Run("When a managed CRD caBundle drifts after a reconcile, it should be restored by the reconcile triggered from the CRD watch", func(t *testing.T) {
		g := NewWithT(t)

		_, foreignCA := testCABundles(t)
		crd := newConversionCRD("machines", "hypershift", "operator", nil, nil)
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd).Build()
		r := newReconciler(cl)

		_, err := r.Reconcile(t.Context(), caRequest())
		g.Expect(err).ToNot(HaveOccurred())
		caSecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: CASecretName, Namespace: "hypershift"}, caSecret)).To(Succeed())
		webhookCA := caSecret.Data[certs.CASignerCertMapKey]
		g.Expect(getCRD(t, cl, crd.Name).Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(webhookCA))

		// Simulate service-ca re-injecting its own bundle after the annotation is re-added.
		drifted := getCRD(t, cl, crd.Name)
		drifted.Annotations = map[string]string{injectCABundleAnnotation: "true"}
		drifted.Spec.Conversion.Webhook.ClientConfig.CABundle = foreignCA
		g.Expect(cl.Update(t.Context(), drifted)).To(Succeed())

		// The CRD watch selects the drifted CRD and maps it to the reconcile key.
		g.Expect(r.operatorConversionCRDPredicate().Update(event.UpdateEvent{ObjectOld: crd, ObjectNew: drifted})).To(BeTrue())
		requests := r.mapCRDToReconcileRequests(t.Context(), drifted)
		g.Expect(requests).To(HaveLen(1))

		_, err = r.Reconcile(t.Context(), requests[0])
		g.Expect(err).ToNot(HaveOccurred())
		restored := getCRD(t, cl, crd.Name)
		g.Expect(restored.Annotations).ToNot(HaveKey(injectCABundleAnnotation))
		g.Expect(restored.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(webhookCA))
	})

	t.Run("When a CRD is retargeted to another service after it was listed, it should return a conflict and leave the CRD untouched", func(t *testing.T) {
		g := NewWithT(t)

		_, foreignCA := testCABundles(t)
		crd := newConversionCRD("machines", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		patchCalls := 0
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd).WithInterceptorFuncs(mutateCRDBeforeFirstPatch(&patchCalls, retargetToCAPIWebhookService)).Build()
		r := newReconciler(cl)

		_, err := r.Reconcile(t.Context(), caRequest())
		g.Expect(err).To(HaveOccurred())
		g.Expect(apierrors.IsConflict(err)).To(BeTrue(), "expected a conflict so the reconcile is retried, got: %v", err)
		expectRetargetedCRDUntouched(t, cl, crd.Name, foreignCA)

		// The retried reconcile lists the CRD again, no longer selects it, and leaves it untouched.
		_, err = r.Reconcile(t.Context(), caRequest())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(patchCalls).To(Equal(1))
		expectRetargetedCRDUntouched(t, cl, crd.Name, foreignCA)
	})

	t.Run("When a CRD patch conflicts, it should still update the webhook configuration caBundles and return the conflict", func(t *testing.T) {
		g := NewWithT(t)

		_, foreignCA := testCABundles(t)
		crd := newConversionCRD("machinesets", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		mwc, vwc := newStaleWebhookConfigs()
		patchCalls := 0
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd, mwc, vwc).WithInterceptorFuncs(mutateCRDBeforeFirstPatch(&patchCalls, retargetToCAPIWebhookService)).Build()
		r := newReconciler(cl)

		_, err := r.Reconcile(t.Context(), caRequest())
		g.Expect(apierrors.IsConflict(err)).To(BeTrue(), "expected a conflict so the reconcile is retried, got: %v", err)

		caSecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: CASecretName, Namespace: "hypershift"}, caSecret)).To(Succeed())
		expectWebhookConfigsCABundle(t, cl, caSecret.Data[certs.CASignerCertMapKey])
		expectRetargetedCRDUntouched(t, cl, crd.Name, foreignCA)
	})

	t.Run("When a CRD patch fails with a non-conflict error, it should return the error without updating the webhook configurations", func(t *testing.T) {
		g := NewWithT(t)

		_, foreignCA := testCABundles(t)
		crd := newConversionCRD("machinesets", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		mwc, vwc := newStaleWebhookConfigs()
		forbidden := apierrors.NewForbidden(apiextensionsv1.Resource("customresourcedefinitions"), crd.Name, errors.New("patch denied"))
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd, mwc, vwc).WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if _, ok := obj.(*apiextensionsv1.CustomResourceDefinition); ok {
					return forbidden
				}
				return c.Patch(ctx, obj, patch, opts...)
			},
		}).Build()
		r := newReconciler(cl)

		_, err := r.Reconcile(t.Context(), caRequest())
		g.Expect(errors.Is(err, forbidden)).To(BeTrue(), "expected the CRD patch error, got: %v", err)
		g.Expect(apierrors.IsForbidden(err)).To(BeTrue())
		expectWebhookConfigsCABundle(t, cl, []byte("stale"))
	})

	t.Run("When a CRD patch conflicts and the webhook configuration update fails, it should return both errors", func(t *testing.T) {
		g := NewWithT(t)

		_, foreignCA := testCABundles(t)
		crd := newConversionCRD("machinesets", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		mwc, vwc := newStaleWebhookConfigs()
		conflict := apierrors.NewConflict(apiextensionsv1.Resource("customresourcedefinitions"), crd.Name, errors.New("the object has been modified"))
		updateErr := apierrors.NewForbidden(admissionregistrationv1.Resource("mutatingwebhookconfigurations"), webhookConfigName, errors.New("update denied"))
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd, mwc, vwc).WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if _, ok := obj.(*apiextensionsv1.CustomResourceDefinition); ok {
					return conflict
				}
				return c.Patch(ctx, obj, patch, opts...)
			},
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if _, ok := obj.(*admissionregistrationv1.MutatingWebhookConfiguration); ok {
					return updateErr
				}
				return c.Update(ctx, obj, opts...)
			},
		}).Build()
		r := newReconciler(cl)

		_, err := r.Reconcile(t.Context(), caRequest())
		g.Expect(errors.Is(err, conflict)).To(BeTrue(), "expected the CRD conflict to be preserved, got: %v", err)
		g.Expect(errors.Is(err, updateErr)).To(BeTrue(), "expected the webhook configuration error to be preserved, got: %v", err)
		g.Expect(apierrors.IsConflict(err)).To(BeTrue())
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

	t.Run("When CA exists but serving cert is missing it should regenerate both secrets", func(t *testing.T) {
		g := NewWithT(t)

		caSecret, _, _, err := GenerateInitialWebhookCerts("hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret).Build()

		err = EnsureWebhookCerts(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		// Both secrets should exist with valid data.
		updatedCASecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: CASecretName, Namespace: "hypershift"}, updatedCASecret)).To(Succeed())
		g.Expect(updatedCASecret.Data[certs.CASignerCertMapKey]).ToNot(BeEmpty())
		g.Expect(updatedCASecret.Data[certs.CASignerKeyMapKey]).ToNot(BeEmpty())

		servingSecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: ServingCertSecretName, Namespace: "hypershift"}, servingSecret)).To(Succeed())
		g.Expect(servingSecret.Data[corev1.TLSCertKey]).ToNot(BeEmpty())
		g.Expect(servingSecret.Data[corev1.TLSPrivateKeyKey]).ToNot(BeEmpty())

		// Verify the serving cert was signed by the (possibly replaced) CA.
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

func TestPatchCRDConversionWebhookCABundles(t *testing.T) {
	t.Run("When a CRD has a conversion webhook it should patch the caBundle", func(t *testing.T) {
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

		newCA := []byte("new-ca-bundle")
		err := patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", newCA)
		g.Expect(err).ToNot(HaveOccurred())

		updatedCRD := &apiextensionsv1.CustomResourceDefinition{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: crd.Name}, updatedCRD)).To(Succeed())
		g.Expect(updatedCRD.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(newCA))
	})

	t.Run("When CRD already has the correct caBundle it should not patch", func(t *testing.T) {
		g := NewWithT(t)

		correctCA := []byte("correct-ca")
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
							CABundle: correctCA,
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

		err := patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", correctCA)
		g.Expect(err).ToNot(HaveOccurred())

		updatedCRD := &apiextensionsv1.CustomResourceDefinition{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: crd.Name}, updatedCRD)).To(Succeed())
		g.Expect(updatedCRD.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(correctCA))
	})

	t.Run("When CRD points to different service it should not patch", func(t *testing.T) {
		g := NewWithT(t)

		originalCA := []byte("original-ca")
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
							CABundle: originalCA,
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

		err := patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", []byte("new-ca"))
		g.Expect(err).ToNot(HaveOccurred())

		updatedCRD := &apiextensionsv1.CustomResourceDefinition{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: crd.Name}, updatedCRD)).To(Succeed())
		g.Expect(updatedCRD.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(originalCA))
	})

	t.Run("When an operator-service CRD has the inject-cabundle annotation and a stale caBundle, it should remove only that annotation and set the caBundle in a single patch", func(t *testing.T) {
		g := NewWithT(t)

		webhookCA, foreignCA := testCABundles(t)
		crd := newConversionCRD("clusters", "hypershift", "operator", foreignCA, map[string]string{
			injectCABundleAnnotation:                "true",
			"controller-gen.kubebuilder.io/version": "v0.19.0",
		})
		patches := 0
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd).WithInterceptorFuncs(countPatches(&patches)).Build()

		g.Expect(patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", webhookCA)).To(Succeed())

		g.Expect(patches).To(Equal(1))
		updated := getCRD(t, cl, crd.Name)
		g.Expect(updated.Annotations).ToNot(HaveKey(injectCABundleAnnotation))
		g.Expect(updated.Annotations).To(HaveKeyWithValue("controller-gen.kubebuilder.io/version", "v0.19.0"))
		g.Expect(updated.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(webhookCA))
	})

	t.Run("When an operator-service CRD has the inject-cabundle annotation and the correct caBundle, it should still remove the annotation", func(t *testing.T) {
		g := NewWithT(t)

		webhookCA, _ := testCABundles(t)
		crd := newConversionCRD("machinesets", "hypershift", "operator", webhookCA, map[string]string{injectCABundleAnnotation: "true"})
		patches := 0
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd).WithInterceptorFuncs(countPatches(&patches)).Build()

		g.Expect(patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", webhookCA)).To(Succeed())

		g.Expect(patches).To(Equal(1))
		updated := getCRD(t, cl, crd.Name)
		g.Expect(updated.Annotations).ToNot(HaveKey(injectCABundleAnnotation))
		g.Expect(updated.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(webhookCA))
	})

	t.Run("When the inject-cabundle annotation is the only annotation, it should send a merge patch that deletes only that key", func(t *testing.T) {
		g := NewWithT(t)

		webhookCA, foreignCA := testCABundles(t)
		crd := newConversionCRD("machinepools", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		var listedResourceVersion string
		var patchType types.PatchType
		var patchData []byte
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd).WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				listedResourceVersion = obj.GetResourceVersion()
				patchType = patch.Type()
				data, err := patch.Data(obj)
				if err != nil {
					return err
				}
				patchData = data
				return c.Patch(ctx, obj, patch, opts...)
			},
		}).Build()

		g.Expect(patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", webhookCA)).To(Succeed())

		g.Expect(patchType).To(Equal(types.MergePatchType))
		g.Expect(listedResourceVersion).ToNot(BeEmpty())
		g.Expect(string(patchData)).To(MatchJSON(fmt.Sprintf(
			`{"metadata":{"annotations":{%q:null},"resourceVersion":%q},"spec":{"conversion":{"webhook":{"clientConfig":{"caBundle":%q}}}}}`,
			injectCABundleAnnotation, listedResourceVersion, base64.StdEncoding.EncodeToString(webhookCA))))
		updated := getCRD(t, cl, crd.Name)
		g.Expect(updated.Annotations).To(BeEmpty())
		g.Expect(updated.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(webhookCA))
	})

	t.Run("When another writer adds an unrelated annotation before the patch is applied, it should conflict and preserve that annotation when the CRD is repaired", func(t *testing.T) {
		g := NewWithT(t)

		webhookCA, foreignCA := testCABundles(t)
		crd := newConversionCRD("machinedeployments", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		patchCalls := 0
		// Another writer adds an annotation after the CRD was listed but before our patch is applied.
		addAnnotation := func(crd *apiextensionsv1.CustomResourceDefinition) {
			crd.Annotations["example.com/added-concurrently"] = "true"
		}
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd).WithInterceptorFuncs(mutateCRDBeforeFirstPatch(&patchCalls, addAnnotation)).Build()

		// The stale patch is rejected and changes nothing.
		err := patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", webhookCA)
		g.Expect(patchCalls).To(Equal(1))
		g.Expect(apierrors.IsConflict(err)).To(BeTrue(), "expected a conflict, got: %v", err)
		unchanged := getCRD(t, cl, crd.Name)
		g.Expect(unchanged.Annotations).To(Equal(map[string]string{injectCABundleAnnotation: "true", "example.com/added-concurrently": "true"}))
		g.Expect(unchanged.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(foreignCA))

		// The next pass lists the CRD again, removes only the inject-cabundle annotation and repairs the caBundle.
		g.Expect(patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", webhookCA)).To(Succeed())
		g.Expect(patchCalls).To(Equal(2))
		updated := getCRD(t, cl, crd.Name)
		g.Expect(updated.Annotations).To(Equal(map[string]string{"example.com/added-concurrently": "true"}))
		g.Expect(updated.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(webhookCA))
	})

	t.Run("When a CRD conversion webhook targets a different service, it should not modify the CRD", func(t *testing.T) {
		webhookCA, foreignCA := testCABundles(t)
		for _, tc := range []struct {
			name         string
			svcNamespace string
			svcName      string
		}{
			{name: "When only the service namespace differs, it should not patch", svcNamespace: "capi-system", svcName: "operator"},
			{name: "When only the service name differs, it should not patch", svcNamespace: "hypershift", svcName: "capi-webhook-service"},
			{name: "When the CRD targets the CAPI webhook service, it should not patch", svcNamespace: "capi-system", svcName: "capi-webhook-service"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				g := NewWithT(t)

				crd := newConversionCRD("clusterclasses", tc.svcNamespace, tc.svcName, foreignCA, map[string]string{injectCABundleAnnotation: "true"})
				patches := 0
				cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd).WithInterceptorFuncs(countPatches(&patches)).Build()
				before := getCRD(t, cl, crd.Name)

				g.Expect(patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", webhookCA)).To(Succeed())

				g.Expect(patches).To(Equal(0))
				after := getCRD(t, cl, crd.Name)
				g.Expect(after.ResourceVersion).To(Equal(before.ResourceVersion))
				g.Expect(after.Annotations).To(HaveKeyWithValue(injectCABundleAnnotation, "true"))
				g.Expect(after.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(foreignCA))
			})
		}
	})

	t.Run("When called twice, it should not patch on the second call", func(t *testing.T) {
		g := NewWithT(t)

		webhookCA, foreignCA := testCABundles(t)
		crd := newConversionCRD("machinehealthchecks", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		patches := 0
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd).WithInterceptorFuncs(countPatches(&patches)).Build()

		g.Expect(patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", webhookCA)).To(Succeed())
		g.Expect(patches).To(Equal(1))
		afterFirst := getCRD(t, cl, crd.Name)

		g.Expect(patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", webhookCA)).To(Succeed())
		g.Expect(patches).To(Equal(1))
		afterSecond := getCRD(t, cl, crd.Name)
		g.Expect(afterSecond.ResourceVersion).To(Equal(afterFirst.ResourceVersion))
		g.Expect(afterSecond.Annotations).ToNot(HaveKey(injectCABundleAnnotation))
		g.Expect(afterSecond.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(webhookCA))
	})

	t.Run("When the annotation is absent and the caBundle matches, it should not patch", func(t *testing.T) {
		g := NewWithT(t)

		webhookCA, _ := testCABundles(t)
		crd := newConversionCRD("machinedrainrules", "hypershift", "operator", webhookCA, map[string]string{"controller-gen.kubebuilder.io/version": "v0.19.0"})
		patches := 0
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd).WithInterceptorFuncs(countPatches(&patches)).Build()

		g.Expect(patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", webhookCA)).To(Succeed())
		g.Expect(patches).To(Equal(0))
	})

	t.Run("When a CRD has no conversion webhook service reference, it should skip it", func(t *testing.T) {
		webhookCA, _ := testCABundles(t)
		for _, tc := range []struct {
			name   string
			mutate func(*apiextensionsv1.CustomResourceDefinition)
		}{
			{name: "When conversion is nil, it should skip the CRD", mutate: func(crd *apiextensionsv1.CustomResourceDefinition) {
				crd.Spec.Conversion = nil
			}},
			{name: "When the conversion webhook is nil, it should skip the CRD", mutate: func(crd *apiextensionsv1.CustomResourceDefinition) {
				crd.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{Strategy: apiextensionsv1.NoneConverter}
			}},
			{name: "When the webhook clientConfig is nil, it should skip the CRD", mutate: func(crd *apiextensionsv1.CustomResourceDefinition) {
				crd.Spec.Conversion.Webhook.ClientConfig = nil
			}},
			{name: "When the webhook clientConfig uses a URL, it should skip the CRD", mutate: func(crd *apiextensionsv1.CustomResourceDefinition) {
				crd.Spec.Conversion.Webhook.ClientConfig.Service = nil
				crd.Spec.Conversion.Webhook.ClientConfig.URL = ptr.To("https://operator.hypershift.svc:443/convert")
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				g := NewWithT(t)

				crd := newConversionCRD("clusters", "hypershift", "operator", nil, map[string]string{injectCABundleAnnotation: "true"})
				tc.mutate(crd)
				patches := 0
				cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd).WithInterceptorFuncs(countPatches(&patches)).Build()

				g.Expect(patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", webhookCA)).To(Succeed())
				g.Expect(patches).To(Equal(0))
				g.Expect(getCRD(t, cl, crd.Name).Annotations).To(HaveKeyWithValue(injectCABundleAnnotation, "true"))
			})
		}
	})

	t.Run("When a CRD is retargeted to another service after it was listed, it should return a conflict and leave the CRD untouched", func(t *testing.T) {
		webhookCA, foreignCA := testCABundles(t)
		for _, tc := range []struct {
			name     string
			caBundle []byte
		}{
			{name: "When the caBundle already matches and only the annotation would be removed, it should conflict", caBundle: webhookCA},
			{name: "When the caBundle differs, it should conflict", caBundle: foreignCA},
		} {
			t.Run(tc.name, func(t *testing.T) {
				g := NewWithT(t)

				crd := newConversionCRD("clusters", "hypershift", "operator", tc.caBundle, map[string]string{injectCABundleAnnotation: "true"})
				patchCalls := 0
				cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd).WithInterceptorFuncs(mutateCRDBeforeFirstPatch(&patchCalls, retargetToCAPIWebhookService)).Build()

				err := patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", webhookCA)
				g.Expect(err).To(HaveOccurred())
				g.Expect(apierrors.IsConflict(err)).To(BeTrue(), "expected a conflict, got: %v", err)
				expectRetargetedCRDUntouched(t, cl, crd.Name, tc.caBundle)

				// The next pass lists the CRD again, no longer selects it, and leaves it untouched.
				g.Expect(patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", webhookCA)).To(Succeed())
				g.Expect(patchCalls).To(Equal(1))
				expectRetargetedCRDUntouched(t, cl, crd.Name, tc.caBundle)
			})
		}
	})

	t.Run("When one CRD conflicts, it should still repair the other CRDs in the same pass and return the conflict", func(t *testing.T) {
		g := NewWithT(t)

		webhookCA, foreignCA := testCABundles(t)
		conflicting := newConversionCRD("clusters", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		other := newConversionCRD("machines", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		patchCalls := 0
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(conflicting, other).WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				patchCalls++
				if obj.GetName() == conflicting.Name {
					return apierrors.NewConflict(apiextensionsv1.Resource("customresourcedefinitions"), obj.GetName(), errors.New("the object has been modified"))
				}
				return c.Patch(ctx, obj, patch, opts...)
			},
		}).Build()

		err := patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", webhookCA)
		g.Expect(apierrors.IsConflict(err)).To(BeTrue(), "expected a conflict, got: %v", err)
		g.Expect(err.Error()).To(ContainSubstring(conflicting.Name))
		g.Expect(patchCalls).To(Equal(2))
		repaired := getCRD(t, cl, other.Name)
		g.Expect(repaired.Annotations).ToNot(HaveKey(injectCABundleAnnotation))
		g.Expect(repaired.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(webhookCA))
		notPatched := getCRD(t, cl, conflicting.Name)
		g.Expect(notPatched.Annotations).To(HaveKeyWithValue(injectCABundleAnnotation, "true"))
		g.Expect(notPatched.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(foreignCA))
	})

	t.Run("When the caBundle is empty, it should return an error without patching any CRD", func(t *testing.T) {
		g := NewWithT(t)

		_, foreignCA := testCABundles(t)
		crd := newConversionCRD("clusters", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		patches := 0
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd).WithInterceptorFuncs(countPatches(&patches)).Build()

		g.Expect(patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", nil)).ToNot(Succeed())
		g.Expect(patches).To(Equal(0))
		after := getCRD(t, cl, crd.Name)
		g.Expect(after.Annotations).To(HaveKeyWithValue(injectCABundleAnnotation, "true"))
		g.Expect(after.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(foreignCA))
	})

	t.Run("When patching a CRD fails, it should return a wrapped error naming the CRD", func(t *testing.T) {
		g := NewWithT(t)

		webhookCA, foreignCA := testCABundles(t)
		crd := newConversionCRD("clusters", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		patchErr := errors.New("injected patch failure")
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(crd).WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				return patchErr
			},
		}).Build()

		err := patchCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator", webhookCA)
		g.Expect(err).To(MatchError(patchErr))
		g.Expect(err.Error()).To(ContainSubstring("clusters.cluster.x-k8s.io"))
	})
}

func TestBuildCRDConversionCABundlePatch(t *testing.T) {
	t.Run("When the inject-cabundle annotation must be removed, it should delete only that key and set the caBundle", func(t *testing.T) {
		g := NewWithT(t)

		data, err := buildCRDConversionCABundlePatch([]byte("ca-bundle"), "12345", true)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(string(data)).To(MatchJSON(fmt.Sprintf(
			`{"metadata":{"annotations":{%q:null},"resourceVersion":"12345"},"spec":{"conversion":{"webhook":{"clientConfig":{"caBundle":%q}}}}}`,
			injectCABundleAnnotation, base64.StdEncoding.EncodeToString([]byte("ca-bundle")))))
	})

	t.Run("When the inject-cabundle annotation is absent, it should only set the caBundle with the resourceVersion precondition", func(t *testing.T) {
		g := NewWithT(t)

		data, err := buildCRDConversionCABundlePatch([]byte("ca-bundle"), "12345", false)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(string(data)).To(MatchJSON(fmt.Sprintf(
			`{"metadata":{"resourceVersion":"12345"},"spec":{"conversion":{"webhook":{"clientConfig":{"caBundle":%q}}}}}`,
			base64.StdEncoding.EncodeToString([]byte("ca-bundle")))))
	})
}

func TestConversionWebhookTargetsService(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mutate   func(*apiextensionsv1.CustomResourceDefinition)
		expected bool
	}{
		{name: "When the conversion webhook targets the operator service, it should return true", mutate: func(*apiextensionsv1.CustomResourceDefinition) {}, expected: true},
		{name: "When the service namespace differs, it should return false", mutate: func(crd *apiextensionsv1.CustomResourceDefinition) {
			crd.Spec.Conversion.Webhook.ClientConfig.Service.Namespace = "capi-system"
		}},
		{name: "When the service name differs, it should return false", mutate: func(crd *apiextensionsv1.CustomResourceDefinition) {
			crd.Spec.Conversion.Webhook.ClientConfig.Service.Name = "capi-webhook-service"
		}},
		{name: "When conversion is nil, it should return false", mutate: func(crd *apiextensionsv1.CustomResourceDefinition) {
			crd.Spec.Conversion = nil
		}},
		{name: "When the conversion webhook is nil, it should return false", mutate: func(crd *apiextensionsv1.CustomResourceDefinition) {
			crd.Spec.Conversion.Webhook = nil
		}},
		{name: "When the webhook clientConfig is nil, it should return false", mutate: func(crd *apiextensionsv1.CustomResourceDefinition) {
			crd.Spec.Conversion.Webhook.ClientConfig = nil
		}},
		{name: "When the webhook clientConfig uses a URL, it should return false", mutate: func(crd *apiextensionsv1.CustomResourceDefinition) {
			crd.Spec.Conversion.Webhook.ClientConfig.Service = nil
			crd.Spec.Conversion.Webhook.ClientConfig.URL = ptr.To("https://operator.hypershift.svc:443/convert")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			crd := newConversionCRD("clusters", "hypershift", "operator", nil, nil)
			tc.mutate(crd)
			g.Expect(conversionWebhookTargetsService(crd, "hypershift", "operator")).To(Equal(tc.expected))
		})
	}
}

func TestWebhookCertReconciler_OperatorConversionCRDPredicate(t *testing.T) {
	r := &WebhookCertReconciler{Namespace: "hypershift", ServiceName: "operator"}
	for _, tc := range []struct {
		name     string
		obj      client.Object
		expected bool
	}{
		{name: "When a CRD conversion webhook targets the operator service, it should select it",
			obj: newConversionCRD("clusters", "hypershift", "operator", nil, map[string]string{injectCABundleAnnotation: "true"}), expected: true},
		{name: "When a CRD conversion webhook targets a service in another namespace, it should not select it",
			obj: newConversionCRD("clusters", "capi-system", "operator", nil, nil)},
		{name: "When a CRD conversion webhook targets another service, it should not select it",
			obj: newConversionCRD("clusters", "capi-system", "capi-webhook-service", nil, nil)},
		{name: "When a CRD has no conversion webhook, it should not select it",
			obj: &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "hostedclusters.hypershift.openshift.io"}}},
		{name: "When the object is not a CRD, it should not select it",
			obj: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: CASecretName, Namespace: "hypershift"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			p := r.operatorConversionCRDPredicate()
			g.Expect(p.Create(event.CreateEvent{Object: tc.obj})).To(Equal(tc.expected))
			g.Expect(p.Update(event.UpdateEvent{ObjectOld: tc.obj, ObjectNew: tc.obj})).To(Equal(tc.expected))
			g.Expect(p.Delete(event.DeleteEvent{Object: tc.obj})).To(Equal(tc.expected))
			g.Expect(p.Generic(event.GenericEvent{Object: tc.obj})).To(Equal(tc.expected))
		})
	}
}

func TestWebhookCertReconciler_MapCRDToReconcileRequests(t *testing.T) {
	t.Run("When a CRD event is mapped, it should enqueue only the webhook CA secret key", func(t *testing.T) {
		g := NewWithT(t)

		r := &WebhookCertReconciler{Namespace: "hypershift", ServiceName: "operator"}
		crd := newConversionCRD("clusters", "hypershift", "operator", nil, nil)
		g.Expect(r.mapCRDToReconcileRequests(t.Context(), crd)).To(Equal([]ctrl.Request{caRequest()}))
	})
}

func TestEnsureLegacyServiceCAMigration(t *testing.T) {
	t.Run("When service has service-ca annotations it should remove them", func(t *testing.T) {
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

		err := EnsureLegacyServiceCAMigration(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		updatedSvc := &corev1.Service{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: "operator", Namespace: "hypershift"}, updatedSvc)).To(Succeed())
		g.Expect(updatedSvc.Annotations).ToNot(HaveKey("service.beta.openshift.io/serving-cert-secret-name"))
	})

	t.Run("When service-ca managed serving cert exists it should delete it", func(t *testing.T) {
		g := NewWithT(t)

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

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(serviceCACert).Build()

		err := EnsureLegacyServiceCAMigration(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		// Serving cert should be deleted.
		servingSecret := &corev1.Secret{}
		err = cl.Get(t.Context(), client.ObjectKey{Name: ServingCertSecretName, Namespace: "hypershift"}, servingSecret)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("not found"))
	})

	t.Run("When self-managed serving cert exists it should not delete it", func(t *testing.T) {
		g := NewWithT(t)

		_, servingSecret, _, err := GenerateInitialWebhookCerts("hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())
		originalCert := servingSecret.Data[corev1.TLSCertKey]

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(servingSecret).Build()

		err = EnsureLegacyServiceCAMigration(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		// Self-managed cert should still exist unchanged.
		updatedSecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: ServingCertSecretName, Namespace: "hypershift"}, updatedSecret)).To(Succeed())
		g.Expect(updatedSecret.Data[corev1.TLSCertKey]).To(Equal(originalCert))
	})

	t.Run("When webhook configurations have inject-cabundle annotation it should remove them", func(t *testing.T) {
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

		err := EnsureLegacyServiceCAMigration(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		updatedMWC := &admissionregistrationv1.MutatingWebhookConfiguration{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: webhookConfigName}, updatedMWC)).To(Succeed())
		g.Expect(updatedMWC.Annotations).ToNot(HaveKey(injectCABundleAnnotation))

		updatedVWC := &admissionregistrationv1.ValidatingWebhookConfiguration{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: webhookConfigName}, updatedVWC)).To(Succeed())
		g.Expect(updatedVWC.Annotations).ToNot(HaveKey(injectCABundleAnnotation))
	})

	t.Run("When no service-ca resources exist it should not error", func(t *testing.T) {
		g := NewWithT(t)

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()

		err := EnsureLegacyServiceCAMigration(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())
	})
}

func TestEnsureCRDConversionWebhookCABundles(t *testing.T) {
	t.Run("When CA secret exists with valid data it should patch CRD caBundles", func(t *testing.T) {
		g := NewWithT(t)

		caSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      CASecretName,
				Namespace: "hypershift",
			},
			Data: map[string][]byte{
				certs.CASignerCertMapKey: []byte("valid-ca-bundle"),
			},
		}

		crd := &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: "machines.cluster.x-k8s.io"},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: "cluster.x-k8s.io",
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural:   "machines",
					Singular: "machine",
					Kind:     "Machine",
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

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, crd).Build()

		err := EnsureCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		updatedCRD := &apiextensionsv1.CustomResourceDefinition{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: crd.Name}, updatedCRD)).To(Succeed())
		g.Expect(updatedCRD.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal([]byte("valid-ca-bundle")))
	})

	t.Run("When CA secret does not exist it should return an error", func(t *testing.T) {
		g := NewWithT(t)

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()

		err := EnsureCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to get webhook CA secret"))
	})

	t.Run("When CA secret exists but caBundle is empty it should return an error", func(t *testing.T) {
		g := NewWithT(t)

		caSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      CASecretName,
				Namespace: "hypershift",
			},
			Data: map[string][]byte{},
		}

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret).Build()

		err := EnsureCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("empty caBundle"))
	})

	t.Run("When called multiple times it should be idempotent", func(t *testing.T) {
		g := NewWithT(t)

		caSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      CASecretName,
				Namespace: "hypershift",
			},
			Data: map[string][]byte{
				certs.CASignerCertMapKey: []byte("correct-ca-bundle"),
			},
		}

		crd := &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: "machinesets.cluster.x-k8s.io"},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: "cluster.x-k8s.io",
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural:   "machinesets",
					Singular: "machineset",
					Kind:     "MachineSet",
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

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, crd).Build()

		// First call should patch the CRD
		err := EnsureCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		// Second call should succeed without error (idempotent)
		err = EnsureCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		// Verify caBundle is still correct
		updatedCRD := &apiextensionsv1.CustomResourceDefinition{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: crd.Name}, updatedCRD)).To(Succeed())
		g.Expect(updatedCRD.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal([]byte("correct-ca-bundle")))
	})

	t.Run("When CRD points to different service it should not patch that CRD", func(t *testing.T) {
		g := NewWithT(t)

		caSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      CASecretName,
				Namespace: "hypershift",
			},
			Data: map[string][]byte{
				certs.CASignerCertMapKey: []byte("hypershift-ca-bundle"),
			},
		}

		// CRD for our webhook
		ourCRD := &apiextensionsv1.CustomResourceDefinition{
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

		// CRD for a different webhook service
		otherCRD := &apiextensionsv1.CustomResourceDefinition{
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
							CABundle: []byte("other-ca"),
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

		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, ourCRD, otherCRD).Build()

		err := EnsureCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).ToNot(HaveOccurred())

		// Our CRD should be patched
		updatedOurCRD := &apiextensionsv1.CustomResourceDefinition{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: ourCRD.Name}, updatedOurCRD)).To(Succeed())
		g.Expect(updatedOurCRD.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal([]byte("hypershift-ca-bundle")))

		// Other CRD should NOT be patched
		updatedOtherCRD := &apiextensionsv1.CustomResourceDefinition{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Name: otherCRD.Name}, updatedOtherCRD)).To(Succeed())
		g.Expect(updatedOtherCRD.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal([]byte("other-ca")))
	})

	t.Run("When a CRD has the inject-cabundle annotation and a foreign caBundle, it should remove the annotation and set the webhook CA bundle", func(t *testing.T) {
		g := NewWithT(t)

		webhookCA, foreignCA := testCABundles(t)
		caSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: CASecretName, Namespace: "hypershift"},
			Data:       map[string][]byte{certs.CASignerCertMapKey: webhookCA},
		}
		crd := newConversionCRD("clusters", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, crd).Build()

		g.Expect(EnsureCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator")).To(Succeed())

		updated := getCRD(t, cl, crd.Name)
		g.Expect(updated.Annotations).ToNot(HaveKey(injectCABundleAnnotation))
		g.Expect(updated.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(webhookCA))
	})

	t.Run("When a CRD is retargeted to another service after it was listed, it should retry with a fresh list and leave the CRD untouched", func(t *testing.T) {
		g := NewWithT(t)

		webhookCA, foreignCA := testCABundles(t)
		caSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: CASecretName, Namespace: "hypershift"},
			Data:       map[string][]byte{certs.CASignerCertMapKey: webhookCA},
		}
		crd := newConversionCRD("clusters", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		patchCalls := 0
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, crd).WithInterceptorFuncs(mutateCRDBeforeFirstPatch(&patchCalls, retargetToCAPIWebhookService)).Build()

		g.Expect(EnsureCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator")).To(Succeed())
		g.Expect(patchCalls).To(Equal(1))
		expectRetargetedCRDUntouched(t, cl, crd.Name, foreignCA)
	})

	t.Run("When patching conflicts once, it should retry and repair the CRD", func(t *testing.T) {
		g := NewWithT(t)

		webhookCA, foreignCA := testCABundles(t)
		caSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: CASecretName, Namespace: "hypershift"},
			Data:       map[string][]byte{certs.CASignerCertMapKey: webhookCA},
		}
		crd := newConversionCRD("clusters", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		patchCalls := 0
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, crd).WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				patchCalls++
				if patchCalls == 1 {
					return apierrors.NewConflict(apiextensionsv1.Resource("customresourcedefinitions"), obj.GetName(), errors.New("the object has been modified"))
				}
				return c.Patch(ctx, obj, patch, opts...)
			},
		}).Build()

		g.Expect(EnsureCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator")).To(Succeed())
		g.Expect(patchCalls).To(Equal(2))
		updated := getCRD(t, cl, crd.Name)
		g.Expect(updated.Annotations).ToNot(HaveKey(injectCABundleAnnotation))
		g.Expect(updated.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(webhookCA))
	})

	t.Run("When patching keeps conflicting, it should return the conflict after bounded retries", func(t *testing.T) {
		g := NewWithT(t)

		webhookCA, foreignCA := testCABundles(t)
		caSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: CASecretName, Namespace: "hypershift"},
			Data:       map[string][]byte{certs.CASignerCertMapKey: webhookCA},
		}
		crd := newConversionCRD("clusters", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		patchCalls := 0
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, crd).WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				patchCalls++
				return apierrors.NewConflict(apiextensionsv1.Resource("customresourcedefinitions"), obj.GetName(), errors.New("the object has been modified"))
			},
		}).Build()

		err := EnsureCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator")
		g.Expect(apierrors.IsConflict(err)).To(BeTrue(), "expected a conflict, got: %v", err)
		g.Expect(patchCalls).To(Equal(retry.DefaultBackoff.Steps))
	})

	t.Run("When the CA is rotated and the CRD updated after it was listed, it should retry with the current CA and not overwrite the newer caBundle", func(t *testing.T) {
		g := NewWithT(t)

		originalCA, rotatedCA := testCABundles(t)
		caSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: CASecretName, Namespace: "hypershift"},
			Data:       map[string][]byte{certs.CASignerCertMapKey: originalCA},
		}
		crd := newConversionCRD("clusters", "hypershift", "operator", nil, map[string]string{injectCABundleAnnotation: "true"})
		patchCalls := 0
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, crd).WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				patchCalls++
				if patchCalls == 1 {
					// Another writer rotates the CA and updates the CRD after it was listed.
					secret := &corev1.Secret{}
					if err := c.Get(ctx, client.ObjectKey{Namespace: "hypershift", Name: CASecretName}, secret); err != nil {
						return err
					}
					secret.Data[certs.CASignerCertMapKey] = rotatedCA
					if err := c.Update(ctx, secret); err != nil {
						return err
					}
					latest := &apiextensionsv1.CustomResourceDefinition{}
					if err := c.Get(ctx, client.ObjectKeyFromObject(obj), latest); err != nil {
						return err
					}
					delete(latest.Annotations, injectCABundleAnnotation)
					latest.Spec.Conversion.Webhook.ClientConfig.CABundle = rotatedCA
					if err := c.Update(ctx, latest); err != nil {
						return err
					}
				}
				return c.Patch(ctx, obj, patch, opts...)
			},
		}).Build()

		g.Expect(EnsureCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator")).To(Succeed())
		g.Expect(patchCalls).To(Equal(1))
		updated := getCRD(t, cl, crd.Name)
		g.Expect(updated.Annotations).ToNot(HaveKey(injectCABundleAnnotation))
		g.Expect(updated.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(rotatedCA))
	})

	t.Run("When reading the CA secret fails on a retry, it should return the error", func(t *testing.T) {
		g := NewWithT(t)

		webhookCA, foreignCA := testCABundles(t)
		caSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: CASecretName, Namespace: "hypershift"},
			Data:       map[string][]byte{certs.CASignerCertMapKey: webhookCA},
		}
		crd := newConversionCRD("clusters", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		getErr := errors.New("injected CA secret read failure")
		secretGets, patchCalls := 0, 0
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, crd).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*corev1.Secret); ok && key.Name == CASecretName {
					secretGets++
					if secretGets > 1 {
						return getErr
					}
				}
				return c.Get(ctx, key, obj, opts...)
			},
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				patchCalls++
				if patchCalls == 1 {
					return apierrors.NewConflict(apiextensionsv1.Resource("customresourcedefinitions"), obj.GetName(), errors.New("the object has been modified"))
				}
				return c.Patch(ctx, obj, patch, opts...)
			},
		}).Build()

		err := EnsureCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator")
		g.Expect(errors.Is(err, getErr)).To(BeTrue(), "expected the CA secret read error, got: %v", err)
		g.Expect(err.Error()).To(ContainSubstring("failed to get webhook CA secret"))
		g.Expect(patchCalls).To(Equal(1))
		g.Expect(getCRD(t, cl, crd.Name).Annotations).To(HaveKeyWithValue(injectCABundleAnnotation, "true"))
	})

	t.Run("When the CA secret is emptied before a retry, it should return the empty caBundle error without patching", func(t *testing.T) {
		g := NewWithT(t)

		webhookCA, foreignCA := testCABundles(t)
		caSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: CASecretName, Namespace: "hypershift"},
			Data:       map[string][]byte{certs.CASignerCertMapKey: webhookCA},
		}
		crd := newConversionCRD("clusters", "hypershift", "operator", foreignCA, map[string]string{injectCABundleAnnotation: "true"})
		patchCalls := 0
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(caSecret, crd).WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				patchCalls++
				if patchCalls == 1 {
					secret := &corev1.Secret{}
					if err := c.Get(ctx, client.ObjectKey{Namespace: "hypershift", Name: CASecretName}, secret); err != nil {
						return err
					}
					secret.Data = map[string][]byte{}
					if err := c.Update(ctx, secret); err != nil {
						return err
					}
					return apierrors.NewConflict(apiextensionsv1.Resource("customresourcedefinitions"), obj.GetName(), errors.New("the object has been modified"))
				}
				return c.Patch(ctx, obj, patch, opts...)
			},
		}).Build()

		err := EnsureCRDConversionWebhookCABundles(t.Context(), cl, "hypershift", "operator")
		g.Expect(err).To(MatchError("webhook CA secret exists but has empty caBundle"))
		g.Expect(patchCalls).To(Equal(1))
		unchanged := getCRD(t, cl, crd.Name)
		g.Expect(unchanged.Annotations).To(HaveKeyWithValue(injectCABundleAnnotation, "true"))
		g.Expect(unchanged.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(foreignCA))
	})
}
