package globalconfig

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
)

func IngressConfig() *configv1.Ingress {
	return &configv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ReconcileIngressConfig(cfg *configv1.Ingress, hcp *hyperv1.HostedControlPlane) {
	cfg.Spec.Domain = IngressDomain(hcp)
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Ingress != nil {
		cfg.Spec = *hcp.Spec.Configuration.Ingress
	}
}

func IngressDomain(hcp *hyperv1.HostedControlPlane) string {
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Ingress != nil {
		if len(hcp.Spec.Configuration.Ingress.AppsDomain) > 0 {
			return hcp.Spec.Configuration.Ingress.AppsDomain
		}
		return hcp.Spec.Configuration.Ingress.Domain
	}
	return fmt.Sprintf("apps.%s", BaseDomain(hcp))
}
