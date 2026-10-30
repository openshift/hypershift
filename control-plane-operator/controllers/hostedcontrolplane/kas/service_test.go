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
	"k8s.io/utils/ptr"
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

	t.Run("When passing serviceAnnotations and loadBalancerClass, it should apply them to the Service", testReconcileServiceAnnotationPassthrough)
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

	t.Run("When passing serviceAnnotations and loadBalancerClass, it should apply them to the private Service", testReconcilePrivateServiceAnnotationPassthrough)
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

// testReconcileServiceAnnotationPassthrough is invoked as a subtest of
// TestReconcileService to keep the one-to-one test-to-production-function rule.
func testReconcileServiceAnnotationPassthrough(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                  string
		platform              hyperv1.PlatformType
		endpointAccess        hyperv1.AWSEndpointAccessType
		strategy              hyperv1.ServicePublishingStrategy
		resourceVersion       string // non-empty = update, empty = create
		existingServiceType   corev1.ServiceType
		existingAnnotations   map[string]string
		wantAnnotations       map[string]string
		wantLoadBalancerClass *string
		wantServiceType       corev1.ServiceType
	}{
		{
			name:           "When creating a public LoadBalancer with serviceAnnotations it should apply them to the Service",
			platform:       hyperv1.AgentPlatform,
			endpointAccess: hyperv1.Public,
			strategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					ServiceAnnotations: map[string]string{
						"metallb.io/loadBalancerIPs": "10.1.1.100",
						"metallb.io/address-pool":    "production",
					},
				},
			},
			wantAnnotations: map[string]string{
				"metallb.io/loadBalancerIPs": "10.1.1.100",
				"metallb.io/address-pool":    "production",
			},
		},
		{
			name:           "When creating a public LoadBalancer with loadBalancerClass it should set it on the Service",
			platform:       hyperv1.AgentPlatform,
			endpointAccess: hyperv1.Public,
			strategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					LoadBalancerClass: "metallb.io/metallb",
				},
			},
			wantLoadBalancerClass: ptr.To("metallb.io/metallb"),
		},
		{
			name:            "When updating a LoadBalancer with serviceAnnotations it should apply them",
			platform:        hyperv1.AgentPlatform,
			endpointAccess:  hyperv1.Public,
			resourceVersion: "1",
			strategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					ServiceAnnotations: map[string]string{
						"metallb.io/loadBalancerIPs": "10.1.1.100",
					},
				},
			},
			wantAnnotations: map[string]string{
				"metallb.io/loadBalancerIPs": "10.1.1.100",
			},
		},
		{
			name:                "When updating a Service that is already a LoadBalancer without a class, it should not set loadBalancerClass",
			platform:            hyperv1.AgentPlatform,
			endpointAccess:      hyperv1.Public,
			resourceVersion:     "1",
			existingServiceType: corev1.ServiceTypeLoadBalancer,
			strategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					LoadBalancerClass: "metallb.io/metallb",
				},
			},
			wantLoadBalancerClass: nil,
			wantServiceType:       corev1.ServiceTypeLoadBalancer,
		},
		{
			name:           "When creating an AWS LoadBalancer with a serviceAnnotation that collides with CPO NLB annotation it should preserve CPO value",
			platform:       hyperv1.AWSPlatform,
			endpointAccess: hyperv1.Public,
			strategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					ServiceAnnotations: map[string]string{
						AWSNLBAnnotation: "clb",
					},
				},
			},
			// CPO writes "nlb" AFTER user annotations, so CPO wins.
			wantAnnotations: map[string]string{
				AWSNLBAnnotation: "nlb",
			},
		},
		{
			name:            "When updating an AWS LoadBalancer, it should ignore a user-supplied NLB annotation",
			platform:        hyperv1.AWSPlatform,
			endpointAccess:  hyperv1.Public,
			resourceVersion: "1",
			existingAnnotations: map[string]string{
				AWSNLBAnnotation: "nlb",
			},
			strategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					ServiceAnnotations: map[string]string{
						AWSNLBAnnotation: "clb",
					},
				},
			},
			// The admission policy forbids changing this annotation, so the
			// existing CPO value must survive the update.
			wantAnnotations: map[string]string{
				AWSNLBAnnotation: "nlb",
			},
		},
		{
			name:            "When reconciling a LoadBalancer with serviceAnnotations it should preserve external annotations",
			platform:        hyperv1.AgentPlatform,
			endpointAccess:  hyperv1.Public,
			resourceVersion: "1",
			existingAnnotations: map[string]string{
				"metallb.io/ip-allocated-from-pool": "production",
			},
			strategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					ServiceAnnotations: map[string]string{
						"metallb.io/loadBalancerIPs": "10.1.1.100",
					},
				},
			},
			wantAnnotations: map[string]string{
				"metallb.io/loadBalancerIPs":        "10.1.1.100",
				"metallb.io/ip-allocated-from-pool": "production",
			},
		},
		{
			name:           "When using a Route strategy with serviceAnnotations it should not apply them",
			platform:       hyperv1.AgentPlatform,
			endpointAccess: hyperv1.Public,
			strategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					ServiceAnnotations: map[string]string{
						"metallb.io/loadBalancerIPs": "10.1.1.100",
					},
				},
			},
			wantAnnotations: nil,
		},
		{
			name:            "When a user manually changes a serviceAnnotation value on the Service it should be overwritten by HC spec value",
			platform:        hyperv1.AgentPlatform,
			endpointAccess:  hyperv1.Public,
			resourceVersion: "1",
			existingAnnotations: map[string]string{
				"metallb.io/loadBalancerIPs": "10.1.1.999",
			},
			strategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					ServiceAnnotations: map[string]string{
						"metallb.io/loadBalancerIPs": "10.1.1.100",
					},
				},
			},
			wantAnnotations: map[string]string{
				"metallb.io/loadBalancerIPs": "10.1.1.100",
			},
		},
		{
			name:           "When the cluster is private-only, it should not apply loadBalancerClass to the ClusterIP Service",
			platform:       hyperv1.AWSPlatform,
			endpointAccess: hyperv1.Private,
			strategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					LoadBalancerClass: "metallb.io/metallb",
					ServiceAnnotations: map[string]string{
						"metallb.io/loadBalancerIPs": "10.1.1.100",
					},
				},
			},
			wantAnnotations:       nil,
			wantLoadBalancerClass: nil,
			wantServiceType:       corev1.ServiceTypeClusterIP,
		},
		{
			name:           "When the cluster has public and private endpoints, it should apply the deterministic LB fields to the public Service",
			platform:       hyperv1.AWSPlatform,
			endpointAccess: hyperv1.PublicAndPrivate,
			strategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					LoadBalancerClass: "metallb.io/metallb",
					ServiceAnnotations: map[string]string{
						"metallb.io/loadBalancerIPs": "10.1.1.100",
					},
				},
			},
			wantAnnotations: map[string]string{
				"metallb.io/loadBalancerIPs": "10.1.1.100",
			},
			wantLoadBalancerClass: ptr.To("metallb.io/metallb"),
			wantServiceType:       corev1.ServiceTypeLoadBalancer,
		},
		{
			name:                "When a private cluster transitions to public, it should set loadBalancerClass on the existing Service",
			platform:            hyperv1.AWSPlatform,
			endpointAccess:      hyperv1.PublicAndPrivate,
			resourceVersion:     "1",
			existingServiceType: corev1.ServiceTypeClusterIP,
			strategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					LoadBalancerClass: "metallb.io/metallb",
				},
			},
			wantLoadBalancerClass: ptr.To("metallb.io/metallb"),
			wantServiceType:       corev1.ServiceTypeLoadBalancer,
		},
		{
			name:                "When the Service already exists as a LoadBalancer without a class, it should not set it",
			platform:            hyperv1.AWSPlatform,
			endpointAccess:      hyperv1.PublicAndPrivate,
			resourceVersion:     "1",
			existingServiceType: corev1.ServiceTypeLoadBalancer,
			strategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					LoadBalancerClass: "metallb.io/metallb",
				},
			},
			wantLoadBalancerClass: nil,
			wantServiceType:       corev1.ServiceTypeLoadBalancer,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			svc := &corev1.Service{
				ObjectMeta: v1.ObjectMeta{
					ResourceVersion: tc.resourceVersion,
				},
				Spec: corev1.ServiceSpec{
					Type: tc.existingServiceType,
				},
			}
			if tc.existingAnnotations != nil {
				svc.Annotations = tc.existingAnnotations
			}

			platform := hyperv1.PlatformSpec{Type: tc.platform}
			if tc.endpointAccess != "" && tc.platform == hyperv1.AWSPlatform {
				platform.AWS = &hyperv1.AWSPlatformSpec{EndpointAccess: tc.endpointAccess}
			}
			hcp := hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{Platform: platform}}

			err := ReconcileService(svc, &tc.strategy, &v1.OwnerReference{}, 6443, []string{}, &hcp)
			g.Expect(err).ToNot(HaveOccurred())

			for k, v := range tc.wantAnnotations {
				g.Expect(svc.Annotations).To(HaveKeyWithValue(k, v), "expected annotation %s=%s", k, v)
			}

			if tc.wantLoadBalancerClass != nil {
				g.Expect(svc.Spec.LoadBalancerClass).To(Equal(tc.wantLoadBalancerClass))
			} else {
				g.Expect(svc.Spec.LoadBalancerClass).To(BeNil())
			}

			if tc.wantServiceType != "" {
				g.Expect(svc.Spec.Type).To(Equal(tc.wantServiceType))
			}

			// Verify Route strategy didn't apply LB annotations
			if tc.strategy.Type == hyperv1.Route && tc.wantAnnotations == nil {
				g.Expect(svc.Annotations).ToNot(HaveKey("metallb.io/loadBalancerIPs"))
			}
		})
	}
}

