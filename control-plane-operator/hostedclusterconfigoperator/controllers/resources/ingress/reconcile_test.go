package ingress

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"

	operatorv1 "github.com/openshift/api/operator/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileDefaultIngressController(t *testing.T) {
	fakeIngressDomain := "example.com"
	fakeInputReplicas := int32(3)
	testsCases := []struct {
		name                      string
		inputIngressController    *operatorv1.IngressController
		inputIngressDomain        string
		inputPlatformType         hyperv1.PlatformType
		inputReplicas             int32
		inputIsIBMCloudUPI        bool
		inputIsPrivate            bool
		inputIsNLB                bool
		inputLoadBalancerScope    operatorv1.LoadBalancerScope
		inputLoadBalancerIP       string
		expectedIngressController *operatorv1.IngressController
	}{
		{
			name:                   "IBM Cloud UPI uses Nodeport publishing strategy",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.IBMCloudPlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     true,
			inputIsPrivate:         false,
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.NodePortServiceStrategyType,
						NodePort: &operatorv1.NodePortStrategy{
							Protocol: operatorv1.TCPProtocol,
						},
					},
					NodePlacement: &operatorv1.NodePlacement{
						Tolerations: []corev1.Toleration{
							{
								Key:   "dedicated",
								Value: "edge",
							},
						},
					},
				},
			},
		},
		{
			name:                   "IBM Cloud Non-UPI uses LoadBalancer publishing strategy (External)",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.IBMCloudPlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         false,
			inputLoadBalancerScope: operatorv1.ExternalLoadBalancer,
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.LoadBalancerServiceStrategyType,
						LoadBalancer: &operatorv1.LoadBalancerStrategy{
							Scope: operatorv1.ExternalLoadBalancer,
						},
					},
					NodePlacement: &operatorv1.NodePlacement{
						Tolerations: []corev1.Toleration{
							{
								Key:   "dedicated",
								Value: "edge",
							},
						},
					},
				},
			},
		},
		{
			name:                   "IBM Cloud Non-UPI uses LoadBalancer publishing strategy (Internal)",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.IBMCloudPlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         false,
			inputLoadBalancerScope: operatorv1.InternalLoadBalancer,
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.LoadBalancerServiceStrategyType,
						LoadBalancer: &operatorv1.LoadBalancerStrategy{
							Scope: operatorv1.InternalLoadBalancer,
						},
					},
					NodePlacement: &operatorv1.NodePlacement{
						Tolerations: []corev1.Toleration{
							{
								Key:   "dedicated",
								Value: "edge",
							},
						},
					},
				},
			},
		},
		{
			name:                   "Kubevirt uses NodePort publishing strategy",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.KubevirtPlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         false,
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.NodePortServiceStrategyType,
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		{
			name:                   "None Platform uses HostNetwork publishing strategy",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.NonePlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         false,
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.HostNetworkStrategyType,
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		{
			name:                   "AWS uses Loadbalancer publishing strategy",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.AWSPlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         false,
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.LoadBalancerServiceStrategyType,
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		{
			name:                   "Private Publishing Strategy on IBM Cloud",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.IBMCloudPlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         true,
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type:    operatorv1.PrivateStrategyType,
						Private: &operatorv1.PrivateStrategy{},
					},
					NodePlacement: &operatorv1.NodePlacement{
						Tolerations: []corev1.Toleration{
							{
								Key:   "dedicated",
								Value: "edge",
							},
						},
					},
				},
			},
		},
		{
			name:                   "Private Publishing Strategy on other Platforms",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         true,
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type:    operatorv1.PrivateStrategyType,
						Private: &operatorv1.PrivateStrategy{},
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		{
			name: "Existing ingress controller",
			inputIngressController: func() *operatorv1.IngressController {
				ic := manifests.IngressDefaultIngressController()
				ic.ResourceVersion = "1"
				return ic
			}(),
			inputIngressDomain: fakeIngressDomain,
			inputReplicas:      fakeInputReplicas,
			inputIsIBMCloudUPI: false,
			inputIsPrivate:     false,
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: func() metav1.ObjectMeta {
					m := manifests.IngressDefaultIngressController().ObjectMeta
					m.ResourceVersion = "1"
					return m
				}(),
				Spec: operatorv1.IngressControllerSpec{},
			},
		},
		{
			name:                   "NLB ingress controller service",
			inputPlatformType:      hyperv1.AWSPlatform,
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputReplicas:          fakeInputReplicas,
			inputIsNLB:             true,
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.LoadBalancerServiceStrategyType,
						LoadBalancer: &operatorv1.LoadBalancerStrategy{
							ProviderParameters: &operatorv1.ProviderLoadBalancerParameters{
								Type: operatorv1.AWSLoadBalancerProvider,
								AWS: &operatorv1.AWSLoadBalancerParameters{
									Type:                          operatorv1.AWSNetworkLoadBalancer,
									NetworkLoadBalancerParameters: &operatorv1.AWSNetworkLoadBalancerParameters{},
								},
							},
						},
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		{
			name:                   "OpenStack uses Loadbalancer publishing strategy with a floating IP",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.OpenStackPlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         false,
			inputLoadBalancerIP:    "1.2.3.4",
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.LoadBalancerServiceStrategyType,
						LoadBalancer: &operatorv1.LoadBalancerStrategy{
							ProviderParameters: &operatorv1.ProviderLoadBalancerParameters{
								Type: operatorv1.OpenStackLoadBalancerProvider,
								OpenStack: &operatorv1.OpenStackLoadBalancerParameters{
									FloatingIP: "1.2.3.4",
								},
							},
						},
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := ReconcileDefaultIngressController(tc.inputIngressController, tc.inputIngressDomain, tc.inputPlatformType, tc.inputReplicas, tc.inputIsIBMCloudUPI, tc.inputIsPrivate, tc.inputIsNLB, tc.inputLoadBalancerScope, tc.inputLoadBalancerIP, nil)
			g.Expect(err).To(BeNil())
			g.Expect(tc.inputIngressController).To(BeEquivalentTo(tc.expectedIngressController))
		})
	}
}

