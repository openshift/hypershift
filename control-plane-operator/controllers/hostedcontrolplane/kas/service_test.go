package kas

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestReconcileService(t *testing.T) {

	testCases := []struct {
		name          string
		platform      hyperv1.PlatformType
		strategy      hyperv1.ServicePublishingStrategy
		apiServerPort int
		svc_in        corev1.Service
		svc_out       corev1.Service
		err           error
	}{
		{
			name:          "IBM Cloud, NodePort strategy, NodePort service, expected to fill port number from strategy",
			platform:      hyperv1.IBMCloudPlatform,
			strategy:      hyperv1.ServicePublishingStrategy{Type: hyperv1.NodePort, NodePort: &hyperv1.NodePortPublishingStrategy{Port: 31125}},
			apiServerPort: 1125,
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol: corev1.ProtocolTCP,
						Port:     1125,
					},
				},
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       1125,
						TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "client"},
						NodePort:   31125,
					},
				},
			}},
			err: nil,
		},
		{
			name:          "IBM Cloud, Route strategy, NodePort service with existing port number, expected not to change",
			platform:      hyperv1.IBMCloudPlatform,
			strategy:      hyperv1.ServicePublishingStrategy{Type: hyperv1.Route},
			apiServerPort: 1125,
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol: corev1.ProtocolTCP,
						Port:     1125,
						NodePort: 1125,
					},
				},
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       1125,
						TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "client"},
						NodePort:   1125,
					},
				},
			}},
			err: nil,
		},
		{
			name:          "Non-IBM Cloud, Route strategy, ClusterIP service, expected to fill port value only",
			platform:      hyperv1.AWSPlatform,
			strategy:      hyperv1.ServicePublishingStrategy{Type: hyperv1.Route},
			apiServerPort: 1125,
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       1125,
						TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "client"},
					},
				},
			}},
			err: nil,
		},
		{
			name:     "Invalid strategy",
			strategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.None},
			err:      fmt.Errorf("invalid publishing strategy for Kube API server service: None"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp := hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{Platform: hyperv1.PlatformSpec{Type: tc.platform}}}

			err := ReconcileService(&tc.svc_in, &tc.strategy, &v1.OwnerReference{}, tc.apiServerPort, []string{}, &hcp)

			g := NewWithT(t)
			if tc.err == nil {
				g.Expect(err).To(BeNil())
				g.Expect(tc.svc_in.Spec.Type).To(Equal(tc.svc_out.Spec.Type))
				g.Expect(tc.svc_in.Spec.Ports).To(Equal(tc.svc_out.Spec.Ports))
			} else {
				g.Expect(tc.err.Error()).To(Equal(err.Error()))
			}
		})
	}
}

func TestReconcileServiceAzureInternalLB(t *testing.T) {
	testCases := []struct {
		name             string
		endpointAccess   hyperv1.AzureEndpointAccessType
		expectAnnotation bool
		strategy         hyperv1.ServicePublishingStrategy
	}{
		{
			name:             "Azure Private endpoint sets internal LB annotation",
			endpointAccess:   hyperv1.AzureEndpointAccessPrivate,
			expectAnnotation: true,
			strategy:         hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer},
		},
		{
			name:             "Azure PublicAndPrivate endpoint does not set internal LB annotation on main service",
			endpointAccess:   hyperv1.AzureEndpointAccessPublicAndPrivate,
			expectAnnotation: false,
			strategy:         hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer},
		},
		{
			name:             "Azure Public endpoint does not set internal LB annotation",
			endpointAccess:   hyperv1.AzureEndpointAccessPublic,
			expectAnnotation: false,
			strategy:         hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer},
		},
		{
			name:             "Azure empty endpoint access does not set internal LB annotation",
			endpointAccess:   "",
			expectAnnotation: false,
			strategy:         hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			hcp := hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							EndpointAccess: tc.endpointAccess,
						},
					},
				},
			}
			svc := &corev1.Service{}
			err := ReconcileService(svc, &tc.strategy, &v1.OwnerReference{}, 6443, []string{}, &hcp)
			g.Expect(err).To(BeNil())
			if tc.expectAnnotation {
				g.Expect(svc.Annotations).To(HaveKeyWithValue(azureutil.InternalLoadBalancerAnnotation, azureutil.InternalLoadBalancerValue))
			} else {
				g.Expect(svc.Annotations).ToNot(HaveKey(azureutil.InternalLoadBalancerAnnotation))
			}
		})
	}
}

