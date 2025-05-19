package capimanager

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/blang/semver"
)

func (capi *CAPIManagerOptions) adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	versionStr := cpContext.ReleaseImageProvider.Version()
	version, err := semver.Parse(versionStr)
	if err != nil {
		return fmt.Errorf("failed to parse version (%s): %w", versionStr, err)
	}

	util.UpdateContainer("manager", deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		if version.GE(config.Version419) {
			c.Args = append(c.Args, "--feature-gates=MachineSetPreflightChecks=false")
		}

		if len(capi.imageOverride) > 0 {
			c.Image = capi.imageOverride
		}
	})

	if deployment.Annotations == nil {
		deployment.Annotations = make(map[string]string)
	}
	deployment.Annotations[util.HostedClusterAnnotation] = cpContext.HCP.Annotations[util.HostedClusterAnnotation]

	return nil
}