func TestReconcileDefaultIngressControllerWithCustomEndpointPublishingStrategy(t *testing.T) {
	fakeIngressDomain := "example.com"
	fakeInputReplicas := int32(3)

	testsCases := []struct {
		name                            string
		inputIngressController          *operatorv1.IngressController
		inputIngressDomain              string
		inputPlatformType               hyperv1.PlatformType
		inputReplicas                   int32
		inputIsIBMCloudUPI              bool
		inputIsPrivate                  bool
		inputIsNLB                      bool
		inputLoadBalancerScope          operatorv1.LoadBalancerScope
		inputLoadBalancerIP             string
		inputEndpointPublishingStrategy *operatorv1.EndpointPublishingStrategy
		expectedIngressController       *operatorv1.IngressController
	}{
		{
			name:                   "Custom HostNetwork strategy overrides AWS platform default",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.AWSPlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         false,
			inputEndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.HostNetworkStrategyType,
				HostNetwork: &operatorv1.HostNetworkStrategy{
					HTTPPort:  8080,
					HTTPSPort: 8443,
					StatsPort: 1936,
				},
			},
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.HostNetworkStrategyType,
						HostNetwork: &operatorv1.HostNetworkStrategy{
							HTTPPort:  8080,
							HTTPSPort: 8443,
							StatsPort: 1936,
						},
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		{
			name:                   "Custom NodePort strategy overrides None platform default",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.NonePlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         false,
			inputEndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.NodePortServiceStrategyType,
				NodePort: &operatorv1.NodePortStrategy{
					Protocol: operatorv1.ProxyProtocol,
				},
			},
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.NodePortServiceStrategyType,
						NodePort: &operatorv1.NodePortStrategy{
							Protocol: operatorv1.ProxyProtocol,
						},
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		{
			name:                   "Custom LoadBalancer with Internal scope overrides KubeVirt platform default",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.KubevirtPlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         false,
			inputEndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.LoadBalancerServiceStrategyType,
				LoadBalancer: &operatorv1.LoadBalancerStrategy{
					Scope: operatorv1.InternalLoadBalancer,
				},
			},
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.LoadBalancerServiceStrategyType,
						LoadBalancer: &operatorv1.LoadBalancerStrategy{
							Scope: operatorv1.InternalLoadBalancer,
						},
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		{
			name:                   "Custom Private strategy overrides AWS platform default",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.AWSPlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         false,
			inputEndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
				Type:    operatorv1.PrivateStrategyType,
				Private: &operatorv1.PrivateStrategy{},
			},
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type:    operatorv1.PrivateStrategyType,
						Private: &operatorv1.PrivateStrategy{},
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		{
			name:                   "Custom LoadBalancer with AWS NLB parameters",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.AWSPlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         false,
			inputEndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.LoadBalancerServiceStrategyType,
				LoadBalancer: &operatorv1.LoadBalancerStrategy{
					Scope: operatorv1.ExternalLoadBalancer,
					ProviderParameters: &operatorv1.ProviderLoadBalancerParameters{
						Type: operatorv1.AWSLoadBalancerProvider,
						AWS: &operatorv1.AWSLoadBalancerParameters{
							Type: operatorv1.AWSNetworkLoadBalancer,
							NetworkLoadBalancerParameters: &operatorv1.AWSNetworkLoadBalancerParameters{
								EIPAllocations: []operatorv1.EIPAllocation{
									"eipalloc-1234567890abcdef1",
								},
							},
						},
					},
				},
			},
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.LoadBalancerServiceStrategyType,
						LoadBalancer: &operatorv1.LoadBalancerStrategy{
							Scope: operatorv1.ExternalLoadBalancer,
							ProviderParameters: &operatorv1.ProviderLoadBalancerParameters{
								Type: operatorv1.AWSLoadBalancerProvider,
								AWS: &operatorv1.AWSLoadBalancerParameters{
									Type: operatorv1.AWSNetworkLoadBalancer,
									NetworkLoadBalancerParameters: &operatorv1.AWSNetworkLoadBalancerParameters{
										EIPAllocations: []operatorv1.EIPAllocation{
											"eipalloc-1234567890abcdef1",
										},
									},
								},
							},
						},
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		{
			name:                   "Custom LoadBalancer strategy with OpenStack floating IP",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.OpenStackPlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         false,
			inputEndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.LoadBalancerServiceStrategyType,
				LoadBalancer: &operatorv1.LoadBalancerStrategy{
					ProviderParameters: &operatorv1.ProviderLoadBalancerParameters{
						Type: operatorv1.OpenStackLoadBalancerProvider,
						OpenStack: &operatorv1.OpenStackLoadBalancerParameters{
							FloatingIP: "10.0.0.100",
						},
					},
				},
			},
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.LoadBalancerServiceStrategyType,
						LoadBalancer: &operatorv1.LoadBalancerStrategy{
							ProviderParameters: &operatorv1.ProviderLoadBalancerParameters{
								Type: operatorv1.OpenStackLoadBalancerProvider,
								OpenStack: &operatorv1.OpenStackLoadBalancerParameters{
									FloatingIP: "10.0.0.100",
								},
							},
						},
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		{
			name:                   "Custom strategy ignores platform defaults and annotations on IBM Cloud UPI",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.IBMCloudPlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     true,
			inputIsPrivate:         false,
			inputEndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.LoadBalancerServiceStrategyType,
				LoadBalancer: &operatorv1.LoadBalancerStrategy{
					Scope: operatorv1.ExternalLoadBalancer,
				},
			},
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.LoadBalancerServiceStrategyType,
						LoadBalancer: &operatorv1.LoadBalancerStrategy{
							Scope: operatorv1.ExternalLoadBalancer,
						},
					},
					NodePlacement: &operatorv1.NodePlacement{
						Tolerations: []corev1.Toleration{
							{
								Key:   "dedicated",
								Value: "edge",
							},
						},
					},
				},
			},
		},
		{
			name:                   "Custom strategy ignores isPrivate annotation",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.AWSPlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         true,
			inputEndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.LoadBalancerServiceStrategyType,
				LoadBalancer: &operatorv1.LoadBalancerStrategy{
					Scope: operatorv1.ExternalLoadBalancer,
				},
			},
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.LoadBalancerServiceStrategyType,
						LoadBalancer: &operatorv1.LoadBalancerStrategy{
							Scope: operatorv1.ExternalLoadBalancer,
						},
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := ReconcileDefaultIngressController(tc.inputIngressController, tc.inputIngressDomain, tc.inputPlatformType, tc.inputReplicas, tc.inputIsIBMCloudUPI, tc.inputIsPrivate, tc.inputIsNLB, tc.inputLoadBalancerScope, tc.inputLoadBalancerIP, tc.inputEndpointPublishingStrategy)
			g.Expect(err).To(BeNil())
			g.Expect(tc.inputIngressController).To(BeEquivalentTo(tc.expectedIngressController))
		})
	}
}

