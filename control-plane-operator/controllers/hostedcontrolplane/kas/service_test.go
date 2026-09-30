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
		name                   string
		platform               hyperv1.PlatformType
		strategy               hyperv1.ServicePublishingStrategy
		endpointAccess         hyperv1.AWSEndpointAccessType
		apiServerPort          int
		checkNLBAnnotation     bool
		wantNLBAnnotation      bool
		wantNLBAnnotationValue string
		svc_in                 corev1.Service
		svc_out                corev1.Service
		err                    error
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
			name:               "When creating an AWS Route service, it should not add the NLB annotation",
			platform:           hyperv1.AWSPlatform,
			strategy:           hyperv1.ServicePublishingStrategy{Type: hyperv1.Route},
			endpointAccess:     hyperv1.Public,
			apiServerPort:      1125,
			checkNLBAnnotation: true,
			wantNLBAnnotation:  false,
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
			name:                   "When creating a private AWS LoadBalancer service, it should seed the NLB annotation for future endpoint transitions",
			platform:               hyperv1.AWSPlatform,
			strategy:               hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer},
			endpointAccess:         hyperv1.Private,
			apiServerPort:          6443,
			checkNLBAnnotation:     true,
			wantNLBAnnotation:      true,
			wantNLBAnnotationValue: "nlb",
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       6443,
						TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "client"},
					},
				},
			}},
			err: nil,
		},
		{
			name:                   "When updating a legacy AWS Route service, it should preserve the NLB annotation",
			platform:               hyperv1.AWSPlatform,
			strategy:               hyperv1.ServicePublishingStrategy{Type: hyperv1.Route},
			endpointAccess:         hyperv1.Public,
			apiServerPort:          6443,
			checkNLBAnnotation:     true,
			wantNLBAnnotation:      true,
			wantNLBAnnotationValue: "nlb",
			svc_in: corev1.Service{ObjectMeta: v1.ObjectMeta{
				ResourceVersion: "1",
				Annotations: map[string]string{
					AWSNLBAnnotation: "nlb",
				},
			}, Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       6443,
						TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "client"},
					},
				},
			}},
			err: nil,
		},
		{
			name:               "When updating an AWS LoadBalancer service without an annotation, it should not add one",
			platform:           hyperv1.AWSPlatform,
			strategy:           hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer},
			endpointAccess:     hyperv1.Public,
			apiServerPort:      6443,
			checkNLBAnnotation: true,
			wantNLBAnnotation:  false,
			svc_in: corev1.Service{ObjectMeta: v1.ObjectMeta{
				ResourceVersion: "1",
				Annotations:     map[string]string{},
			}, Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeLoadBalancer,
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeLoadBalancer,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       6443,
						TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "client"},
					},
				},
			}},
			err: nil,
		},
		{
			name:                   "When updating an AWS service with a non-NLB annotation, it should preserve its value",
			platform:               hyperv1.AWSPlatform,
			strategy:               hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer},
			endpointAccess:         hyperv1.Public,
			apiServerPort:          6443,
			checkNLBAnnotation:     true,
			wantNLBAnnotation:      true,
			wantNLBAnnotationValue: "clb",
			svc_in: corev1.Service{ObjectMeta: v1.ObjectMeta{
				ResourceVersion: "1",
				Annotations: map[string]string{
					AWSNLBAnnotation: "clb",
				},
			}, Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeLoadBalancer,
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeLoadBalancer,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       6443,
						TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "client"},
					},
				},
			}},
			err: nil,
		},
		{
			name:     "When using an invalid publishing strategy, it should return an error",
			strategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.None},
			err:      fmt.Errorf("invalid publishing strategy for Kube API server service: None"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			platform := hyperv1.PlatformSpec{Type: tc.platform}
			if tc.endpointAccess != "" {
				platform.AWS = &hyperv1.AWSPlatformSpec{EndpointAccess: tc.endpointAccess}
			}
			hcp := hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{Platform: platform}}

			err := ReconcileService(&tc.svc_in, &tc.strategy, &v1.OwnerReference{}, tc.apiServerPort, []string{}, &hcp)

			g := NewWithT(t)
			if tc.err == nil {
				g.Expect(err).To(BeNil())
				g.Expect(tc.svc_in.Spec.Type).To(Equal(tc.svc_out.Spec.Type))
				g.Expect(tc.svc_in.Spec.Ports).To(Equal(tc.svc_out.Spec.Ports))
				if tc.checkNLBAnnotation {
					if tc.wantNLBAnnotation {
						g.Expect(tc.svc_in.Annotations).To(HaveKeyWithValue(AWSNLBAnnotation, tc.wantNLBAnnotationValue))
					} else {
						g.Expect(tc.svc_in.Annotations).ToNot(HaveKey(AWSNLBAnnotation))
					}
				}
			} else {
				g.Expect(tc.err.Error()).To(Equal(err.Error()))
			}
		})
	}
}

