package ignitionserverproxy

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestAdaptService(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		services     []hyperv1.ServicePublishingStrategyMapping
		expectedType corev1.ServiceType
		expectedPort int32
		expectError  bool
		errorMessage string
	}{
		{
			name: "When NodePort strategy is configured without specific port, it should create NodePort service",
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service: hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type: hyperv1.NodePort,
					},
				},
			},
			expectedType: corev1.ServiceTypeNodePort,
			expectedPort: 0,
			expectError:  false,
		},
		{
			name: "When NodePort strategy is configured with specific port, it should create NodePort service with specified port",
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service: hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type: hyperv1.NodePort,
						NodePort: &hyperv1.NodePortPublishingStrategy{
							Port: 30123,
						},
					},
				},
			},
			expectedType: corev1.ServiceTypeNodePort,
			expectedPort: 30123,
			expectError:  false,
		},
		{
			name: "When Route strategy is configured, it should create ClusterIP service",
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service: hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type: hyperv1.Route,
					},
				},
			},
			expectedType: corev1.ServiceTypeClusterIP,
			expectedPort: 0,
			expectError:  false,
		},
		{
			name: "When LoadBalancer strategy is configured, it should return error",
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service: hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type: hyperv1.LoadBalancer,
					},
				},
			},
			expectError:  true,
			errorMessage: "invalid publishing strategy for Ignition service: LoadBalancer",
		},
		{
			name: "When S3 strategy is configured, it should return error",
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service: hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type: hyperv1.S3,
					},
				},
			},
			expectError:  true,
			errorMessage: "invalid publishing strategy for Ignition service: S3",
		},
		{
			name:         "When ignition service strategy is not specified, it should return error",
			services:     []hyperv1.ServicePublishingStrategyMapping{},
			expectError:  true,
			errorMessage: "ignition service strategy not specified",
		},
		{
			name: "When ignition service strategy is missing in service list, it should return error",
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service: hyperv1.APIServer,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type: hyperv1.Route,
					},
				},
			},
			expectError:  true,
			errorMessage: "ignition service strategy not specified",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: tc.services,
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			svc := &corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeClusterIP,
					Ports: []corev1.ServicePort{
						{
							Name:     "https",
							Port:     443,
							Protocol: corev1.ProtocolTCP,
						},
					},
				},
			}

			err := adaptService(cpContext, svc)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tc.errorMessage))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(svc.Spec.Type).To(Equal(tc.expectedType))
				if tc.expectedPort > 0 {
					g.Expect(svc.Spec.Ports).To(HaveLen(1))
					g.Expect(svc.Spec.Ports[0].NodePort).To(Equal(tc.expectedPort))
				} else {
					g.Expect(svc.Spec.Ports).To(HaveLen(1))
					g.Expect(svc.Spec.Ports[0].NodePort).To(Equal(int32(0)), "NodePort should not be set when no specific port is configured")
				}
			}
		})
	}

	const namespace = "clusters-example"
	ignitionStrategy := func(strategy hyperv1.ServicePublishingStrategy) []hyperv1.ServicePublishingStrategyMapping {
		return []hyperv1.ServicePublishingStrategyMapping{{Service: hyperv1.Ignition, ServicePublishingStrategy: strategy}}
	}
	// renderService loads the service manifest and adapts it the same way the
	// component framework does before applying it.
	renderService := func(t *testing.T, services []hyperv1.ServicePublishingStrategyMapping) *corev1.Service {
		t.Helper()
		g := NewWithT(t)
		obj, _, err := assets.LoadManifest(ComponentName, "service.yaml")
		g.Expect(err).ToNot(HaveOccurred())
		svc := obj.(*corev1.Service)
		svc.SetNamespace(namespace)
		cpContext := component.WorkloadContext{
			HCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AgentPlatform},
					Services: services,
				},
			},
		}
		g.Expect(adaptService(cpContext, svc)).To(Succeed())
		return svc
	}

	ipFamilyPolicyTestCases := []struct {
		name             string
		services         []hyperv1.ServicePublishingStrategyMapping
		expectedType     corev1.ServiceType
		expectedNodePort int32
	}{
		{
			name:         "When the manifest is adapted for NodePort without a port, it should prefer dual-stack on a NodePort service",
			services:     ignitionStrategy(hyperv1.ServicePublishingStrategy{Type: hyperv1.NodePort}),
			expectedType: corev1.ServiceTypeNodePort,
		},
		{
			name:             "When the manifest is adapted for NodePort with a port, it should prefer dual-stack and use the strategy port",
			services:         ignitionStrategy(hyperv1.ServicePublishingStrategy{Type: hyperv1.NodePort, NodePort: &hyperv1.NodePortPublishingStrategy{Port: 30123}}),
			expectedType:     corev1.ServiceTypeNodePort,
			expectedNodePort: 30123,
		},
		{
			name:         "When the manifest is adapted for Route, it should prefer dual-stack on a ClusterIP service",
			services:     ignitionStrategy(hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}),
			expectedType: corev1.ServiceTypeClusterIP,
		},
	}
	for _, tc := range ipFamilyPolicyTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			svc := renderService(t, tc.services)

			g.Expect(svc.Spec.IPFamilyPolicy).To(Equal(ptr.To(corev1.IPFamilyPolicyPreferDualStack)))
			g.Expect(svc.Spec.Type).To(Equal(tc.expectedType))
			g.Expect(svc.Spec.Ports).To(HaveLen(1))
			g.Expect(svc.Spec.Ports[0].NodePort).To(Equal(tc.expectedNodePort))
			// Address allocation is left to the API server.
			g.Expect(svc.Spec.ClusterIP).To(BeEmpty())
			g.Expect(svc.Spec.ClusterIPs).To(BeNil())
			g.Expect(svc.Spec.IPFamilies).To(BeNil())
		})
	}

	// Existing services are updated by the apply provider with the rendered service.
	// The fake client does not allocate addresses or ports like the API server does,
	// so these cases check the update request and that a later reconcile of the
	// API server's dual-stack result is a no-op.
	existingServiceTestCases := []struct {
		name                    string
		services                []hyperv1.ServicePublishingStrategyMapping
		existing                corev1.ServiceSpec
		expectedRequestNodePort int32
	}{
		{
			name:     "When an existing single-stack NodePort service is updated without a strategy port, it should request dual-stack without allocated values and then stay unchanged",
			services: ignitionStrategy(hyperv1.ServicePublishingStrategy{Type: hyperv1.NodePort}),
			existing: corev1.ServiceSpec{
				Type:           corev1.ServiceTypeNodePort,
				ClusterIP:      "172.30.23.45",
				ClusterIPs:     []string{"172.30.23.45"},
				IPFamilies:     []corev1.IPFamily{corev1.IPv4Protocol},
				IPFamilyPolicy: ptr.To(corev1.IPFamilyPolicySingleStack),
				Ports:          []corev1.ServicePort{{Name: "https", Port: 443, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromString("https"), NodePort: 31567}},
				Selector:       map[string]string{"app": ComponentName},
			},
		},
		{
			name:     "When an existing single-stack NodePort service is updated with a strategy port, it should request dual-stack with that port and then stay unchanged",
			services: ignitionStrategy(hyperv1.ServicePublishingStrategy{Type: hyperv1.NodePort, NodePort: &hyperv1.NodePortPublishingStrategy{Port: 30123}}),
			existing: corev1.ServiceSpec{
				Type:           corev1.ServiceTypeNodePort,
				ClusterIP:      "172.30.23.46",
				ClusterIPs:     []string{"172.30.23.46"},
				IPFamilies:     []corev1.IPFamily{corev1.IPv4Protocol},
				IPFamilyPolicy: ptr.To(corev1.IPFamilyPolicySingleStack),
				Ports:          []corev1.ServicePort{{Name: "https", Port: 443, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromString("https"), NodePort: 30123}},
				Selector:       map[string]string{"app": ComponentName},
			},
			expectedRequestNodePort: 30123,
		},
		{
			name:     "When an existing single-stack ClusterIP service is updated for Route, it should request dual-stack without allocated addresses and then stay unchanged",
			services: ignitionStrategy(hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}),
			existing: corev1.ServiceSpec{
				Type:           corev1.ServiceTypeClusterIP,
				ClusterIP:      "172.30.23.47",
				ClusterIPs:     []string{"172.30.23.47"},
				IPFamilies:     []corev1.IPFamily{corev1.IPv4Protocol},
				IPFamilyPolicy: ptr.To(corev1.IPFamilyPolicySingleStack),
				Ports:          []corev1.ServicePort{{Name: "https", Port: 443, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromString("https")}},
				Selector:       map[string]string{"app": ComponentName},
			},
		},
	}
	for _, tc := range existingServiceTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			existing := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:        ComponentName,
					Namespace:   namespace,
					Annotations: map[string]string{upsert.DesiredStateHashAnnotation: "hash-of-previous-desired-state"},
				},
				Spec: *tc.existing.DeepCopy(),
			}

			var updateRequests []*corev1.Service
			c := fake.NewClientBuilder().
				WithObjects(existing).
				WithInterceptorFuncs(interceptor.Funcs{
					// The control-plane-operator reads through the manager cache, which sets
					// apiVersion and kind on returned objects. The fake client clears them,
					// which would make every apply compare as drift.
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if err := c.Get(ctx, key, obj, opts...); err != nil {
							return err
						}
						obj.GetObjectKind().SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Service"))
						return nil
					},
					Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
						if svc, ok := obj.(*corev1.Service); ok {
							updateRequests = append(updateRequests, svc.DeepCopy())
						}
						return c.Update(ctx, obj, opts...)
					},
				}).
				Build()
			provider := upsert.NewApplyProvider(false)

			result, err := provider.ApplyManifest(t.Context(), c, renderService(t, tc.services))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(controllerutil.OperationResultUpdated))
			g.Expect(updateRequests).To(HaveLen(1))
			request := updateRequests[0]
			g.Expect(request.Spec.IPFamilyPolicy).To(Equal(ptr.To(corev1.IPFamilyPolicyPreferDualStack)))
			g.Expect(request.Spec.Type).To(Equal(tc.existing.Type))
			g.Expect(request.Spec.Selector).To(Equal(tc.existing.Selector))
			g.Expect(request.Spec.Ports).To(HaveLen(1))
			g.Expect(request.Spec.Ports[0].Port).To(Equal(int32(443)))
			g.Expect(request.Spec.Ports[0].NodePort).To(Equal(tc.expectedRequestNodePort))
			// The update leaves allocated addresses out, and the NodePort too unless the
			// strategy sets one; the API server keeps the existing values and adds the
			// second family on dual-stack clusters.
			g.Expect(request.Spec.ClusterIP).To(BeEmpty())
			g.Expect(request.Spec.ClusterIPs).To(BeNil())
			g.Expect(request.Spec.IPFamilies).To(BeNil())

			// Emulate the API server's dual-stack result: keep the primary ClusterIP and
			// NodePort, and append an IPv6 ClusterIP.
			stored := &corev1.Service{}
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(existing), stored)).To(Succeed())
			stored.Spec.ClusterIP = tc.existing.ClusterIP
			stored.Spec.ClusterIPs = []string{tc.existing.ClusterIP, "fd02::23:45"}
			stored.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol}
			stored.Spec.Ports[0].NodePort = tc.existing.Ports[0].NodePort
			g.Expect(c.Update(t.Context(), stored)).To(Succeed())
			updateRequests = nil

			result, err = provider.ApplyManifest(t.Context(), c, renderService(t, tc.services))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(controllerutil.OperationResultNone))
			g.Expect(updateRequests).To(BeEmpty())
		})
	}
}

