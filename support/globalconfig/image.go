package globalconfig

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ImageConfig() *configv1.Image {
	return &configv1.Image{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ReconcileImageConfig(cfg *configv1.Image, hcp *hyperv1.HostedControlPlane) {
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Image != nil {
		cfg.Spec = *hcp.Spec.Configuration.Image
	}
}

func ReconcileImageConfigFromHostedCluster(cfg *configv1.Image, hc *hyperv1.HostedCluster) {
	if hc.Spec.Configuration != nil && hc.Spec.Configuration.Image != nil {
		cfg.Spec = *hc.Spec.Configuration.Image
	}
}
