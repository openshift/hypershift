package machineapprover

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	profile := hcp.Spec.Configuration.GetTLSSecurityProfile()
	tlsArgs, err := config.TLSArgs(profile)
	if err != nil {
		return err
	}

	curveArgs, err := config.TLSCurveArgs(cpContext, profile)
	if err != nil {
		return err
	}
	if len(curveArgs) > 0 {
		tlsArgs = append(tlsArgs, curveArgs...)
	}

	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args, fmt.Sprintf("--machine-namespace=%s", hcp.Namespace))

		if len(tlsArgs) > 0 {
			c.Args = append(c.Args, tlsArgs...)
		}
	})

	return nil
}