func TestReconcileServiceAzureInternalLB(t *testing.T) {
	// The main KAS service (kube-apiserver-azure-lb) should never have the internal LB
	// annotation. For Azure Private, this service becomes ClusterIP because isPublic=false.
	// The internal LB annotation is set on the separate kube-apiserver-private service
	// via ReconcilePrivateService instead.
	testCases := []struct {
		name     string
		topology hyperv1.AzureTopologyType
		strategy hyperv1.ServicePublishingStrategy
	}{
		{
			name:     "Azure Private endpoint does not set internal LB annotation on main service",
			topology: hyperv1.AzureTopologyPrivate,
			strategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer},
		},
		{
			name:     "Azure PublicAndPrivate endpoint does not set internal LB annotation on main service",
			topology: hyperv1.AzureTopologyPublicAndPrivate,
			strategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer},
		},
		{
			name:     "Azure Public endpoint does not set internal LB annotation",
			topology: hyperv1.AzureTopologyPublic,
			strategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer},
		},
		{
			name:     "Azure empty endpoint access does not set internal LB annotation",
			topology: "",
			strategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer},
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
							Topology: tc.topology,
						},
					},
				},
			}
			svc := &corev1.Service{}
			err := ReconcileService(svc, &tc.strategy, &v1.OwnerReference{}, 6443, []string{}, &hcp)
			g.Expect(err).To(BeNil())
			g.Expect(svc.Annotations).ToNot(HaveKey(azureutil.InternalLoadBalancerAnnotation))
		})
	}
}

func TestReconcileServiceAzurePIPAnnotation(t *testing.T) {
	// When an Azure (or KubeVirt-on-Azure) HCP with a public LB strategy has an infraID,
	// ReconcileService should set the azure-pip-name annotation to "{infraID}-kas-pip".
	// This forces the Azure cloud-provider to create a dedicated LB frontend, avoiding
	// port 6443 collision with the management cluster's KAS.
	testCases := []struct {
		name          string
		hcp           hyperv1.HostedControlPlane
		expectPIPName string
		expectNoPIP   bool
	}{
		{
			name: "Azure public HCP with infraID sets azure-pip-name",
			hcp: hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "my-infra-123",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							Topology: hyperv1.AzureTopologyPublic,
						},
					},
				},
			},
			expectPIPName: "my-infra-123-kas-pip",
		},
		{
			name: "KubeVirt-on-Azure public HCP sets azure-pip-name",
			hcp: hyperv1.HostedControlPlane{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.ManagementPlatformAnnotation: string(hyperv1.AzurePlatform),
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "kv-azure-456",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			},
			expectPIPName: "kv-azure-456-kas-pip",
		},
		{
			name: "AWS HCP does not set azure-pip-name",
			hcp: hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "aws-infra-789",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			expectNoPIP: true,
		},
		{
			name: "Azure HCP without infraID does not set azure-pip-name",
			hcp: hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							Topology: hyperv1.AzureTopologyPublic,
						},
					},
				},
			},
			expectNoPIP: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			svc := &corev1.Service{}
			strategy := hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}
			// Pass KASSVCLBAzurePort (7443) to simulate the production caller in
			// reconcileAPIServerService, which defaults to 7443 for Azure.
			err := ReconcileService(svc, &strategy, &v1.OwnerReference{}, config.KASSVCLBAzurePort, []string{}, &tc.hcp)
			g.Expect(err).To(BeNil(), "ReconcileService should not return an error for %s", tc.name)
			if tc.expectNoPIP {
				g.Expect(svc.Annotations).ToNot(HaveKey(azureutil.PIPNameAnnotation),
					"expected no azure-pip-name annotation for %s", tc.name)
			} else {
				g.Expect(svc.Annotations).To(HaveKeyWithValue(azureutil.PIPNameAnnotation, tc.expectPIPName),
					"expected azure-pip-name annotation for %s", tc.name)
				g.Expect(svc.Spec.Ports[0].Port).To(Equal(int32(config.KASSVCPort)),
					"expected port 6443 when azure-pip-name is set for %s", tc.name)
			}
		})
		// For PIP cases, also verify that port 6443 is preserved on update reconcile
		// (simulating a subsequent ReconcileService call on an existing Service).
		if !tc.expectNoPIP {
			t.Run(tc.name+" update preserves port 6443", func(t *testing.T) {
				g := NewWithT(t)
				// Simulate an existing Service with the PIP annotation already set.
				svc := &corev1.Service{
					ObjectMeta: v1.ObjectMeta{
						ResourceVersion: "1",
						Annotations: map[string]string{
							azureutil.PIPNameAnnotation: tc.expectPIPName,
						},
					},
					Spec: corev1.ServiceSpec{
						Ports: []corev1.ServicePort{{Port: int32(config.KASSVCPort)}},
					},
				}
				strategy := hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}
				err := ReconcileService(svc, &strategy, &v1.OwnerReference{}, config.KASSVCLBAzurePort, []string{}, &tc.hcp)
				g.Expect(err).To(BeNil(), "ReconcileService update should not return an error for %s", tc.name)
				g.Expect(svc.Annotations).To(HaveKeyWithValue(azureutil.PIPNameAnnotation, tc.expectPIPName),
					"azure-pip-name annotation should be preserved on update for %s", tc.name)
				g.Expect(svc.Spec.Ports[0].Port).To(Equal(int32(config.KASSVCPort)),
					"port 6443 should be preserved on update when azure-pip-name is set for %s", tc.name)
			})
		}
	}
}

