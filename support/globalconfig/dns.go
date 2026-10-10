package globalconfig

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/gcputil"

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
	dns.Spec.BaseDomain = BaseDomain(hcp)
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
	applyPlatformDNSConfig(dns, hcp)
}

// applyPlatformDNSConfig applies platform-specific DNS configuration overrides.
func applyPlatformDNSConfig(dns *configv1.DNS, hcp *hyperv1.HostedControlPlane) {
	switch hcp.Spec.Platform.Type {
	case hyperv1.IBMCloudPlatform:
		dns.Spec.BaseDomain = hcp.Spec.DNS.BaseDomain
	case hyperv1.AWSPlatform:
		if hcp.Spec.Platform.AWS != nil && hcp.Spec.Platform.AWS.SharedVPC != nil {
			dns.Spec.Platform.Type = configv1.AWSPlatformType
			dns.Spec.Platform.AWS = &configv1.AWSDNSSpec{
				PrivateZoneIAMRole: hcp.Spec.Platform.AWS.SharedVPC.RolesRef.IngressARN,
			}
		}
	case hyperv1.GCPPlatform:
		applyGCPDNSConfig(dns, hcp)
	}
}

// applyGCPDNSConfig points the guest dns.config at the GCP Cloud DNS ingress
// managed zones created by the GCP Private Service Connect controller, so the
// stock cluster-ingress-operator manages the default IngressController's
// *.apps wildcard records.
//
// The zone names are derived from the RAW base domain (hcp.Spec.DNS.BaseDomain),
// not globalconfig.BaseDomain(hcp): the PSC controller keys the zone name on the
// raw base domain and ignores the prefix/cluster name, so feeding the prefixed
// value here would reference a non-existent zone. On a derivation error (which
// would equally have failed zone creation) the zone is left unset.
func applyGCPDNSConfig(dns *configv1.DNS, hcp *hyperv1.HostedControlPlane) {
	baseDomain := hcp.Spec.DNS.BaseDomain
	if baseDomain == "" {
		return
	}
	// Derive each zone ID only when it is not explicitly configured on the
	// HostedControlPlane, so an operator-provided PublicZoneID/PrivateZoneID
	// (already applied by ReconcileDNSConfig) is preserved.
	if len(hcp.Spec.DNS.PublicZoneID) == 0 {
		if publicZone, err := gcputil.PublicIngressZoneName(baseDomain); err == nil {
			dns.Spec.PublicZone = &configv1.DNSZone{ID: publicZone}
		}
	}
	if len(hcp.Spec.DNS.PrivateZoneID) == 0 {
		if privateZone, err := gcputil.PrivateIngressZoneName(baseDomain); err == nil {
			dns.Spec.PrivateZone = &configv1.DNSZone{ID: privateZone}
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
