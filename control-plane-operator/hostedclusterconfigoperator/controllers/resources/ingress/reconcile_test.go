package ingress

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"

	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
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

func TestReconcileDefaultIngressPassthroughService(t *testing.T) {
	t.Parallel()

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "guest", Namespace: "clusters-guest"},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
		},
	}

	tests := []struct {
		name            string
		defaultNodePort *corev1.Service
		wantErr         bool
		errSubstr       string
		validateService func(g Gomega, svc *corev1.Service)
	}{
		{
			name: "When NodePort service has both HTTP and HTTPS ports, it should configure both ports in the passthrough service",
			defaultNodePort: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Port: 80, NodePort: 32080},
						{Port: 443, NodePort: 32443},
					},
				},
			},
			validateService: func(g Gomega, svc *corev1.Service) {
				g.Expect(svc.Spec.Ports).To(HaveLen(2))
				g.Expect(svc.Spec.Ports).To(ContainElements(
					corev1.ServicePort{
						Name:       "https-443",
						Protocol:   corev1.ProtocolTCP,
						Port:       443,
						TargetPort: intstr.FromInt32(32443),
					},
					corev1.ServicePort{
						Name:       "http-80",
						Protocol:   corev1.ProtocolTCP,
						Port:       80,
						TargetPort: intstr.FromInt32(32080),
					},
				))
				g.Expect(svc.Spec.Selector).To(BeEmpty())
				g.Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
				g.Expect(svc.Labels).To(HaveKeyWithValue(hyperv1.InfraIDLabel, "test-infra-id"))
			},
		},
		{
			name: "When NodePort service is missing the HTTPS port, it should return an error",
			defaultNodePort: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Port: 80, NodePort: 32080},
					},
				},
			},
			wantErr:   true,
			errSubstr: "unable to detect default ingress NodePort https port",
		},
		{
			name: "When NodePort service is missing the HTTP port, it should succeed with only HTTPS port configured",
			defaultNodePort: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Port: 443, NodePort: 32443},
					},
				},
			},
			validateService: func(g Gomega, svc *corev1.Service) {
				g.Expect(svc.Spec.Ports).To(HaveLen(1))
				g.Expect(svc.Spec.Ports[0].Name).To(Equal("https-443"))
				g.Expect(svc.Spec.Ports[0].Port).To(Equal(int32(443)))
				g.Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
			},
		},
		{
			name: "When NodePort service has no ports, it should return an error for the missing HTTPS port",
			defaultNodePort: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{},
				},
			},
			wantErr:   true,
			errSubstr: "unable to detect default ingress NodePort https port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			svc := &corev1.Service{}
			err := ReconcileDefaultIngressPassthroughService(svc, tt.defaultNodePort, hcp)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				tt.validateService(g, svc)
			}
		})
	}
}

func TestReconcileDefaultIngressPassthroughHTTPRoute(t *testing.T) {
	t.Parallel()

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "guest", Namespace: "clusters-guest"},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
			DNS: hyperv1.DNSSpec{
				BaseDomain: "apps.mgmt.example.com",
			},
		},
	}

	cpService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "default-ingress-passthrough-service-abc123"},
	}

	tests := []struct {
		name          string
		hcp           *hyperv1.HostedControlPlane
		existingRoute *routev1.Route
		validateRoute func(g Gomega, route *routev1.Route)
	}{
		{
			name: "When HCP has baseDomainPassthrough enabled, it should create an HTTP wildcard route for insecure guest routes",
			hcp:  hcp,
			validateRoute: func(g Gomega, route *routev1.Route) {
				g.Expect(route.Spec.WildcardPolicy).To(Equal(routev1.WildcardPolicySubdomain))
				g.Expect(route.Spec.Host).To(Equal("apps.guest.apps.mgmt.example.com"))
				g.Expect(route.Spec.TLS).To(BeNil())
				g.Expect(route.Spec.Port).To(Equal(&routev1.RoutePort{
					TargetPort: intstr.FromString("http-80"),
				}))
				g.Expect(route.Spec.To.Name).To(Equal(cpService.Name))
				g.Expect(route.Labels).To(HaveKeyWithValue(hyperv1.InfraIDLabel, "test-infra-id"))
			},
		},
		{
			name: "When route already has TLS config, it should clear it to ensure plain HTTP",
			hcp:  hcp,
			existingRoute: &routev1.Route{
				Spec: routev1.RouteSpec{
					TLS: &routev1.TLSConfig{
						Termination: routev1.TLSTerminationEdge,
					},
				},
			},
			validateRoute: func(g Gomega, route *routev1.Route) {
				g.Expect(route.Spec.TLS).To(BeNil(), "pre-existing TLS config must be cleared on the HTTP route")
				g.Expect(route.Spec.Host).To(Equal("apps.guest.apps.mgmt.example.com"))
			},
		},
		{
			name: "When HCP has a different name and baseDomain, it should set the correct HTTP route host",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "mycluster", Namespace: "clusters-mycluster"},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "my-infra-id",
					DNS: hyperv1.DNSSpec{
						BaseDomain: "apps.production.example.com",
					},
				},
			},
			validateRoute: func(g Gomega, route *routev1.Route) {
				g.Expect(route.Spec.Host).To(Equal("apps.mycluster.apps.production.example.com"))
				g.Expect(route.Spec.WildcardPolicy).To(Equal(routev1.WildcardPolicySubdomain))
				g.Expect(route.Spec.TLS).To(BeNil())
				g.Expect(route.Labels).To(HaveKeyWithValue(hyperv1.InfraIDLabel, "my-infra-id"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			route := tt.existingRoute
			if route == nil {
				route = &routev1.Route{}
			}
			err := ReconcileDefaultIngressPassthroughHTTPRoute(route, cpService, tt.hcp)
			g.Expect(err).ToNot(HaveOccurred())
			tt.validateRoute(g, route)
		})
	}
}
