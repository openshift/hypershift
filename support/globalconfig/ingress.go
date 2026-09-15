package globalconfig

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func IngressConfig() *configv1.Ingress {
	return &configv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ReconcileIngressConfig(cfg *configv1.Ingress, hcp *hyperv1.HostedControlPlane) {
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Ingress != nil {
		cfg.Spec = *hcp.Spec.Configuration.Ingress
	}
	if cfg.Spec.Domain == "" {
		cfg.Spec.Domain = IngressDomain(hcp)
	}
}

func IngressDomain(hcp *hyperv1.HostedControlPlane) string {
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Ingress != nil {
		if len(hcp.Spec.Configuration.Ingress.AppsDomain) > 0 {
			return hcp.Spec.Configuration.Ingress.AppsDomain
		}
		if len(hcp.Spec.Configuration.Ingress.Domain) > 0 {
			return hcp.Spec.Configuration.Ingress.Domain
		}
	}
	// Managed ingress DNS publishes the apps wildcard into a dedicated
	// CPO-managed zone rooted at the ingress zone domain. The apps domain must
	// therefore live beneath that zone, otherwise Route53 rejects the wildcard
	// record as not permitted in the zone.
	if hcp.Spec.Platform.AWS != nil && hcp.Spec.Platform.AWS.ManagedDNS != nil {
		return fmt.Sprintf("apps.%s", ManagedDNSIngressZoneDomain(hcp))
	}
	return fmt.Sprintf("apps.%s", BaseDomain(hcp))
}

// ManagedDNSIngressZoneDomain returns the managed ingress zone domain, e.g.
// "in.<base>". The CPO creates the Route53 ingress zone at this domain and the
// guest apps domain lives beneath it, so both must derive from this single
// function to stay in sync. It assumes managed ingress DNS is configured on the
// HCP and that ingressDomainPrefix has been defaulted by the API server ("in").
func ManagedDNSIngressZoneDomain(hcp *hyperv1.HostedControlPlane) string {
	return fmt.Sprintf("%s.%s", hcp.Spec.Platform.AWS.ManagedDNS.IngressDomainPrefix, BaseDomain(hcp))
}
