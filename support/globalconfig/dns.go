package globalconfig

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func DNSConfig() *configv1.DNS {
	return &configv1.DNS{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ReconcileDNSConfig(dns *configv1.DNS, hcp *hyperv1.HostedControlPlane) {
	if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		dns.Spec.BaseDomain = hcp.Spec.DNS.BaseDomain
	} else {
		dns.Spec.BaseDomain = BaseDomain(hcp)
	}
	if len(hcp.Spec.DNS.PublicZoneID) > 0 {
		dns.Spec.PublicZone = &configv1.DNSZone{
			ID: hcp.Spec.DNS.PublicZoneID,
		}
	}
	if len(hcp.Spec.DNS.PrivateZoneID) > 0 {
		dns.Spec.PrivateZone = &configv1.DNSZone{
			ID: hcp.Spec.DNS.PrivateZoneID,
		}
	}
	if hcp.Spec.Platform.AWS != nil && hcp.Spec.Platform.AWS.SharedVPC != nil {
		dns.Spec.Platform.Type = configv1.AWSPlatformType
		dns.Spec.Platform.AWS = &configv1.AWSDNSSpec{
			PrivateZoneIAMRole: hcp.Spec.Platform.AWS.SharedVPC.RolesRef.IngressARN,
		}
	}

	// When managed ingress DNS is active, override the zones with the
	// CPO-managed ingress zones so the ingress operator creates wildcard
	// records there. The ingress operator gets the platform type from
	// infrastructure.config, not dns.config, so no platform change is needed.
	if hcp.Spec.Platform.AWS != nil && hcp.Spec.Platform.AWS.ManagedDNS != nil {
		reconcileManagedIngressDNSZones(dns, hcp)
	}
}

func reconcileManagedIngressDNSZones(dns *configv1.DNS, hcp *hyperv1.HostedControlPlane) {
	if hcp.Status.Platform == nil || hcp.Status.Platform.AWS == nil {
		return
	}
	for _, zone := range hcp.Status.Platform.AWS.DNSZones {
		switch zone.ZoneType {
		case hyperv1.PublicIngressZone:
			dns.Spec.PublicZone = &configv1.DNSZone{ID: zone.ZoneID}
		case hyperv1.PrivateIngressZone:
			dns.Spec.PrivateZone = &configv1.DNSZone{ID: zone.ZoneID}
		}
	}
}

func BaseDomain(hcp *hyperv1.HostedControlPlane) string {
	prefix := hcp.Name
	if hcp.Spec.DNS.BaseDomainPrefix != nil {
		prefix = *hcp.Spec.DNS.BaseDomainPrefix
	}

	if prefix == "" {
		return hcp.Spec.DNS.BaseDomain
	}

	return fmt.Sprintf("%s.%s", prefix, hcp.Spec.DNS.BaseDomain)
}
