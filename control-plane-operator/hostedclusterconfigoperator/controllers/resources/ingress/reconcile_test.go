package ingress

import (
	"testing"

	. "github.com/onsi/gomega"
	operatorv1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	corev1 "k8s.io/api/core/v1"
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
			name:                   "IBM Cloud Non-UPI uses LoadBalancer publishing strategy",
			inputIngressController: manifests.IngressDefaultIngressController(),
			inputIngressDomain:     fakeIngressDomain,
			inputPlatformType:      hyperv1.IBMCloudPlatform,
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
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := ReconcileDefaultIngressController(tc.inputIngressController, tc.inputIngressDomain, tc.inputPlatformType, tc.inputReplicas, tc.inputIsIBMCloudUPI, tc.inputIsPrivate)
			g.Expect(err).To(BeNil())
			g.Expect(tc.inputIngressController).To(BeEquivalentTo(tc.expectedIngressController))
		})
	}
}
