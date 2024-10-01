package ingress

import (
	"testing"

	. "github.com/onsi/gomega"
	operatorv1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
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
			err := ReconcileDefaultIngressController(tc.inputIngressController, tc.inputIngressDomain, tc.inputPlatformType, tc.inputReplicas, tc.inputIsIBMCloudUPI, tc.inputIsPrivate, tc.inputIsNLB, tc.inputLoadBalancerScope, tc.inputLoadBalancerIP)
			g.Expect(err).To(BeNil())
			g.Expect(tc.inputIngressController).To(BeEquivalentTo(tc.expectedIngressController))
		})
	}
}
