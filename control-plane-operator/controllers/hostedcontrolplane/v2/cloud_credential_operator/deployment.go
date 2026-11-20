package cco

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		util.UpsertEnvVar(c, corev1.EnvVar{
			Name: "RELEASE_VERSION",
			// The CCO uses this both as its desired version for the guest
			// cluster's featuregates.config.openshift.io/cluster (which carries
			// the user payload's feature sets) and as the version it reports on
			// its ClusterOperator. It must follow the user payload
			// (spec.release): using the control plane payload version under a
			// spec.controlPlaneRelease skew makes the CCO fail to find its
			// version in the FeatureGate and crashloop, never creating the
			// cloud-credential ClusterOperator.
			Value: cpContext.UserReleaseImageProvider.Version(),
		})
		proxy.SetEnvVars(&c.Env)

	})
	return nil
}
