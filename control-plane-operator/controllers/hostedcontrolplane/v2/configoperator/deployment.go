package configoperator

import (
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	kubeconfigVolumeName      = "kubeconfig"
	rootCAVolumeName          = "root-ca"
	clusterSignerCAVolumeName = "cluster-signer-ca"
)

func (h *hcco) adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	versions, err := cpContext.ReleaseImageProvider.ComponentVersions()
	if err != nil {
		return fmt.Errorf("failed to get component versions: %w", err)
	}
	kubeVersion := versions["kubernetes"]
	hcp := cpContext.HCP
	openShiftVersion := cpContext.ReleaseImageProvider.Version()

	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Command = append(c.Command,
			"--platform-type", string(hcp.Spec.Platform.Type),
			fmt.Sprintf("--enable-ci-debug-output=%t", cpContext.EnableCIDebugOutput),
			fmt.Sprintf("--hosted-control-plane=%s", hcp.Name),
			fmt.Sprintf("--konnectivity-address=%s", cpContext.InfraStatus.KonnectivityHost),
			fmt.Sprintf("--konnectivity-port=%d", cpContext.InfraStatus.KonnectivityPort),
			fmt.Sprintf("--oauth-address=%s", cpContext.InfraStatus.OAuthHost),
			fmt.Sprintf("--oauth-port=%d", cpContext.InfraStatus.OAuthPort),
			"--registry-overrides", util.ConvertRegistryOverridesToCommandLineFlag(h.registryOverrides),
		)

		if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
			c.Command = append(c.Command, "--controllers=controller-manager-ca,resources,inplaceupgrader,drainer,hcpstatus")
		}

		c.Env = append(c.Env, []corev1.EnvVar{
			{
				Name:  "OPENSHIFT_RELEASE_VERSION",
				Value: openShiftVersion,
			},
			{
				Name:  "KUBERNETES_VERSION",
				Value: kubeVersion,
			},
			{
				Name:  "OPERATE_ON_RELEASE_IMAGE",
				Value: hcp.Spec.ReleaseImage,
			},
			{
				Name:  "OPENSHIFT_IMG_OVERRIDES",
				Value: util.ConvertOpenShiftImageRegistryOverridesToCommandLineFlag(h.openShiftImageRegistryOverrides),
			},
		}...)

		proxy.SetEnvVars(&c.Env)
		if os.Getenv("ENABLE_SIZE_TAGGING") == "1" {
			c.Env = append(c.Env,
				corev1.EnvVar{
					Name:  "ENABLE_SIZE_TAGGING",
					Value: "1",
				},
			)
		}
		if len(os.Getenv("MANAGED_SERVICE")) > 0 {
			c.Env = append(c.Env,
				corev1.EnvVar{
					Name:  "MANAGED_SERVICE",
					Value: os.Getenv("MANAGED_SERVICE"),
				})
		}
	})

	util.UpdateVolume(kubeconfigVolumeName, deployment.Spec.Template.Spec.Volumes, func(v *corev1.Volume) {
		v.VolumeSource.Secret.SecretName = manifests.HCCOKubeconfigSecret("").Name
	})
	util.UpdateVolume(rootCAVolumeName, deployment.Spec.Template.Spec.Volumes, func(v *corev1.Volume) {
		v.VolumeSource.ConfigMap.Name = manifests.RootCAConfigMap("").Name
	})
	util.UpdateVolume(clusterSignerCAVolumeName, deployment.Spec.Template.Spec.Volumes, func(v *corev1.Volume) {
		v.VolumeSource.ConfigMap.Name = manifests.KubeletClientCABundle("").Name
	})

	if isExternalInfraKubevirt(hcp) {
		// injects the kubevirt credentials secret volume, volume mount path, and appends cli arg.
		util.DeploymentAddKubevirtInfraCredentials(deployment)
	}

	return nil
}

func isExternalInfraKubevirt(hcp *hyperv1.HostedControlPlane) bool {
	if hcp.Spec.Platform.Kubevirt != nil &&
		hcp.Spec.Platform.Kubevirt.Credentials != nil &&
		hcp.Spec.Platform.Kubevirt.Credentials.InfraKubeConfigSecret != nil &&
		hcp.Spec.Platform.Kubevirt.Credentials.InfraNamespace != "" {
		return true
	} else {
		return false
	}
}
