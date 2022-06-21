package ccm

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/ccm/platform"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ReconcileDeployment(deployment *appsv1.Deployment, hcp *hyperv1.HostedControlPlane, ccmPlatform platform.Platform) error {
	p := NewCloudProviderParams(hcp)
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: ccmLabels(),
		},
		Strategy: appsv1.DeploymentStrategy{
			Type: appsv1.RecreateDeploymentStrategyType,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: ccmLabels(),
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					util.BuildContainer(manifests.CCMContainer(), buildCCMContainer(ccmPlatform)),
				},
				Volumes: []corev1.Volume{},
			},
		},
	}

	ccmPlatform.AddPlatfomVolumes(deployment)

	p.OwnerRef.ApplyTo(deployment)
	p.DeploymentConfig.ApplyTo(deployment)
	return nil
}

func buildCCMContainer(ccmPlatform platform.Platform) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = ccmPlatform.GetContainerImage()
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Command = ccmPlatform.GetContainerCommand()
		c.Args = ccmPlatform.GetContainerArgs()
		c.VolumeMounts = ccmPlatform.GetPodVolumeMounts().ContainerMounts(c.Name)
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "https",
				Protocol:      "TCP",
				ContainerPort: 10258,
			},
		}
	}
}
