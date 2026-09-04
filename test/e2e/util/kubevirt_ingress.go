package util

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcpmanifests "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ValidateKubeVirtIngressPassthrough checks the Service, managed EndpointSlices
// for the supplied machines, and Route without polling or mutating resources.
// Callers supply the expected HTTPS port independently of the observed Service.
func ValidateKubeVirtIngressPassthrough(ctx context.Context, infraClient crclient.Reader, namespace string, hc *hyperv1.HostedCluster, machines []capiv1.Machine, httpsPort int32) error {
	if hc == nil || hc.Spec.Platform.Type != hyperv1.KubevirtPlatform || hc.Spec.Platform.Kubevirt == nil || hc.Spec.Platform.Kubevirt.GenerateID == "" {
		return fmt.Errorf("expected a KubeVirt HostedCluster with a GenerateID")
	}
	if len(machines) == 0 {
		return fmt.Errorf("expected at least one machine to validate passthrough endpoints")
	}
	svc := hcpmanifests.IngressDefaultIngressPassthroughService(namespace)
	svc.Name = hcpmanifests.IngressDefaultIngressPassthroughServiceName + "-" + hc.Spec.Platform.Kubevirt.GenerateID
	if err := infraClient.Get(ctx, crclient.ObjectKeyFromObject(svc), svc); err != nil {
		return fmt.Errorf("getting passthrough Service: %w", err)
	}
	if svc.Spec.Type != corev1.ServiceTypeClusterIP || len(svc.Spec.Selector) != 0 {
		return fmt.Errorf("service %s/%s must be a selector-less ClusterIP", namespace, svc.Name)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 443 || svc.Spec.Ports[0].Protocol != corev1.ProtocolTCP || svc.Spec.Ports[0].TargetPort != intstr.FromInt32(httpsPort) {
		return fmt.Errorf("service %s/%s must expose TCP 443 targeting %d, got %v", namespace, svc.Name, httpsPort, svc.Spec.Ports)
	}
	if len(svc.Spec.IPFamilies) == 0 {
		return fmt.Errorf("service %s/%s has no IP families", namespace, svc.Name)
	}
	for _, machine := range machines {
		if err := validateKubeVirtIngressMachineEndpoints(ctx, infraClient, svc, machine, httpsPort); err != nil {
			return err
		}
	}
	route := hcpmanifests.IngressDefaultIngressPassthroughRoute(namespace)
	route.Name = hcpmanifests.IngressDefaultIngressPassthroughRouteName + "-" + hc.Spec.Platform.Kubevirt.GenerateID
	if err := infraClient.Get(ctx, crclient.ObjectKeyFromObject(route), route); err != nil {
		return fmt.Errorf("getting passthrough Route: %w", err)
	}
	if route.Spec.To.Kind != "Service" || route.Spec.To.Name != svc.Name || route.Spec.Host != fmt.Sprintf("https.apps.%s.%s", hc.Name, hc.Spec.DNS.BaseDomain) || route.Spec.WildcardPolicy != routev1.WildcardPolicySubdomain || route.Spec.TLS == nil || route.Spec.TLS.Termination != routev1.TLSTerminationPassthrough {
		return fmt.Errorf("route %s/%s must be a wildcard TLS passthrough to Service %s", namespace, route.Name, svc.Name)
	}
	return nil
}

func validateKubeVirtIngressMachineEndpoints(ctx context.Context, infraClient crclient.Reader, svc *corev1.Service, machine capiv1.Machine, httpsPort int32) error {
	readyEndpoint := false
	for _, family := range svc.Spec.IPFamilies {
		// CAPK may report multiple addresses per family; HCCO uses the first
		// non-link-local internal address, not every interface on the VM.
		expectedAddress := ""
		for _, address := range machine.Status.Addresses {
			ip, err := netip.ParseAddr(address.Address)
			if address.Type != capiv1.MachineInternalIP || err != nil || ip.IsLinkLocalUnicast() {
				continue
			}
			if (family == corev1.IPv4Protocol && ip.Is4()) || (family == corev1.IPv6Protocol && ip.Is6()) {
				expectedAddress = address.Address
				break
			}
		}
		slice := &discoveryv1.EndpointSlice{}
		key := crclient.ObjectKey{Namespace: svc.Namespace, Name: svc.Name + "-" + machine.Name + "-" + strings.ToLower(string(family))}
		if err := infraClient.Get(ctx, key, slice); err != nil {
			return fmt.Errorf("getting EndpointSlice %s: %w", key, err)
		}
		if slice.Labels[discoveryv1.LabelServiceName] != svc.Name || slice.Labels[discoveryv1.LabelManagedBy] != "control-plane-operator.hypershift.openshift.io" || slice.AddressType != discoveryv1.AddressType(family) {
			return fmt.Errorf("EndpointSlice %s has incorrect labels or address type", key)
		}
		if len(slice.Ports) != 1 || ptr.Deref(slice.Ports[0].Port, 0) != httpsPort || ptr.Deref(slice.Ports[0].Name, "") != svc.Spec.Ports[0].Name || ptr.Deref(slice.Ports[0].Protocol, "") != corev1.ProtocolTCP {
			return fmt.Errorf("EndpointSlice %s ports do not match the passthrough Service", key)
		}
		if expectedAddress == "" && len(slice.Endpoints) == 0 {
			continue
		}
		if expectedAddress == "" || len(slice.Endpoints) != 1 || len(slice.Endpoints[0].Addresses) != 1 || slice.Endpoints[0].Addresses[0] != expectedAddress {
			return fmt.Errorf("EndpointSlice %s must contain machine internal address %q", key, expectedAddress)
		}
		endpoint := slice.Endpoints[0]
		if !ptr.Deref(endpoint.Conditions.Ready, false) || !ptr.Deref(endpoint.Conditions.Serving, false) {
			return fmt.Errorf("EndpointSlice %s is not ready/serving", key)
		}
		readyEndpoint = true
	}
	if !readyEndpoint {
		return fmt.Errorf("machine %s has no ready passthrough endpoint", machine.Name)
	}
	return nil
}