func TestReconcilePrivateService(t *testing.T) {
	azureILBAnnotation := azureutil.InternalLoadBalancerAnnotation
	awsCrossZoneAnnotation := "service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled"
	awsLBAttributesAnnotation := "service.beta.kubernetes.io/aws-load-balancer-attributes"
	awsInternalAnnotation := "service.beta.kubernetes.io/aws-load-balancer-internal"
	awsNLBAnnotation := AWSNLBAnnotation

	testCases := []struct {
		name                    string
		hcp                     *hyperv1.HostedControlPlane
		expectAzureILB          bool
		expectAWSAnnotations    bool
		expectAWSNLBAnnotation  bool
		expectedPort            int32
		expectIPFamilyDualStack bool
	}{
		{
			name: "When Azure platform with Private endpoint access it should set ILB annotation and use port 7443",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							Topology: hyperv1.AzureTopologyPrivate,
						},
					},
				},
			},
			expectAzureILB:          true,
			expectAWSAnnotations:    false,
			expectedPort:            int32(config.KASSVCLBAzurePort),
			expectIPFamilyDualStack: true,
		},
		{
			name: "When Azure platform with PublicAndPrivate endpoint access it should set ILB annotation and use port 7443",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							Topology: hyperv1.AzureTopologyPublicAndPrivate,
						},
					},
				},
			},
			expectAzureILB:          true,
			expectAWSAnnotations:    false,
			expectedPort:            int32(config.KASSVCLBAzurePort),
			expectIPFamilyDualStack: true,
		},
		{
			name: "When Azure platform with Public endpoint access it should still set ILB annotation on private service and use port 7443",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							Topology: hyperv1.AzureTopologyPublic,
						},
					},
				},
			},
			expectAzureILB:          true,
			expectAWSAnnotations:    false,
			expectedPort:            int32(config.KASSVCLBAzurePort),
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
			expectAWSNLBAnnotation:  true,
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
				g.Expect(svc.Annotations).ToNot(HaveKey(awsLBAttributesAnnotation))
				g.Expect(svc.Annotations).ToNot(HaveKey(awsInternalAnnotation))
				g.Expect(svc.Annotations).ToNot(HaveKey(awsNLBAnnotation))
			}

			// Verify AWS annotations
			if tc.expectAWSAnnotations {
				g.Expect(svc.Annotations).To(HaveKeyWithValue(awsCrossZoneAnnotation, "true"))
				g.Expect(svc.Annotations).To(HaveKeyWithValue(awsLBAttributesAnnotation, "load_balancing.cross_zone.enabled=true"))
				g.Expect(svc.Annotations).To(HaveKeyWithValue(awsInternalAnnotation, "true"))
				if tc.expectAWSNLBAnnotation {
					g.Expect(svc.Annotations).To(HaveKeyWithValue(awsNLBAnnotation, "nlb"))
				} else {
					g.Expect(svc.Annotations).ToNot(HaveKey(awsNLBAnnotation))
				}
				// Non-Azure should NOT have Azure ILB annotation
				g.Expect(svc.Annotations).ToNot(HaveKey(azureILBAnnotation))
			}
		})
	}
}

func TestReconcilePrivateServiceAWSNLBAnnotation(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		resourceVersion string
		annotationValue string
		wantAnnotation  bool
		wantValue       string
	}{
		{
			name:           "When creating an AWS private service, it should set the NLB annotation",
			wantAnnotation: true,
			wantValue:      "nlb",
		},
		{
			name:            "When updating an AWS private service without an annotation, it should not add one",
			resourceVersion: "1",
			wantAnnotation:  false,
		},
		{
			name:            "When updating an AWS private service with a non-NLB annotation, it should preserve its value",
			resourceVersion: "1",
			annotationValue: "clb",
			wantAnnotation:  true,
			wantValue:       "clb",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			hcp := &hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
			}}
			svc := &corev1.Service{ObjectMeta: v1.ObjectMeta{
				ResourceVersion: tc.resourceVersion,
				Annotations:     map[string]string{},
			}}
			if tc.annotationValue != "" {
				svc.Annotations[AWSNLBAnnotation] = tc.annotationValue
			}

			err := ReconcilePrivateService(svc, hcp, &v1.OwnerReference{})
			g.Expect(err).ToNot(HaveOccurred())
			if tc.wantAnnotation {
				g.Expect(svc.Annotations).To(HaveKeyWithValue(AWSNLBAnnotation, tc.wantValue))
			} else {
				g.Expect(svc.Annotations).ToNot(HaveKey(AWSNLBAnnotation))
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
