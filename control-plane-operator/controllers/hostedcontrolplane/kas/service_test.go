package kas

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/events"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
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

type fakeMessageCollector struct{}

func (c *fakeMessageCollector) ErrorMessages(resource crclient.Object) ([]string, error) {
	return nil, nil
}

var _ events.MessageCollector = &fakeMessageCollector{}

func TestReconcileServiceStatus(t *testing.T) {
	testCases := []struct {
		name            string
		svc             *corev1.Service
		strategy        *hyperv1.ServicePublishingStrategy
		apiServerPort   int
		expectedHost    string
		expectedPort    int32
		expectedMessage string
		expectedErr     error
	}{
		{
			name: "When LoadBalancer strategy has a configured hostname, it should return the hostname without waiting for LB provisioning",
			svc: &corev1.Service{
				ObjectMeta: v1.ObjectMeta{
					CreationTimestamp: v1.NewTime(time.Now().Add(-5 * time.Minute)),
				},
				// No LoadBalancer ingress status — LB not yet provisioned
			},
			strategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					Hostname: "kube-apiserver.my-hcp-ns.svc.cluster.local",
				},
			},
			apiServerPort: config.KASSVCPort,
			expectedHost:  "kube-apiserver.my-hcp-ns.svc.cluster.local",
			expectedPort:  int32(config.KASSVCPort),
		},
		{
			name: "When LoadBalancer strategy has no configured hostname and LB is not provisioned, it should return a message",
			svc: &corev1.Service{
				ObjectMeta: v1.ObjectMeta{
					CreationTimestamp: v1.NewTime(time.Now().Add(-5 * time.Minute)),
				},
			},
			strategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
			},
			apiServerPort:   config.KASSVCPort,
			expectedHost:    "",
			expectedPort:    0,
			expectedMessage: "load balancer is not provisioned",
		},
		{
			name: "When LoadBalancer strategy has no configured hostname and LB has ingress hostname, it should return the LB hostname",
			svc: &corev1.Service{
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{Hostname: "kas.test.elb.amazonaws.com"},
						},
					},
				},
			},
			strategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
			},
			apiServerPort: config.KASSVCPort,
			expectedHost:  "kas.test.elb.amazonaws.com",
			expectedPort:  int32(config.KASSVCPort),
		},
		{
			name: "When LoadBalancer strategy has no configured hostname and LB has ingress IP, it should return the LB IP",
			svc: &corev1.Service{
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{IP: "10.0.0.1"},
						},
					},
				},
			},
			strategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
			},
			apiServerPort: config.KASSVCPort,
			expectedHost:  "10.0.0.1",
			expectedPort:  int32(config.KASSVCPort),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			host, port, message, err := ReconcileServiceStatus(tc.svc, tc.strategy, tc.apiServerPort, &fakeMessageCollector{})
			if tc.expectedErr != nil {
				g.Expect(err).To(MatchError(tc.expectedErr))
			} else {
				g.Expect(err).To(BeNil())
			}
			g.Expect(host).To(Equal(tc.expectedHost))
			g.Expect(port).To(Equal(tc.expectedPort))
			if tc.expectedMessage != "" {
				g.Expect(message).To(ContainSubstring(tc.expectedMessage))
			} else {
				g.Expect(message).To(BeEmpty())
			}
		})
	}
}

func TestReconcileKonnectivityServerServiceStatus(t *testing.T) {
	testCases := []struct {
		name            string
		svc             *corev1.Service
		route           *routev1.Route
		strategy        *hyperv1.ServicePublishingStrategy
		expectedHost    string
		expectedPort    int32
		expectedMessage string
		expectedErr     error
	}{
		{
			name: "When Route strategy has Spec.Host set, it should return Spec.Host and port 443",
			svc:  &corev1.Service{},
			route: &routev1.Route{
				Spec: routev1.RouteSpec{
					Host: "konnectivity.example.com",
				},
			},
			strategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
			},
			expectedHost: "konnectivity.example.com",
			expectedPort: 443,
		},
		{
			name:  "When Route strategy has configured hostname, it should return the configured hostname and port 443",
			svc:   &corev1.Service{},
			route: &routev1.Route{},
			strategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{
					Hostname: "konnectivity.configured.example.com",
				},
			},
			expectedHost: "konnectivity.configured.example.com",
			expectedPort: 443,
		},
		{
			name: "When Route strategy has Spec.Host empty but Status.Ingress[0].RouterCanonicalHostname is set, it should return empty host because RouterCanonicalHostname is not route-specific",
			svc:  &corev1.Service{},
			route: &routev1.Route{
				Status: routev1.RouteStatus{
					Ingress: []routev1.RouteIngress{
						{
							RouterCanonicalHostname: "router-canonical.aks.example.com",
						},
					},
				},
			},
			strategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
			},
			expectedHost: "",
			expectedPort: 0,
		},
		{
			name: "When Route strategy has Spec.Host empty but Status.Ingress[0].Host is set, it should return the ingress host and port 443",
			svc:  &corev1.Service{},
			route: &routev1.Route{
				Status: routev1.RouteStatus{
					Ingress: []routev1.RouteIngress{
						{
							Host: "ingress.example.com",
						},
					},
				},
			},
			strategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
			},
			expectedHost: "ingress.example.com",
			expectedPort: 443,
		},
		{
			name:  "When Route strategy has all host fields empty, it should return empty host and port 0",
			svc:   &corev1.Service{},
			route: &routev1.Route{},
			strategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
			},
			expectedHost: "",
			expectedPort: 0,
		},
		{
			name: "When LoadBalancer strategy has no ingress, it should return a message indicating LB is not provisioned",
			svc: &corev1.Service{
				ObjectMeta: v1.ObjectMeta{
					CreationTimestamp: v1.NewTime(time.Now().Add(-5 * time.Minute)),
				},
			},
			route: &routev1.Route{},
			strategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
			},
			expectedHost:    "",
			expectedPort:    0,
			expectedMessage: "Konnectivity load balancer is not provisioned",
		},
		{
			name: "When LoadBalancer strategy has an ingress hostname, it should return the hostname and the konnectivity port",
			svc: &corev1.Service{
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{Hostname: "lb.example.com"},
						},
					},
				},
			},
			route: &routev1.Route{},
			strategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
			},
			expectedHost: "lb.example.com",
			expectedPort: int32(KonnectivityServerPort),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			host, port, message, err := ReconcileKonnectivityServerServiceStatus(tc.svc, tc.route, tc.strategy, &fakeMessageCollector{})
			if tc.expectedErr != nil {
				g.Expect(err).To(MatchError(tc.expectedErr))
			} else {
				g.Expect(err).To(BeNil())
			}
			g.Expect(host).To(Equal(tc.expectedHost))
			g.Expect(port).To(Equal(tc.expectedPort))
			if tc.expectedMessage != "" {
				g.Expect(message).To(ContainSubstring(tc.expectedMessage))
			} else {
				g.Expect(message).To(BeEmpty())
			}
		})
	}
}
