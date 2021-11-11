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

func ReconcileIngressConfig(cfg *configv1.Ingress, hcp *hyperv1.HostedControlPlane, globalConfig *GlobalConfig) {
	cfg.Spec.Domain = ingressDomain(hcp)
	if globalConfig.Ingress != nil {
		// For now only the AppsDomain is configurable through the HCP configuration
		// field. As needed, we can enable other parts of the spec.
		cfg.Spec.AppsDomain = globalConfig.Ingress.Spec.AppsDomain
	}
}

func ingressDomain(hcp *hyperv1.HostedControlPlane) string {
	return fmt.Sprintf("apps.%s", baseDomain(hcp))
}