func TestAdaptServicePreservesNodePort(t *testing.T) {
	t.Parallel()

	g := NewWithT(t)

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service: hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type: hyperv1.NodePort,
						NodePort: &hyperv1.NodePortPublishingStrategy{
							Port: 30456,
						},
					},
				},
			},
		},
	}

	cpContext := component.WorkloadContext{
		HCP: hcp,
	}

	// Simulate existing service with different NodePort
	svc := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
			Ports: []corev1.ServicePort{
				{
					Name:     "https",
					Port:     443,
					Protocol: corev1.ProtocolTCP,
					NodePort: 31000, // Existing port, different from strategy
				},
			},
		},
	}

	err := adaptService(cpContext, svc)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeNodePort))
	// The adapt function should update the NodePort to match the strategy
	g.Expect(svc.Spec.Ports[0].NodePort).To(Equal(int32(30456)))
}

func TestAdaptServiceMultiplePorts(t *testing.T) {
	t.Parallel()

	g := NewWithT(t)

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service: hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type: hyperv1.NodePort,
						NodePort: &hyperv1.NodePortPublishingStrategy{
							Port: 30789,
						},
					},
				},
			},
		},
	}

	cpContext := component.WorkloadContext{
		HCP: hcp,
	}

	// Service with multiple ports
	svc := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
			Ports: []corev1.ServicePort{
				{
					Name:     "https",
					Port:     443,
					Protocol: corev1.ProtocolTCP,
				},
				{
					Name:     "metrics",
					Port:     9090,
					Protocol: corev1.ProtocolTCP,
				},
			},
		},
	}

	err := adaptService(cpContext, svc)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeNodePort))
	// Only the first port should have the NodePort set
	g.Expect(svc.Spec.Ports[0].NodePort).To(Equal(int32(30789)))
}