// TestConfigurationPriority verifies the priority order of ingress endpoint publishing strategy configuration.
// This test specifically addresses review requirements:
// 1. User configuration priority: Custom endpointPublishingStrategy is not overridden by platform defaults or annotations
// 2. Annotation fallback: Private annotation works when no user configuration is provided
// 3. Platform defaults: Platform-specific behavior is preserved when no user configuration exists
func TestConfigurationPriority(t *testing.T) {
	fakeIngressDomain := "example.com"
	fakeInputReplicas := int32(3)

	testsCases := []struct {
		name                            string
		inputIngressController          *operatorv1.IngressController
		inputIngressDomain              string
		inputPlatformType               hyperv1.PlatformType
		inputReplicas                   int32
		inputIsIBMCloudUPI              bool
		inputIsPrivate                  bool
		inputIsNLB                      bool
		inputLoadBalancerScope          operatorv1.LoadBalancerScope
		inputLoadBalancerIP             string
		inputEndpointPublishingStrategy *operatorv1.EndpointPublishingStrategy
		expectedIngressController       *operatorv1.IngressController
	}{
		// Test 1: User configuration priority over platform defaults
		{
			name:                   "User configuration has priority: HostNetwork overrides AWS LoadBalancer default",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.AWSPlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         false,
			inputIsNLB:             false,
			inputEndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.HostNetworkStrategyType,
			},
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.HostNetworkStrategyType,
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		{
			name:                   "User configuration has priority: LoadBalancer overrides None platform HostNetwork default",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.NonePlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         false,
			inputIsNLB:             false,
			inputEndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.LoadBalancerServiceStrategyType,
				LoadBalancer: &operatorv1.LoadBalancerStrategy{
					Scope: operatorv1.ExternalLoadBalancer,
				},
			},
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.LoadBalancerServiceStrategyType,
						LoadBalancer: &operatorv1.LoadBalancerStrategy{
							Scope: operatorv1.ExternalLoadBalancer,
						},
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		// Test 2: User configuration priority over private annotation
		{
			name:                   "User configuration has priority: External LoadBalancer not overridden by private annotation on AWS",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.AWSPlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     false,
			inputIsPrivate:         true, // Private annotation set
			inputIsNLB:             false,
			inputEndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.LoadBalancerServiceStrategyType,
				LoadBalancer: &operatorv1.LoadBalancerStrategy{
					Scope: operatorv1.ExternalLoadBalancer,
				},
			},
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.LoadBalancerServiceStrategyType,
						LoadBalancer: &operatorv1.LoadBalancerStrategy{
							Scope: operatorv1.ExternalLoadBalancer,
						},
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		{
			name:                   "User configuration has priority: NodePort not overridden by private annotation on IBM Cloud UPI",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.IBMCloudPlatform,
			inputReplicas:          fakeInputReplicas,
			inputIsIBMCloudUPI:     true,
			inputIsPrivate:         true, // Private annotation set
			inputIsNLB:             false,
			inputEndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.LoadBalancerServiceStrategyType,
				LoadBalancer: &operatorv1.LoadBalancerStrategy{
					Scope: operatorv1.InternalLoadBalancer,
				},
			},
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.LoadBalancerServiceStrategyType,
						LoadBalancer: &operatorv1.LoadBalancerStrategy{
							Scope: operatorv1.InternalLoadBalancer,
						},
					},
					NodePlacement: &operatorv1.NodePlacement{
						Tolerations: []corev1.Toleration{
							{
								Key:   "dedicated",
								Value: "edge",
							},
						},
					},
				},
			},
		},
		// Test 3: Annotation fallback when no user configuration
		{
			name:                            "Annotation fallback: Private annotation applies when no user configuration on AWS",
			inputIngressController:          manifests.IngressDefaultIngressController(),
			inputIngressDomain:              fakeIngressDomain,
			inputPlatformType:               hyperv1.AWSPlatform,
			inputReplicas:                   fakeInputReplicas,
			inputIsIBMCloudUPI:              false,
			inputIsPrivate:                  true, // Private annotation set
			inputIsNLB:                      false,
			inputEndpointPublishingStrategy: nil, // No user configuration
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type:    operatorv1.PrivateStrategyType,
						Private: &operatorv1.PrivateStrategy{},
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		{
			name:                            "Annotation fallback: Private annotation applies when no user configuration on IBM Cloud",
			inputIngressController:          manifests.IngressDefaultIngressController(),
			inputIngressDomain:              fakeIngressDomain,
			inputPlatformType:               hyperv1.IBMCloudPlatform,
			inputReplicas:                   fakeInputReplicas,
			inputIsIBMCloudUPI:              false,
			inputIsPrivate:                  true, // Private annotation set
			inputLoadBalancerScope:          operatorv1.ExternalLoadBalancer,
			inputEndpointPublishingStrategy: nil, // No user configuration
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type:    operatorv1.PrivateStrategyType,
						Private: &operatorv1.PrivateStrategy{},
					},
					NodePlacement: &operatorv1.NodePlacement{
						Tolerations: []corev1.Toleration{
							{
								Key:   "dedicated",
								Value: "edge",
							},
						},
					},
				},
			},
		},
		// Test 4: Platform defaults when no user configuration and no annotation
		{
			name:                            "Platform defaults: AWS uses LoadBalancer when no user configuration",
			inputIngressController:          manifests.IngressDefaultIngressController(),
			inputIngressDomain:              fakeIngressDomain,
			inputPlatformType:               hyperv1.AWSPlatform,
			inputReplicas:                   fakeInputReplicas,
			inputIsIBMCloudUPI:              false,
			inputIsPrivate:                  false, // No private annotation
			inputIsNLB:                      false,
			inputEndpointPublishingStrategy: nil, // No user configuration
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.LoadBalancerServiceStrategyType,
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		{
			name:                            "Platform defaults: None platform uses HostNetwork when no user configuration",
			inputIngressController:          manifests.IngressDefaultIngressController(),
			inputIngressDomain:              fakeIngressDomain,
			inputPlatformType:               hyperv1.NonePlatform,
			inputReplicas:                   fakeInputReplicas,
			inputIsIBMCloudUPI:              false,
			inputIsPrivate:                  false, // No private annotation
			inputEndpointPublishingStrategy: nil,   // No user configuration
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.HostNetworkStrategyType,
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		{
			name:                            "Platform defaults: KubeVirt uses NodePort when no user configuration",
			inputIngressController:          manifests.IngressDefaultIngressController(),
			inputIngressDomain:              fakeIngressDomain,
			inputPlatformType:               hyperv1.KubevirtPlatform,
			inputReplicas:                   fakeInputReplicas,
			inputIsIBMCloudUPI:              false,
			inputIsPrivate:                  false, // No private annotation
			inputEndpointPublishingStrategy: nil,   // No user configuration
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.NodePortServiceStrategyType,
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
		{
			name:                            "Platform defaults: IBM Cloud UPI uses NodePort when no user configuration",
			inputIngressController:          manifests.IngressDefaultIngressController(),
			inputIngressDomain:              fakeIngressDomain,
			inputPlatformType:               hyperv1.IBMCloudPlatform,
			inputReplicas:                   fakeInputReplicas,
			inputIsIBMCloudUPI:              true,
			inputIsPrivate:                  false, // No private annotation
			inputEndpointPublishingStrategy: nil,   // No user configuration
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.NodePortServiceStrategyType,
						NodePort: &operatorv1.NodePortStrategy{
							Protocol: operatorv1.TCPProtocol,
						},
					},
					NodePlacement: &operatorv1.NodePlacement{
						Tolerations: []corev1.Toleration{
							{
								Key:   "dedicated",
								Value: "edge",
							},
						},
					},
				},
			},
		},
		{
			name:                            "Platform defaults: AWS with NLB flag uses NLB LoadBalancer when no user configuration",
			inputIngressController:          manifests.IngressDefaultIngressController(),
			inputIngressDomain:              fakeIngressDomain,
			inputPlatformType:               hyperv1.AWSPlatform,
			inputReplicas:                   fakeInputReplicas,
			inputIsIBMCloudUPI:              false,
			inputIsPrivate:                  false, // No private annotation
			inputIsNLB:                      true,  // NLB flag set
			inputEndpointPublishingStrategy: nil,   // No user configuration
			expectedIngressController: &operatorv1.IngressController{
				ObjectMeta: manifests.IngressDefaultIngressController().ObjectMeta,
				Spec: operatorv1.IngressControllerSpec{
					Domain:   fakeIngressDomain,
					Replicas: &fakeInputReplicas,
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.LoadBalancerServiceStrategyType,
						LoadBalancer: &operatorv1.LoadBalancerStrategy{
							ProviderParameters: &operatorv1.ProviderLoadBalancerParameters{
								Type: operatorv1.AWSLoadBalancerProvider,
								AWS: &operatorv1.AWSLoadBalancerParameters{
									Type:                          operatorv1.AWSNetworkLoadBalancer,
									NetworkLoadBalancerParameters: &operatorv1.AWSNetworkLoadBalancerParameters{},
								},
							},
						},
					},
					DefaultCertificate: &corev1.LocalObjectReference{
						Name: manifests.IngressDefaultIngressControllerCert().Name,
					},
				},
			},
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := ReconcileDefaultIngressController(tc.inputIngressController, tc.inputIngressDomain, tc.inputPlatformType, tc.inputReplicas, tc.inputIsIBMCloudUPI, tc.inputIsPrivate, tc.inputIsNLB, tc.inputLoadBalancerScope, tc.inputLoadBalancerIP, tc.inputEndpointPublishingStrategy)
			g.Expect(err).To(BeNil())
			g.Expect(tc.inputIngressController).To(BeEquivalentTo(tc.expectedIngressController))
		})
	}
}

