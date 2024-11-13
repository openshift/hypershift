package kas

import (
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/utils/ptr"
)

func ReconcileKASEndpoints(endpoints *corev1.Endpoints, address string, port int32) {
	if endpoints.Labels == nil {
		endpoints.Labels = map[string]string{}
	}
	endpoints.Labels[discoveryv1.LabelSkipMirror] = "true"
	endpoints.Subsets = []corev1.EndpointSubset{{
		Addresses: []corev1.EndpointAddress{{
			IP: address,
		}},
		Ports: []corev1.EndpointPort{{
			Name:     "https",
			Port:     port,
			Protocol: corev1.ProtocolTCP,
		}},
	}}
}

func ReconcileKASEndpointSlice(endpointSlice *discoveryv1.EndpointSlice, address string, port int32) {
	if endpointSlice.Labels == nil {
		endpointSlice.Labels = map[string]string{}
	}
	endpointSlice.Labels[discoveryv1.LabelServiceName] = "kubernetes"
	ipv4, err := util.IsIPv4(address)
	if err != nil || ipv4 {
		endpointSlice.AddressType = discoveryv1.AddressTypeIPv4
	} else {
		endpointSlice.AddressType = discoveryv1.AddressTypeIPv6
	}
	endpointSlice.Endpoints = []discoveryv1.Endpoint{{
		Addresses: []string{
			address,
		},
		Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(true)},
	}}
	endpointSlice.Ports = []discoveryv1.EndpointPort{{
		Name:     ptr.To("https"),
		Port:     ptr.To(port),
		Protocol: ptr.To(corev1.ProtocolTCP),
	}}
}
