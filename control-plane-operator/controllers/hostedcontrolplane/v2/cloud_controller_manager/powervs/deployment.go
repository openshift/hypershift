package powervs

import (
	"fmt"

	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

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

	util.UpdateVolume(cloudCredsVolumeName, deployment.Spec.Template.Spec.Volumes, func(v *corev1.Volume) {
		v.Secret.SecretName = hcp.Spec.Platform.PowerVS.KubeCloudControllerCreds.Name
	})
	return nil
}
