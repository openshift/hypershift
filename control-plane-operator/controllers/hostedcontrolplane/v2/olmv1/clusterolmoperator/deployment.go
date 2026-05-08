package clusterolmoperator

import (
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/olmv1/util"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	// Inject kubeconfig for hosted cluster API access
	// This allows cluster-olm-operator to:
	// - Manage ClusterCatalog resources in the hosted cluster
	// - Monitor ClusterExtension resources in the hosted cluster
	if err := util.InjectHostedClusterKubeconfig(cpContext, deployment); err != nil {
		return err
	}

	// Configure dual-API access pattern
	// cluster-olm-operator needs to access TWO API servers:
	// 1. Hosted cluster API (via KUBECONFIG) - for ClusterCatalog/ClusterExtension management
	// 2. Management cluster API (via in-cluster config) - for ClusterOperator status reporting
	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		podspec.UpsertEnvVars(c, []corev1.EnvVar{
			// HOSTED_KUBECONFIG tells cluster-olm-operator to use this kubeconfig for hosted cluster API access
			// The operator code must be modified to read this env var and create a separate client
			{Name: "HOSTED_KUBECONFIG", Value: "/etc/openshift/kubeconfig/kubeconfig"},

			// IN_CLUSTER_MODE tells cluster-olm-operator it's running in HyperShift mode
			// In this mode, it should:
			// - Use HOSTED_KUBECONFIG client for ClusterCatalog/ClusterExtension
			// - Use in-cluster config for ClusterOperator status reporting to management cluster
			{Name: "HYPERSHIFT_MODE", Value: "true"},

			{Name: "RELEASE_VERSION", Value: cpContext.UserReleaseImageProvider.Version()},
		})
	})

	return nil
}
