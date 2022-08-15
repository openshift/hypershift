package config

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
)

func IngressSubdomain(hcp *hyperv1.HostedControlPlane) string {
	return fmt.Sprintf("apps.%s.%s", hcp.Name, hcp.Spec.DNS.BaseDomain)
}
