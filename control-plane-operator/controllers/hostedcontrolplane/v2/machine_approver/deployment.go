package machineapprover

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/blang/semver"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	versionString := cpContext.ReleaseImageProvider.Version()
	version, err := semver.Parse(versionString)
	if err != nil {
		return fmt.Errorf("failed to parse machine approver release version %q: %w", versionString, err)
	}

	var tlsArgs []string
	if version.Major > config.Version423.Major ||
		(version.Major == config.Version423.Major && version.Minor >= config.Version423.Minor) {
		tlsArgs, err = config.TLSArgs(hcp.Spec.Configuration.GetTLSSecurityProfile())
		if err != nil {
			return fmt.Errorf("failed to resolve machine approver TLS arguments: %w", err)
		}
	}

	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args, fmt.Sprintf("--machine-namespace=%s", hcp.Namespace))

		if len(tlsArgs) > 0 {
			c.Args = append(c.Args, tlsArgs...)
		}
	})

	return nil
}
