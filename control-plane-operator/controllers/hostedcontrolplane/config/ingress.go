package config

import (
	"fmt"

	hyperv1 "github.com/alknopfler/hypershift/api/v1alpha1"
)

func IngressSubdomain(hcp *hyperv1.HostedControlPlane) string {
	return fmt.Sprintf("apps.%s.%s", hcp.Name, hcp.Spec.DNS.BaseDomain)
}
