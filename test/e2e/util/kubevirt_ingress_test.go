package util

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestValidateKubeVirtIngressPassthrough(t *testing.T) {
	for _, tc := range []struct {
		name          string
		mutate        func(*corev1.Service, *discoveryv1.EndpointSlice, *routev1.Route)
		noMachines    bool
		missingRoute  bool
		ipv6          bool
		defaultPort   bool
		expectedError string
	}{
		{name: "When resources target the configured port, it should pass"},
		{name: "When resources use IPv6, it should validate IPv6 endpoints", ipv6: true},
		{name: "When resources target the default HTTPS port, it should pass", defaultPort: true},
		{name: "When no machines are supplied, it should fail", noMachines: true, expectedError: "at least one machine"},
		{name: "When the service targets the wrong port, it should fail", mutate: func(s *corev1.Service, _ *discoveryv1.EndpointSlice, _ *routev1.Route) {
			s.Spec.Ports[0].TargetPort = intstr.FromInt32(443)
		}, expectedError: "targeting 8443"},
		{name: "When the service has a selector, it should fail", mutate: func(s *corev1.Service, _ *discoveryv1.EndpointSlice, _ *routev1.Route) {
			s.Spec.Selector = map[string]string{"app": "router"}
		}, expectedError: "selector-less"},
		{name: "When the endpoints have the wrong port, it should fail", mutate: func(_ *corev1.Service, e *discoveryv1.EndpointSlice, _ *routev1.Route) {
			e.Ports[0].Port = ptr.To[int32](443)
		}, expectedError: "ports do not match"},
		{name: "When the slice has no endpoints, it should fail", mutate: func(_ *corev1.Service, e *discoveryv1.EndpointSlice, _ *routev1.Route) { e.Endpoints = nil }, expectedError: "machine internal address"},
		{name: "When the endpoint address is wrong, it should fail", mutate: func(_ *corev1.Service, e *discoveryv1.EndpointSlice, _ *routev1.Route) {
			e.Endpoints[0].Addresses = []string{"192.0.2.20"}
		}, expectedError: "machine internal address"},
		{name: "When the endpoint is not ready, it should fail", mutate: func(_ *corev1.Service, e *discoveryv1.EndpointSlice, _ *routev1.Route) {
			e.Endpoints[0].Conditions.Ready = ptr.To(false)
		}, expectedError: "not ready/serving"},
		{name: "When the route lacks TLS, it should fail", mutate: func(_ *corev1.Service, _ *discoveryv1.EndpointSlice, r *routev1.Route) { r.Spec.TLS = nil }, expectedError: "wildcard TLS passthrough"},
		{name: "When the route is missing, it should return the lookup error", missingRoute: true, expectedError: "getting passthrough Route"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			scheme := runtime.NewScheme()
			g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
			g.Expect(discoveryv1.AddToScheme(scheme)).To(Succeed())
			g.Expect(routev1.AddToScheme(scheme)).To(Succeed())
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "hc"}, Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{Type: hyperv1.KubevirtPlatform, Kubevirt: &hyperv1.KubevirtPlatformSpec{GenerateID: "test"}},
				DNS:      hyperv1.DNSSpec{BaseDomain: "example.com"},
			}}
			svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "infra", Name: "default-ingress-passthrough-service-test"}, Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP, IPFamilies: []corev1.IPFamily{corev1.IPv4Protocol},
				Ports: []corev1.ServicePort{{Name: "https-443", Port: 443, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt32(8443)}},
			}}
			slice := &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Namespace: "infra", Name: svc.Name + "-machine-ipv4", Labels: map[string]string{
				discoveryv1.LabelServiceName: svc.Name, discoveryv1.LabelManagedBy: "control-plane-operator.hypershift.openshift.io",
			}}, AddressType: discoveryv1.AddressTypeIPv4,
				Ports:     []discoveryv1.EndpointPort{{Name: ptr.To("https-443"), Port: ptr.To[int32](8443), Protocol: ptr.To(corev1.ProtocolTCP)}},
				Endpoints: []discoveryv1.Endpoint{{Addresses: []string{"192.0.2.10"}, Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(true), Serving: ptr.To(true)}}},
			}
			route := &routev1.Route{ObjectMeta: metav1.ObjectMeta{Namespace: "infra", Name: "default-ingress-passthrough-route-test"}, Spec: routev1.RouteSpec{
				Host: "https.apps.hc.example.com", To: routev1.RouteTargetReference{Kind: "Service", Name: svc.Name},
				WildcardPolicy: routev1.WildcardPolicySubdomain, TLS: &routev1.TLSConfig{Termination: routev1.TLSTerminationPassthrough},
			}}
			machines := []capiv1.Machine{{ObjectMeta: metav1.ObjectMeta{Name: "machine"}, Status: capiv1.MachineStatus{Addresses: []capiv1.MachineAddress{{Type: capiv1.MachineInternalIP, Address: "192.0.2.10"}}}}}
			if tc.ipv6 {
				svc.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv6Protocol}
				slice.Name = svc.Name + "-machine-ipv6"
				slice.AddressType = discoveryv1.AddressTypeIPv6
				slice.Endpoints[0].Addresses = []string{"2001:db8::10"}
				machines[0].Status.Addresses[0].Address = "2001:db8::10"
			}
			expectedPort := int32(8443)
			if tc.defaultPort {
				expectedPort = 443
				svc.Spec.Ports[0].TargetPort = intstr.FromInt32(expectedPort)
				slice.Ports[0].Port = ptr.To(expectedPort)
			}
			if tc.noMachines {
				machines = nil
			}
			if tc.mutate != nil {
				tc.mutate(svc, slice, route)
			}
			builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(svc, slice)
			if !tc.missingRoute {
				builder.WithObjects(route)
			}
			err := ValidateKubeVirtIngressPassthrough(t.Context(), builder.Build(), "infra", hc, machines, expectedPort)
			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedError)))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}