func TestReconcileDefaultIngressControllerCertSecret(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		sourceSecret *corev1.Secret
		wantErr      bool
		errSubstr    string
	}{
		{
			name: "When source secret has both cert and key, it should succeed",
			sourceSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "source", Namespace: "test-ns"},
				Data: map[string][]byte{
					corev1.TLSCertKey:       []byte("cert-data"),
					corev1.TLSPrivateKeyKey: []byte("key-data"),
				},
			},
		},
		{
			name: "When source secret is missing the cert key, it should return an error",
			sourceSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "source", Namespace: "test-ns"},
				Data: map[string][]byte{
					corev1.TLSPrivateKeyKey: []byte("key-data"),
				},
			},
			wantErr:   true,
			errSubstr: "does not have a cert key",
		},
		{
			name: "When source secret is missing the private key, it should return an error",
			sourceSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "source", Namespace: "test-ns"},
				Data: map[string][]byte{
					corev1.TLSCertKey: []byte("cert-data"),
				},
			},
			wantErr:   true,
			errSubstr: "does not have the expected key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			certSecret := &corev1.Secret{}
			err := ReconcileDefaultIngressControllerCertSecret(certSecret, tt.sourceSecret)

			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(certSecret.Data).To(HaveKeyWithValue(corev1.TLSCertKey, tt.sourceSecret.Data[corev1.TLSCertKey]))
				g.Expect(certSecret.Data).To(HaveKeyWithValue(corev1.TLSPrivateKeyKey, tt.sourceSecret.Data[corev1.TLSPrivateKeyKey]))
			}
		})
	}
}

