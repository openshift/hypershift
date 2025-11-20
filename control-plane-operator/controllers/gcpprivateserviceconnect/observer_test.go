package gcpprivateserviceconnect

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestControllerName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "private-router service",
			input:    "private-router",
			expected: "private-router-observer",
		},
		{
			name:     "custom service name",
			input:    "my-service",
			expected: "my-service-observer",
		},
		{
			name:     "empty service name",
			input:    "",
			expected: "-observer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := ControllerName(tt.input)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestGetConsumerAcceptList(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected []string
	}{
		{
			name: "valid GCP platform with project",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "my-gcp-project",
						},
					},
				},
			},
			expected: []string{"my-gcp-project"},
		},
		{
			name: "project with numeric project ID",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "123456789012",
						},
					},
				},
			},
			expected: []string{"123456789012"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := getConsumerAcceptList(tt.hcp)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestReconcileIntegration(t *testing.T) {
	tests := []struct {
		name                string
		serviceName         string
		requestName         string
		service             *corev1.Service
		hcp                 *hyperv1.HostedControlPlane
		expectRequeue       bool
		expectError         bool
		expectGCPPSCCreated bool
	}{
		{
			name:        "When reconciling target service it should create GCPPrivateServiceConnect CR",
			serviceName: "private-router",
			requestName: "private-router",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "private-router",
					Namespace: "clusters-test-hcp",
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: hyperv1.GroupVersion.String(),
						Kind:       "HostedControlPlane",
						Name:       "test-hcp",
						UID:        "test-hcp-uid",
					}},
					Annotations: map[string]string{
						"networking.gke.io/load-balancer-type": "Internal",
					},
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{IP: "10.128.15.229"},
						},
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "clusters-test-hcp",
					UID:       "test-hcp-uid",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "my-gcp-project",
						},
					},
				},
			},
			expectRequeue:       false,
			expectError:         false,
			expectGCPPSCCreated: true,
		},
		{
			name:        "When reconciling non-target service it should skip processing",
			serviceName: "private-router",
			requestName: "other-service",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-service",
					Namespace: "clusters-test-hcp",
					Annotations: map[string]string{
						"networking.gke.io/load-balancer-type": "Internal",
					},
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{IP: "10.128.15.230"},
						},
					},
				},
			},
			expectRequeue:       false,
			expectError:         false,
			expectGCPPSCCreated: false,
		},
		{
			name:        "When service has no LoadBalancer IP it should skip processing",
			serviceName: "private-router",
			requestName: "private-router",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "private-router",
					Namespace: "clusters-test-hcp",
					Annotations: map[string]string{
						"networking.gke.io/load-balancer-type": "Internal",
					},
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{},
					},
				},
			},
			expectRequeue:       false,
			expectError:         false,
			expectGCPPSCCreated: false,
		},
		{
			name:        "When service is External LoadBalancer it should skip processing",
			serviceName: "private-router",
			requestName: "private-router",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "private-router",
					Namespace: "clusters-test-hcp",
					Annotations: map[string]string{
						"networking.gke.io/load-balancer-type": "External",
					},
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{IP: "35.1.2.3"},
						},
					},
				},
			},
			expectRequeue:       false,
			expectError:         false,
			expectGCPPSCCreated: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ctx := context.Background()

			// Create fake client with initial objects
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			_ = hyperv1.AddToScheme(scheme)

			objects := []crclient.Object{tt.service}
			if tt.hcp != nil {
				objects = append(objects, tt.hcp)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			// Create observer with fake client
			observer := &GCPPrivateServiceObserver{
				Client:           fakeClient,
				ControllerName:   "test-observer",
				ServiceName:      tt.serviceName,
				ServiceNamespace: "clusters-test-hcp",
				HCPNamespace:     "clusters-test-hcp",
				CreateOrUpdateProvider: &mockCreateOrUpdateProvider{
					client: fakeClient,
				},
			}

			// Execute reconcile
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.requestName,
					Namespace: "clusters-test-hcp",
				},
			}

			result, err := observer.Reconcile(ctx, req)

			// Verify result
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			g.Expect(result.Requeue).To(Equal(tt.expectRequeue))

			// Check if GCPPrivateServiceConnect CR was created
			if tt.expectGCPPSCCreated {
				gcpPSC := &hyperv1.GCPPrivateServiceConnect{}
				err := fakeClient.Get(ctx, types.NamespacedName{
					Name:      tt.serviceName,
					Namespace: "clusters-test-hcp",
				}, gcpPSC)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(gcpPSC.Spec.LoadBalancerIP).To(Equal("10.128.15.229"))
				g.Expect(gcpPSC.Spec.ConsumerAcceptList).To(Equal([]string{"my-gcp-project"}))
			} else {
				// Verify no GCPPrivateServiceConnect was created
				gcpPSCList := &hyperv1.GCPPrivateServiceConnectList{}
				err := fakeClient.List(ctx, gcpPSCList)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(gcpPSCList.Items).To(BeEmpty())
			}
		})
	}
}

// mockCreateOrUpdateProvider implements CreateOrUpdateProvider for testing
type mockCreateOrUpdateProvider struct {
	client crclient.Client
}

func (m *mockCreateOrUpdateProvider) CreateOrUpdate(ctx context.Context, c crclient.Client, obj crclient.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	// Execute the mutation function
	if err := f(); err != nil {
		return controllerutil.OperationResultNone, err
	}

	// Try to create the object first
	err := c.Create(ctx, obj)
	if err != nil {
		// If creation fails, try to update
		if err := c.Update(ctx, obj); err != nil {
			return controllerutil.OperationResultNone, err
		}
		return controllerutil.OperationResultUpdated, nil
	}

	return controllerutil.OperationResultCreated, nil
}
