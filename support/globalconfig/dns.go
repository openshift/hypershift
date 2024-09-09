package globalconfig

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
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