func TestEffectiveEndpointPublishingStrategy(t *testing.T) {
	hostNetwork := &operatorv1.EndpointPublishingStrategy{Type: operatorv1.HostNetworkStrategyType}
	nodePort := &operatorv1.EndpointPublishingStrategy{Type: operatorv1.NodePortServiceStrategyType}
	loadBalancer := &operatorv1.EndpointPublishingStrategy{Type: operatorv1.LoadBalancerServiceStrategyType}

	hcpWith := func(s *operatorv1.EndpointPublishingStrategy) *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{
			OperatorConfiguration: &hyperv1.OperatorConfiguration{
				IngressOperator: &hyperv1.IngressOperatorSpec{EndpointPublishingStrategy: s},
			},
		}}
	}

	tests := []struct {
		name              string
		ingressController *operatorv1.IngressController
		hcp               *hyperv1.HostedControlPlane
		expected          operatorv1.EndpointPublishingStrategyType
	}{
		{
			name:     "When ingress controller and hcp are nil, it should default to NodePortService",
			expected: operatorv1.NodePortServiceStrategyType,
		},
		{
			name:     "When ingress controller is nil and hcp has no configuration, it should default to NodePortService",
			hcp:      &hyperv1.HostedControlPlane{},
			expected: operatorv1.NodePortServiceStrategyType,
		},
		{
			name:     "When ingress controller is nil and hcp has a configuration, it should use the hcp strategy",
			hcp:      hcpWith(hostNetwork),
			expected: operatorv1.HostNetworkStrategyType,
		},
		{
			name: "When ingress controller spec and hcp have a strategy, it should prefer the ingress controller spec",
			ingressController: &operatorv1.IngressController{Spec: operatorv1.IngressControllerSpec{
				EndpointPublishingStrategy: loadBalancer,
			}},
			hcp:      hcpWith(hostNetwork),
			expected: operatorv1.LoadBalancerServiceStrategyType,
		},
		{
			name: "When ingress controller status and spec have a strategy, it should prefer the status",
			ingressController: &operatorv1.IngressController{
				Spec:   operatorv1.IngressControllerSpec{EndpointPublishingStrategy: nodePort},
				Status: operatorv1.IngressControllerStatus{EndpointPublishingStrategy: hostNetwork},
			},
			hcp:      hcpWith(loadBalancer),
			expected: operatorv1.HostNetworkStrategyType,
		},
		{
			name: "When ingress controller status has a strategy with empty type, it should fall through to the spec",
			ingressController: &operatorv1.IngressController{
				Spec:   operatorv1.IngressControllerSpec{EndpointPublishingStrategy: hostNetwork},
				Status: operatorv1.IngressControllerStatus{EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{}},
			},
			hcp:      hcpWith(loadBalancer),
			expected: operatorv1.HostNetworkStrategyType,
		},
		{
			name: "When ingress controller status and spec have strategies with empty type, it should fall through to the hcp",
			ingressController: &operatorv1.IngressController{
				Spec:   operatorv1.IngressControllerSpec{EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{}},
				Status: operatorv1.IngressControllerStatus{EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{}},
			},
			hcp:      hcpWith(hostNetwork),
			expected: operatorv1.HostNetworkStrategyType,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			got := EffectiveEndpointPublishingStrategy(tt.ingressController, tt.hcp)
			g.Expect(got).ToNot(BeNil())
			g.Expect(got.Type).To(Equal(tt.expected))
		})
	}
}

