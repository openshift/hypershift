package webhookvalidation

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		admissionregistrationv1.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("failed to add to scheme: %v", err)
		}
	}
	return s
}

func TestIsAllowedWebhookURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		disallowedURLs []string
		url            string
		expected       bool
	}{
		{
			name:           "When URL contains a disallowed substring, it should return false",
			disallowedURLs: []string{"https://etcd-client"},
			url:            "https://etcd-client:2379",
			expected:       false,
		},
		{
			name:           "When URL matches a fully qualified disallowed URL, it should return false",
			disallowedURLs: []string{"https://etcd-client.ns.svc"},
			url:            "https://etcd-client.ns.svc:2379/path",
			expected:       false,
		},
		{
			name:           "When URL does not match any disallowed URL, it should return true",
			disallowedURLs: []string{"https://etcd-client"},
			url:            "https://external.example.com",
			expected:       true,
		},
		{
			name:           "When disallowed list is empty, it should return true",
			disallowedURLs: []string{},
			url:            "https://anything",
			expected:       true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			result := isAllowedWebhookURL(tt.disallowedURLs, tt.url)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestReconcilerReconcile(t *testing.T) {
	t.Parallel()
	const hcpNamespace = "test-hcp-namespace"

	tests := []struct {
		name               string
		webhookType        string
		cpServices         []corev1.Service
		guestObjects       []client.Object
		reconcileName      string
		expectWebhookGone  bool
		expectWebhookAlive bool
	}{
		{
			name:        "When validating webhook targets a CP service, it should delete the webhook",
			webhookType: validatingType.name,
			cpServices: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-client",
						Namespace: hcpNamespace,
					},
				},
			},
			guestObjects: []client.Object{
				&admissionregistrationv1.ValidatingWebhookConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "test-validating-webhook"},
					Webhooks: []admissionregistrationv1.ValidatingWebhook{
						{
							Name:         "test.webhook.io",
							ClientConfig: admissionregistrationv1.WebhookClientConfig{URL: ptr.To("https://etcd-client:2379")},
						},
					},
				},
			},
			reconcileName:     "test-validating-webhook",
			expectWebhookGone: true,
		},
		{
			name:        "When validating webhook targets an allowed CP service, it should preserve the webhook",
			webhookType: validatingType.name,
			cpServices: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "allowed-service",
						Namespace: hcpNamespace,
						Labels:    map[string]string{hyperv1.AllowGuestWebhooksServiceLabel: "true"},
					},
				},
			},
			guestObjects: []client.Object{
				&admissionregistrationv1.ValidatingWebhookConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "preserved-validating-webhook"},
					Webhooks: []admissionregistrationv1.ValidatingWebhook{
						{
							Name:         "preserved.webhook.io",
							ClientConfig: admissionregistrationv1.WebhookClientConfig{URL: ptr.To("https://allowed-service:8443")},
						},
					},
				},
			},
			reconcileName:      "preserved-validating-webhook",
			expectWebhookAlive: true,
		},
		{
			name:        "When mutating webhook targets a CP service, it should delete the webhook",
			webhookType: mutatingType.name,
			cpServices: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: hcpNamespace,
					},
				},
			},
			guestObjects: []client.Object{
				&admissionregistrationv1.MutatingWebhookConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "test-mutating-webhook"},
					Webhooks: []admissionregistrationv1.MutatingWebhook{
						{
							Name:         "mutating.webhook.io",
							ClientConfig: admissionregistrationv1.WebhookClientConfig{URL: ptr.To("https://kube-apiserver:6443")},
						},
					},
				},
			},
			reconcileName:     "test-mutating-webhook",
			expectWebhookGone: true,
		},
		{
			name:        "When webhook targets an external URL, it should preserve the webhook",
			webhookType: validatingType.name,
			cpServices: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-client",
						Namespace: hcpNamespace,
					},
				},
			},
			guestObjects: []client.Object{
				&admissionregistrationv1.ValidatingWebhookConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "external-validating-webhook"},
					Webhooks: []admissionregistrationv1.ValidatingWebhook{
						{
							Name:         "external.webhook.io",
							ClientConfig: admissionregistrationv1.WebhookClientConfig{URL: ptr.To("https://external.example.com")},
						},
					},
				},
			},
			reconcileName:      "external-validating-webhook",
			expectWebhookAlive: true,
		},
		{
			name:        "When webhook uses Service reference instead of URL, it should preserve the webhook",
			webhookType: validatingType.name,
			cpServices: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-client",
						Namespace: hcpNamespace,
					},
				},
			},
			guestObjects: []client.Object{
				&admissionregistrationv1.ValidatingWebhookConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "service-ref-webhook"},
					Webhooks: []admissionregistrationv1.ValidatingWebhook{
						{
							Name: "service.webhook.io",
							ClientConfig: admissionregistrationv1.WebhookClientConfig{
								Service: &admissionregistrationv1.ServiceReference{
									Name:      "my-webhook-service",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			reconcileName:      "service-ref-webhook",
			expectWebhookAlive: true,
		},
		{
			name:        "When validating webhook has mixed allowed and disallowed URLs, it should delete the entire configuration",
			webhookType: validatingType.name,
			cpServices: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-client",
						Namespace: hcpNamespace,
					},
				},
			},
			guestObjects: []client.Object{
				&admissionregistrationv1.ValidatingWebhookConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "mixed-validating-webhook"},
					Webhooks: []admissionregistrationv1.ValidatingWebhook{
						{
							Name:         "allowed.webhook.io",
							ClientConfig: admissionregistrationv1.WebhookClientConfig{URL: ptr.To("https://external.example.com")},
						},
						{
							Name:         "disallowed.webhook.io",
							ClientConfig: admissionregistrationv1.WebhookClientConfig{URL: ptr.To("https://etcd-client:2379")},
						},
					},
				},
			},
			reconcileName:     "mixed-validating-webhook",
			expectWebhookGone: true,
		},
		{
			name:        "When webhook config does not exist, it should return without error",
			webhookType: validatingType.name,
			cpServices: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-client",
						Namespace: hcpNamespace,
					},
				},
			},
			guestObjects:  []client.Object{},
			reconcileName: "nonexistent-webhook",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			ctx := t.Context()
			scheme := testScheme(t)

			cpObjects := make([]client.Object, 0, len(tt.cpServices))
			for i := range tt.cpServices {
				cpObjects = append(cpObjects, &tt.cpServices[i])
			}

			cpClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cpObjects...).Build()
			guestClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.guestObjects...).Build()

			r := &reconciler{
				client:       guestClient,
				cpClient:     cpClient,
				hcpNamespace: hcpNamespace,
			}

			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKey{
					Namespace: tt.webhookType,
					Name:      tt.reconcileName,
				},
			})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(reconcile.Result{}))

			if tt.expectWebhookGone {
				assertWebhookNotFound(g, ctx, guestClient, tt.webhookType, tt.reconcileName)
			}
			if tt.expectWebhookAlive {
				assertWebhookExists(g, ctx, guestClient, tt.webhookType, tt.reconcileName)
			}
		})
	}
}

