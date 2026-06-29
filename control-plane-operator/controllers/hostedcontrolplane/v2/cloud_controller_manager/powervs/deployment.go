package powervs

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	cloudCredsVolumeName = "cloud-creds"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP
	if hcp.Spec.Platform.PowerVS == nil {
		return fmt.Errorf(".spec.platform.powervs is not defined")
	}

	podspec.UpdateContainer("cloud-controller-manager", deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = config.AppendTLSArgs(c.Args, hcp.Spec.Configuration.GetTLSSecurityProfile())
	})

	podspec.UpdateVolume(cloudCredsVolumeName, deployment.Spec.Template.Spec.Volumes, func(v *corev1.Volume) {
		v.Secret.SecretName = hcp.Spec.Platform.PowerVS.KubeCloudControllerCreds.Name
	})
	return nil
}