func TestReconcileDefaultIngressPassthroughService(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters-hc"},
		Spec:       hyperv1.HostedControlPlaneSpec{InfraID: "hc-infra"},
	}
	nodePortService := &corev1.Service{Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{
		{Name: "http", Port: 80, NodePort: 30080},
		{Name: "https", Port: 443, NodePort: 30443},
	}}}

	tests := []struct {
		name               string
		strategy           *operatorv1.EndpointPublishingStrategy
		nodePortService    *corev1.Service
		expectedTargetPort int32
		expectedErr        string
	}{
		{
			name:               "When strategy is NodePortService, it should target the https nodePort",
			strategy:           &operatorv1.EndpointPublishingStrategy{Type: operatorv1.NodePortServiceStrategyType},
			nodePortService:    nodePortService,
			expectedTargetPort: 30443,
		},
		{
			name:               "When strategy is nil, it should default to NodePortService and target the https nodePort",
			nodePortService:    nodePortService,
			expectedTargetPort: 30443,
		},
		{
			name:            "When strategy is NodePortService and the nodeport service is missing, it should fail",
			strategy:        &operatorv1.EndpointPublishingStrategy{Type: operatorv1.NodePortServiceStrategyType},
			nodePortService: nil,
			expectedErr:     "unable to detect default ingress NodePort https port",
		},
		{
			name:     "When strategy is NodePortService and the https port is missing, it should fail",
			strategy: &operatorv1.EndpointPublishingStrategy{Type: operatorv1.NodePortServiceStrategyType},
			nodePortService: &corev1.Service{Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{
				{Name: "http", Port: 80, NodePort: 30080},
			}}},
			expectedErr: "unable to detect default ingress NodePort https port",
		},
		{
			name:     "When strategy is NodePortService and the https port has no nodePort allocated, it should fail",
			strategy: &operatorv1.EndpointPublishingStrategy{Type: operatorv1.NodePortServiceStrategyType},
			nodePortService: &corev1.Service{Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{
				{Name: "https", Port: 443, NodePort: 0},
			}}},
			expectedErr: "unable to detect default ingress NodePort https port",
		},
		{
			name:               "When strategy is HostNetwork without ports, it should target port 443",
			strategy:           &operatorv1.EndpointPublishingStrategy{Type: operatorv1.HostNetworkStrategyType},
			expectedTargetPort: 443,
		},
		{
			name: "When strategy is HostNetwork with empty ports, it should target port 443",
			strategy: &operatorv1.EndpointPublishingStrategy{
				Type:        operatorv1.HostNetworkStrategyType,
				HostNetwork: &operatorv1.HostNetworkStrategy{},
			},
			expectedTargetPort: 443,
		},
		{
			name: "When strategy is HostNetwork with httpsPort, it should target the configured httpsPort",
			strategy: &operatorv1.EndpointPublishingStrategy{
				Type:        operatorv1.HostNetworkStrategyType,
				HostNetwork: &operatorv1.HostNetworkStrategy{HTTPPort: 80, HTTPSPort: 8443, StatsPort: 1936},
			},
			// The nodeport service must be ignored for HostNetwork.
			nodePortService:    nodePortService,
			expectedTargetPort: 8443,
		},
		{
			name:        "When strategy is unsupported, it should fail",
			strategy:    &operatorv1.EndpointPublishingStrategy{Type: operatorv1.LoadBalancerServiceStrategyType},
			expectedErr: "not supported for endpoint publishing strategy",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			service := manifests.IngressDefaultIngressPassthroughService("clusters-hc")
			err := ReconcileDefaultIngressPassthroughService(service, tt.nodePortService, tt.strategy, hcp)
			if tt.expectedErr != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.expectedErr))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(service.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
			g.Expect(service.Spec.Selector).To(BeEmpty())
			g.Expect(service.Labels).To(HaveKeyWithValue(hyperv1.InfraIDLabel, "hc-infra"))
			g.Expect(service.Spec.Ports).To(HaveLen(1))
			g.Expect(service.Spec.Ports[0].Name).To(Equal("https-443"))
			g.Expect(service.Spec.Ports[0].Port).To(Equal(int32(443)))
			g.Expect(service.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
			g.Expect(service.Spec.Ports[0].TargetPort.IntValue()).To(Equal(int(tt.expectedTargetPort)))
		})
	}
}
