package ignitionserverproxy

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
