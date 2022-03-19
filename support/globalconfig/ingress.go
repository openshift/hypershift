package globalconfig

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func IngressConfig() *configv1.Ingress {
	return &configv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ReconcileIngressConfig(cfg *configv1.Ingress, hcp *hyperv1.HostedControlPlane, globalConfig GlobalConfig) {
	cfg.Spec.Domain = IngressDomain(hcp, globalConfig.Ingress)
	if globalConfig.Ingress != nil {
		cfg.Spec = globalConfig.Ingress.Spec
	}
}

func IngressDomain(hcp *hyperv1.HostedControlPlane, ingressConfig *configv1.Ingress) string {
	if ingressConfig != nil {
		if len(ingressConfig.Spec.AppsDomain) > 0 {
			return ingressConfig.Spec.AppsDomain
		}
		return ingressConfig.Spec.Domain
	}
	return fmt.Sprintf("apps.%s", BaseDomain(hcp))
}
