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
	return fmt.Sprintf("apps.%s", BaseDomain(hcp))
}