func TestReconcilerReconcile_CPClientListError(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	ctx := t.Context()
	scheme := testScheme(t)

	cpClient := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
			return fmt.Errorf("simulated cp list error")
		},
	}).Build()
	guestClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &reconciler{
		client:       guestClient,
		cpClient:     cpClient,
		hcpNamespace: "test-ns",
	}

	_, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: client.ObjectKey{
			Namespace: validatingType.name,
			Name:      "any-webhook",
		},
	})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("simulated cp list error"))
}

func TestReconcilerReconcile_GuestGetNon404Error(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	ctx := t.Context()
	scheme := testScheme(t)

	cpClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	guestClient := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
			return fmt.Errorf("simulated guest get error")
		},
	}).Build()

	r := &reconciler{
		client:       guestClient,
		cpClient:     cpClient,
		hcpNamespace: "test-ns",
	}

	_, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: client.ObjectKey{
			Namespace: validatingType.name,
			Name:      "any-webhook",
		},
	})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("simulated guest get error"))
}

func assertWebhookNotFound(g Gomega, ctx context.Context, c client.Client, wtName, name string) {
	wt := webhookTypesByName[wtName]
	err := c.Get(ctx, client.ObjectKey{Name: name}, wt.newObj())
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "%s webhook %q should have been deleted", wtName, name)
}

func assertWebhookExists(g Gomega, ctx context.Context, c client.Client, wtName, name string) {
	wt := webhookTypesByName[wtName]
	g.Expect(c.Get(ctx, client.ObjectKey{Name: name}, wt.newObj())).To(Succeed(),
		"%s webhook %q should still exist", wtName, name)
}