func TestServicePublishingStrategyByTypeForHCP(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		services []hyperv1.ServicePublishingStrategyMapping
		validate func(g Gomega, err error)
	}{
		{
			name:     "When nil services are provided, it should return error",
			services: nil,
			validate: func(g Gomega, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal("ignition service strategy not specified"))
			},
		},
		{
			name:     "When empty services slice is provided, it should return error",
			services: []hyperv1.ServicePublishingStrategyMapping{},
			validate: func(g Gomega, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal("ignition service strategy not specified"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: tc.services,
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			svc := &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "https", Port: 443},
					},
				},
			}

			err := adaptService(cpContext, svc)
			tc.validate(g, err)
		})
	}
}

func TestAdaptServiceValidatesStrategyType(t *testing.T) {
	t.Parallel()

	invalidStrategies := []hyperv1.PublishingStrategyType{
		hyperv1.LoadBalancer,
		hyperv1.S3,
		// Any other non-NodePort/Route strategy
	}

	for _, strategyType := range invalidStrategies {
		t.Run(fmt.Sprintf("When strategy type is %s, it should return error", strategyType), func(t *testing.T) {
			t.Parallel()

			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: strategyType,
							},
						},
					},
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			svc := &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "https", Port: 443},
					},
				},
			}

			err := adaptService(cpContext, svc)
			g.Expect(err).To(HaveOccurred())
			g.Expect(err.Error()).To(ContainSubstring("invalid publishing strategy for Ignition service"))
		})
	}
}
