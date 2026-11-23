package cco

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/proxy"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/blang/semver"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	versionStr := cpContext.ReleaseImageProvider.Version()
	version, err := semver.Parse(versionStr)
	if err != nil {
		return fmt.Errorf("parsing ReleaseVersion (%s): %w", versionStr, err)
	}

	// Only resolve TLS args for OCP 4.23+ or 5.x+ where they are actually used
	var tlsArgs []string
	if version.Major >= 5 || (version.Major == 4 && version.Minor >= 23) {
		tlsArgs, err = config.TLSArgs(cpContext.HCP.Spec.Configuration.GetTLSSecurityProfile())
		if err != nil {
			return fmt.Errorf("failed to resolve Cloud Credential Operator TLS arguments: %w", err)
		}
	}

	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		podspec.UpsertEnvVar(c, corev1.EnvVar{
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

		if len(tlsArgs) > 0 {
			c.Args = append(c.Args, tlsArgs...)
		}
	})
	return nil
}
