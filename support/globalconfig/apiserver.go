package globalconfig

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func APIServerConfiguration() *configv1.APIServer {
	return &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ReconcileAPIServerConfiguration(APIServer *configv1.APIServer, config *hyperv1.ClusterConfiguration) error {
	if config != nil && config.APIServer != nil {
		APIServer.Spec = *config.APIServer
	}
	return nil
}