func TestReconcilePrivateService(t *testing.T) {
	azureILBAnnotation := azureutil.InternalLoadBalancerAnnotation
	awsCrossZoneAnnotation := "service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled"
	awsInternalAnnotation := "service.beta.kubernetes.io/aws-load-balancer-internal"
	awsNLBAnnotation := "service.beta.kubernetes.io/aws-load-balancer-type"

	testCases := []struct {
		name                    string
		hcp                     *hyperv1.HostedControlPlane
		expectAzureILB          bool
		expectAWSAnnotations    bool
		expectedPort            int32
		expectIPFamilyDualStack bool
	}{
		{
			name: "When Azure platform with Private endpoint access it should set ILB annotation",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							EndpointAccess: hyperv1.AzureEndpointAccessPrivate,
						},
					},
				},
			},
			expectAzureILB:          true,
			expectAWSAnnotations:    false,
			expectedPort:            int32(config.KASSVCPort),
			expectIPFamilyDualStack: true,
		},
		{
			name: "When Azure platform with PublicAndPrivate endpoint access it should set ILB annotation",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							EndpointAccess: hyperv1.AzureEndpointAccessPublicAndPrivate,
						},
					},
				},
			},
			expectAzureILB:          true,
			expectAWSAnnotations:    false,
			expectedPort:            int32(config.KASSVCPort),
			expectIPFamilyDualStack: true,
		},
		{
			name: "When Azure platform with Public endpoint access it should still set ILB annotation on private service",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							EndpointAccess: hyperv1.AzureEndpointAccessPublic,
						},
					},
				},
			},
			expectAzureILB:          true,
			expectAWSAnnotations:    false,
			expectedPort:            int32(config.KASSVCPort),
			expectIPFamilyDualStack: true,
		},
		{
			name: "When AWS platform it should set AWS annotations and not Azure ILB annotation",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			expectAzureILB:          false,
			expectAWSAnnotations:    true,
			expectedPort:            int32(config.KASSVCPort),
			expectIPFamilyDualStack: true,
		},
		{
			name: "When IBM Cloud platform it should set AWS-style annotations and use IBM Cloud port",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.IBMCloudPlatform,
					},
				},
			},
			expectAzureILB:          false,
			expectAWSAnnotations:    true,
			expectedPort:            int32(config.KASSVCIBMCloudPort),
			expectIPFamilyDualStack: true,
		},
		{
			name: "When KubeVirt platform it should set AWS-style annotations and preserve baseline behavior",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			},
			expectAzureILB:          false,
			expectAWSAnnotations:    true,
			expectedPort:            int32(config.KASSVCPort),
			expectIPFamilyDualStack: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			svc := &corev1.Service{}
			owner := &v1.OwnerReference{Name: "test-hcp"}

			err := ReconcilePrivateService(svc, tc.hcp, owner)
			g.Expect(err).To(BeNil())

			// Verify service type is always LoadBalancer for private service
			g.Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeLoadBalancer))

			// Verify port configuration
			g.Expect(svc.Spec.Ports).To(HaveLen(1))
			g.Expect(svc.Spec.Ports[0].Port).To(Equal(tc.expectedPort))
			g.Expect(svc.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
			g.Expect(svc.Spec.Ports[0].TargetPort).To(Equal(intstr.FromString("client")))

			// Verify IP family policy
			if tc.expectIPFamilyDualStack {
				g.Expect(svc.Spec.IPFamilyPolicy).ToNot(BeNil())
				g.Expect(*svc.Spec.IPFamilyPolicy).To(Equal(corev1.IPFamilyPolicyPreferDualStack))
			}

			// Verify selector
			g.Expect(svc.Spec.Selector).To(Equal(kasLabels()))

			// Verify Azure ILB annotation
			if tc.expectAzureILB {
				g.Expect(svc.Annotations).To(HaveKeyWithValue(azureILBAnnotation, "true"))
				// Azure should NOT have AWS annotations
				g.Expect(svc.Annotations).ToNot(HaveKey(awsCrossZoneAnnotation))
				g.Expect(svc.Annotations).ToNot(HaveKey(awsInternalAnnotation))
				g.Expect(svc.Annotations).ToNot(HaveKey(awsNLBAnnotation))
			}

			// Verify AWS annotations
			if tc.expectAWSAnnotations {
				g.Expect(svc.Annotations).To(HaveKeyWithValue(awsCrossZoneAnnotation, "true"))
				g.Expect(svc.Annotations).To(HaveKeyWithValue(awsInternalAnnotation, "true"))
				g.Expect(svc.Annotations).To(HaveKeyWithValue(awsNLBAnnotation, "nlb"))
				// Non-Azure should NOT have Azure ILB annotation
				g.Expect(svc.Annotations).ToNot(HaveKey(azureILBAnnotation))
			}
		})
	}
}