// testReconcilePrivateServiceAnnotationPassthrough is invoked as a subtest of
// TestReconcilePrivateService to keep the one-to-one test-to-production-function rule.
func testReconcilePrivateServiceAnnotationPassthrough(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                  string
		platform              hyperv1.PlatformType
		endpointAccess        hyperv1.AWSEndpointAccessType
		serviceAnnotations    map[string]string
		loadBalancerClass     string
		resourceVersion       string
		existingAnnotations   map[string]string
		wantAnnotations       map[string]string
		notWantAnnotations    []string
		wantLoadBalancerClass *string
	}{
		{
			name:     "When creating an AWS private service with serviceAnnotations it should apply them",
			platform: hyperv1.AWSPlatform,
			serviceAnnotations: map[string]string{
				"service.beta.kubernetes.io/aws-load-balancer-eip-allocations": "eipalloc-abc123",
			},
			wantAnnotations: map[string]string{
				"service.beta.kubernetes.io/aws-load-balancer-eip-allocations": "eipalloc-abc123",
			},
		},
		{
			name:                  "When creating a private service with loadBalancerClass it should set it",
			platform:              hyperv1.AWSPlatform,
			endpointAccess:        hyperv1.Private,
			loadBalancerClass:     "metallb.io/metallb",
			wantLoadBalancerClass: ptr.To("metallb.io/metallb"),
		},
		{
			name:                  "When updating a private service with loadBalancerClass it should not set it",
			platform:              hyperv1.AWSPlatform,
			endpointAccess:        hyperv1.Private,
			resourceVersion:       "1",
			loadBalancerClass:     "metallb.io/metallb",
			wantLoadBalancerClass: nil,
		},
		{
			name:     "When creating a private service with serviceAnnotations it should preserve CPO platform annotations",
			platform: hyperv1.AWSPlatform,
			serviceAnnotations: map[string]string{
				"metallb.io/loadBalancerIPs": "10.1.1.100",
			},
			wantAnnotations: map[string]string{
				"metallb.io/loadBalancerIPs": "10.1.1.100",
				// CPO's own AWS annotations must also be present
				"service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled": "true",
				"service.beta.kubernetes.io/aws-load-balancer-internal":                          "true",
			},
		},
		{
			name:            "When reconciling a private service it should preserve external annotations not in serviceAnnotations",
			platform:        hyperv1.AWSPlatform,
			endpointAccess:  hyperv1.Private,
			resourceVersion: "1",
			existingAnnotations: map[string]string{
				"metallb.io/ip-allocated-from-pool": "production",
			},
			serviceAnnotations: map[string]string{
				"metallb.io/loadBalancerIPs": "10.1.1.100",
			},
			wantAnnotations: map[string]string{
				"metallb.io/loadBalancerIPs":        "10.1.1.100",
				"metallb.io/ip-allocated-from-pool": "production",
			},
		},
		{
			name:           "When the cluster also has a public endpoint, it should apply loadBalancerClass but not the address annotations",
			platform:       hyperv1.AWSPlatform,
			endpointAccess: hyperv1.PublicAndPrivate,
			serviceAnnotations: map[string]string{
				"metallb.io/loadBalancerIPs": "10.1.1.100",
			},
			loadBalancerClass: "metallb.io/metallb",
			wantAnnotations: map[string]string{
				// CPO's own AWS private annotations are still applied.
				"service.beta.kubernetes.io/aws-load-balancer-internal": "true",
			},
			notWantAnnotations:    []string{"metallb.io/loadBalancerIPs"},
			wantLoadBalancerClass: ptr.To("metallb.io/metallb"),
		},
		{
			name:            "When a private cluster gains a public endpoint, it should remove the address annotations from the private Service",
			platform:        hyperv1.AWSPlatform,
			endpointAccess:  hyperv1.PublicAndPrivate,
			resourceVersion: "1",
			existingAnnotations: map[string]string{
				// Written while the cluster was private-only and this Service was
				// the primary endpoint.
				"metallb.io/loadBalancerIPs":        "10.1.1.100",
				"metallb.io/ip-allocated-from-pool": "production",
			},
			serviceAnnotations: map[string]string{
				"metallb.io/loadBalancerIPs": "10.1.1.100",
			},
			wantAnnotations: map[string]string{
				// Keys outside serviceAnnotations are still left alone.
				"metallb.io/ip-allocated-from-pool": "production",
			},
			notWantAnnotations:    []string{"metallb.io/loadBalancerIPs"},
			wantLoadBalancerClass: nil,
		},
		{
			name:            "When updating an AWS private Service, it should ignore a user-supplied NLB annotation",
			platform:        hyperv1.AWSPlatform,
			endpointAccess:  hyperv1.Private,
			resourceVersion: "1",
			existingAnnotations: map[string]string{
				AWSNLBAnnotation: "nlb",
			},
			serviceAnnotations: map[string]string{
				AWSNLBAnnotation: "clb",
			},
			// The admission policy forbids changing this annotation, so the
			// existing CPO value must survive the update.
			wantAnnotations: map[string]string{
				AWSNLBAnnotation: "nlb",
			},
		},
		{
			name:            "When a private cluster gains a public endpoint, it should keep the CPO NLB annotation",
			platform:        hyperv1.AWSPlatform,
			endpointAccess:  hyperv1.PublicAndPrivate,
			resourceVersion: "1",
			existingAnnotations: map[string]string{
				AWSNLBAnnotation: "nlb",
			},
			serviceAnnotations: map[string]string{
				AWSNLBAnnotation: "clb",
			},
			// Releasing the address must not remove the operator-managed NLB
			// type annotation; the admission policy rejects that update.
			wantAnnotations: map[string]string{
				AWSNLBAnnotation: "nlb",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			svc := &corev1.Service{
				ObjectMeta: v1.ObjectMeta{
					ResourceVersion: tc.resourceVersion,
				},
			}
			if tc.existingAnnotations != nil {
				svc.Annotations = tc.existingAnnotations
			}

			platform := hyperv1.PlatformSpec{Type: tc.platform}
			if tc.endpointAccess != "" {
				platform.AWS = &hyperv1.AWSPlatformSpec{EndpointAccess: tc.endpointAccess}
			}

			hcp := &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: platform,
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
								LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
									ServiceAnnotations: tc.serviceAnnotations,
									LoadBalancerClass:  tc.loadBalancerClass,
								},
							},
						},
					},
				},
			}

			err := ReconcilePrivateService(svc, hcp, &v1.OwnerReference{})
			g.Expect(err).ToNot(HaveOccurred())

			for k, v := range tc.wantAnnotations {
				g.Expect(svc.Annotations).To(HaveKeyWithValue(k, v), "expected annotation %s=%s", k, v)
			}
			for _, k := range tc.notWantAnnotations {
				g.Expect(svc.Annotations).ToNot(HaveKey(k), "unexpected annotation %s", k)
			}

			if tc.wantLoadBalancerClass != nil {
				g.Expect(svc.Spec.LoadBalancerClass).To(Equal(tc.wantLoadBalancerClass))
			} else {
				g.Expect(svc.Spec.LoadBalancerClass).To(BeNil())
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
