package azureprivatelinkservice

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	supportutil "github.com/openshift/hypershift/support/util"

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
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "kube-apiserver-private service",
			input:    "kube-apiserver-private",
			expected: "kube-apiserver-private-observer",
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
			t.Parallel()
			g := NewGomegaWithT(t)
			result := ControllerName(tt.input)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestReconcile(t *testing.T) {
	const (
		testNamespace     = "clusters-test-hcp"
		testServiceName   = "kube-apiserver-private"
		testHCPName       = "test-hcp"
		testHCPUID        = "test-hcp-uid"
		testLBIP          = "10.0.0.100"
		testUpdatedLBIP   = "10.0.0.200"
		testSubscription  = "sub-12345"
		testResourceGroup = "test-rg"
		testLocation      = "eastus"
		testNATSubnetID   = "/subscriptions/sub-12345/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/nat-subnet"
		testSubnetID      = "/subscriptions/sub-12345/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/guest-subnet"
		testVNetID        = "/subscriptions/sub-12345/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet"
	)

	defaultHCP := func() *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testHCPName,
				Namespace: testNamespace,
				UID:       testHCPUID,
				Annotations: map[string]string{
					supportutil.HostedClusterAnnotation: "clusters/test-cluster",
				},
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AzurePlatform,
					Azure: &hyperv1.AzurePlatformSpec{
						SubscriptionID:    testSubscription,
						ResourceGroupName: testResourceGroup,
						Location:          testLocation,
						VnetID:            testVNetID,
						SubnetID:          testSubnetID,
						PrivateConnectivity: &hyperv1.AzurePrivateConnectivityConfig{
							NATSubnetID:          testNATSubnetID,
							AllowedSubscriptions: []string{testSubscription},
						},
					},
				},
			},
		}
	}

	defaultService := func() *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testServiceName,
				Namespace: testNamespace,
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: hyperv1.GroupVersion.String(),
					Kind:       "HostedControlPlane",
					Name:       testHCPName,
					UID:        testHCPUID,
				}},
				Annotations: map[string]string{
					"service.beta.kubernetes.io/azure-load-balancer-internal": "true",
				},
			},
			Status: corev1.ServiceStatus{
				LoadBalancer: corev1.LoadBalancerStatus{
					Ingress: []corev1.LoadBalancerIngress{
						{IP: testLBIP},
					},
				},
			},
		}
	}

	tests := []struct {
		name                   string
		serviceName            string
		requestName            string
		service                *corev1.Service
		hcp                    *hyperv1.HostedControlPlane
		existingPLS            *hyperv1.AzurePrivateLinkService
		expectError            bool
		expectPLSCreated       bool
		expectedLoadBalancerIP string
	}{
		{
			name:                   "When KAS Service has ILB IP, it should create AzurePrivateLinkService CR",
			serviceName:            testServiceName,
			requestName:            testServiceName,
			service:                defaultService(),
			hcp:                    defaultHCP(),
			expectError:            false,
			expectPLSCreated:       true,
			expectedLoadBalancerIP: testLBIP,
		},
		{
			name:        "When KAS Service has no ILB annotation, it should skip",
			serviceName: testServiceName,
			requestName: testServiceName,
			service: func() *corev1.Service {
				svc := defaultService()
				svc.Annotations = map[string]string{} // No ILB annotation
				return svc
			}(),
			hcp:              defaultHCP(),
			expectError:      false,
			expectPLSCreated: false,
		},
		{
			name:        "When KAS Service has no ingress IP yet, it should not create CR",
			serviceName: testServiceName,
			requestName: testServiceName,
			service: func() *corev1.Service {
				svc := defaultService()
				svc.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{}
				return svc
			}(),
			hcp:              defaultHCP(),
			expectError:      false,
			expectPLSCreated: false,
		},
		{
			name:        "When HCP is being deleted, it should not create CR",
			serviceName: testServiceName,
			requestName: testServiceName,
			service:     defaultService(),
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := defaultHCP()
				now := metav1.NewTime(time.Now())
				hcp.DeletionTimestamp = &now
				hcp.Finalizers = []string{"test-finalizer"} // Required for DeletionTimestamp to be respected
				return hcp
			}(),
			expectError:      false,
			expectPLSCreated: false,
		},
		{
			name:        "When CR already exists, it should update loadBalancerIP",
			serviceName: testServiceName,
			requestName: testServiceName,
			service: func() *corev1.Service {
				svc := defaultService()
				svc.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{
					{IP: testUpdatedLBIP},
				}
				return svc
			}(),
			hcp: defaultHCP(),
			existingPLS: &hyperv1.AzurePrivateLinkService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testServiceName,
					Namespace: testNamespace,
				},
				Spec: hyperv1.AzurePrivateLinkServiceSpec{
					LoadBalancerIP:       testLBIP, // Old IP
					SubscriptionID:       testSubscription,
					ResourceGroupName:    testResourceGroup,
					Location:             testLocation,
					NATSubnetID:          testNATSubnetID,
					AllowedSubscriptions: []string{testSubscription},
					GuestSubnetID:        testSubnetID,
					GuestVNetID:          testVNetID,
				},
			},
			expectError:            false,
			expectPLSCreated:       true,
			expectedLoadBalancerIP: testUpdatedLBIP,
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
			if tt.existingPLS != nil {
				objects = append(objects, tt.existingPLS)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			// Create observer with fake client
			observer := &AzurePrivateLinkServiceObserver{
				Client:           fakeClient,
				ControllerName:   "test-observer",
				ServiceName:      tt.serviceName,
				ServiceNamespace: testNamespace,
				HCPNamespace:     testNamespace,
				CreateOrUpdateProvider: &mockCreateOrUpdateProvider{
					client: fakeClient,
				},
			}

			// Execute reconcile
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.requestName,
					Namespace: testNamespace,
				},
			}

			result, err := observer.Reconcile(ctx, req)

			// Verify result
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			g.Expect(result.Requeue).To(BeFalse())

			// Check if AzurePrivateLinkService CR was created/updated
			if tt.expectPLSCreated {
				azurePLS := &hyperv1.AzurePrivateLinkService{}
				err := fakeClient.Get(ctx, types.NamespacedName{
					Name:      tt.serviceName,
					Namespace: testNamespace,
				}, azurePLS)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(azurePLS.Spec.LoadBalancerIP).To(Equal(tt.expectedLoadBalancerIP))
				g.Expect(azurePLS.Spec.SubscriptionID).To(Equal(testSubscription))
				g.Expect(azurePLS.Spec.ResourceGroupName).To(Equal(testResourceGroup))
				g.Expect(azurePLS.Spec.Location).To(Equal(testLocation))
				g.Expect(azurePLS.Spec.NATSubnetID).To(Equal(testNATSubnetID))
				g.Expect(azurePLS.Spec.AllowedSubscriptions).To(Equal([]string{testSubscription}))
				g.Expect(azurePLS.Spec.GuestSubnetID).To(Equal(testSubnetID))
				g.Expect(azurePLS.Spec.GuestVNetID).To(Equal(testVNetID))

				// Verify owner reference
				g.Expect(azurePLS.OwnerReferences).To(HaveLen(1))
				g.Expect(azurePLS.OwnerReferences[0].Kind).To(Equal("HostedControlPlane"))
				g.Expect(azurePLS.OwnerReferences[0].Name).To(Equal(testHCPName))

				// Verify HostedCluster annotation is copied
				g.Expect(azurePLS.Annotations).To(HaveKeyWithValue(supportutil.HostedClusterAnnotation, "clusters/test-cluster"))
			} else {
				// Verify no AzurePrivateLinkService was created
				azurePLSList := &hyperv1.AzurePrivateLinkServiceList{}
				err := fakeClient.List(ctx, azurePLSList)
				g.Expect(err).ToNot(HaveOccurred())
				if tt.existingPLS == nil {
					g.Expect(azurePLSList.Items).To(BeEmpty())
				}
			}
		})
	}
}

// mockCreateOrUpdateProvider implements CreateOrUpdateProvider for testing
type mockCreateOrUpdateProvider struct {
	client crclient.Client
}

func (m *mockCreateOrUpdateProvider) CreateOrUpdate(ctx context.Context, c crclient.Client, obj crclient.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	return controllerutil.CreateOrUpdate(ctx, c, obj, f)
}