func TestKonnectivityServiceReconcile(t *testing.T) {
	// Define common inputs

	testCases := []struct {
		name     string
		platform hyperv1.PlatformType
		strategy hyperv1.ServicePublishingStrategy
		svc_in   corev1.Service
		svc_out  corev1.Service
		err      error
	}{
		{
			name:     "IBM Cloud, NodePort strategy, NodePort service, expected to fill port number from strategy",
			platform: hyperv1.IBMCloudPlatform,
			strategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.NodePort, NodePort: &hyperv1.NodePortPublishingStrategy{Port: 1125}},
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       8091,
						TargetPort: intstr.IntOrString{IntVal: 8091},
						NodePort:   1125,
					},
				},
			}},
			err: nil,
		},
		{
			name:     "IBM Cloud, Route strategy, NodePort service with existing port number, expected not to change",
			platform: hyperv1.IBMCloudPlatform,
			strategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route},
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       8091,
						TargetPort: intstr.IntOrString{IntVal: 8091},
						NodePort:   1125,
					},
				},
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       8091,
						TargetPort: intstr.IntOrString{IntVal: 8091},
						NodePort:   1125,
					},
				},
			}},
			err: nil,
		},
		{
			name:     "Non-IBM Cloud, Route strategy, ClusterIP service, expected to fill port value only",
			platform: hyperv1.AWSPlatform,
			strategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route},
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       8091,
						TargetPort: intstr.IntOrString{IntVal: 8091},
					},
				},
			}},
			err: nil,
		},
		{
			name:     "Invalid strategy",
			strategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.None},
			err:      fmt.Errorf("invalid publishing strategy for Konnectivity service: None"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp := hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{Platform: hyperv1.PlatformSpec{Type: tc.platform}}}

			err := ReconcileKonnectivityServerService(&tc.svc_in, config.OwnerRef{}, &tc.strategy, &hcp)

			g := NewWithT(t)
			if tc.err == nil {
				g.Expect(err).To(BeNil())
				g.Expect(tc.svc_in.Spec.Type).To(Equal(tc.svc_out.Spec.Type))
				g.Expect(tc.svc_in.Spec.Ports).To(Equal(tc.svc_out.Spec.Ports))
			} else {
				g.Expect(tc.err.Error()).To(Equal(err.Error()))
			}
		})
	}
}
