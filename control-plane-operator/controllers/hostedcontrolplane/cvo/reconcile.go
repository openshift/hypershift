package cvo

import (
	"fmt"
	"path"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

var (
	cvoLabels = map[string]string{"app": "cluster-version-operator"}

	volumeMounts = util.PodVolumeMounts{
		cvoContainerMain().Name: {
			cvoVolumeUpdatePayloads().Name: "/etc/cvo/updatepayloads",
			cvoVolumeKubeconfig().Name:     "/etc/openshift/kubeconfig",
			cvoVolumePayload().Name:        "/var/payload",
		},
		cvoContainerPrepPayload().Name: {
			cvoVolumePayload().Name: "/var/payload",
		},
	}

	// TODO: These manifests should eventually be removed from the CVO payload by annotating
	// them with the proper cluster profile in the OLM repository.
	manifestsToOmit = []string{
		"0000_50_olm_01-olm-operator.serviceaccount.yaml",
		"0000_50_olm_02-services.yaml",
		"0000_50_olm_06-psm-operator.deployment.yaml",
		"0000_50_olm_06-psm-operator.deployment.ibm-cloud-managed.yaml",
		"0000_50_olm_07-olm-operator.deployment.ibm-cloud-managed.yaml",
		"0000_50_olm_07-olm-operator.deployment.yaml",
		"0000_50_olm_08-catalog-operator.deployment.ibm-cloud-managed.yaml",
		"0000_50_olm_08-catalog-operator.deployment.yaml",
		"0000_50_olm_15-packageserver.clusterserviceversion.yaml",
		"0000_50_olm_99-operatorstatus.yaml",
		"0000_90_olm_00-service-monitor.yaml",
		"0000_90_olm_01-prometheus-rule.yaml",
		"0000_50_operator-marketplace_04_service_account.yaml",
		"0000_50_operator-marketplace_05_role.yaml",
		"0000_50_operator-marketplace_06_role_binding.yaml",
		"0000_50_operator-marketplace_07_configmap.yaml",
		"0000_50_operator-marketplace_08_service.yaml",
		"0000_50_operator-marketplace_09_operator-ibm-cloud-managed.yaml",
		"0000_50_operator-marketplace_09_operator.yaml",
		"0000_50_operator-marketplace_10_clusteroperator.yaml",
		"0000_50_operator-marketplace_11_service_monitor.yaml",
	}
)

func ReconcileDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, deploymentConfig config.DeploymentConfig, image string) error {
	ownerRef.ApplyTo(deployment)
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: cvoLabels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: cvoLabels,
			},
			Spec: corev1.PodSpec{
				AutomountServiceAccountToken: pointer.BoolPtr(false),
				InitContainers: []corev1.Container{
					util.BuildContainer(cvoContainerPrepPayload(), buildCVOContainerPrepPayload(image)),
				},
				Containers: []corev1.Container{
					util.BuildContainer(cvoContainerMain(), buildCVOContainerMain(image)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(cvoVolumePayload(), buildCVOVolumePayload),
					util.BuildVolume(cvoVolumeKubeconfig(), buildCVOVolumeKubeconfig),
					util.BuildVolume(cvoVolumeUpdatePayloads(), buildCVOVolumeUpdatePayloads),
				},
			},
		},
	}
	deploymentConfig.ApplyTo(deployment)
	return nil
}

func cvoContainerPrepPayload() *corev1.Container {
	return &corev1.Container{
		Name: "prepare-payload",
	}
}

func cvoContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "cluster-version-operator",
	}
}

func buildCVOContainerPrepPayload(image string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.Command = []string{"/bin/bash"}
		c.Args = []string{
			"-c",
			preparePayloadScript(),
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func preparePayloadScript() string {
	payloadDir := volumeMounts.Path(cvoContainerPrepPayload().Name, cvoVolumePayload().Name)
	stmts := make([]string, 0, len(manifestsToOmit)+2)
	stmts = append(stmts,
		fmt.Sprintf("cp -R /manifests %s/", payloadDir),
		fmt.Sprintf("cp -R /release-manifests %s/", payloadDir),
	)
	for _, manifest := range manifestsToOmit {
		stmts = append(stmts, fmt.Sprintf("rm %s", path.Join(payloadDir, "release-manifests", manifest)))
	}
	return strings.Join(stmts, "\n")
}

func buildCVOContainerMain(image string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.Command = []string{"cluster-version-operator"}
		c.Args = []string{
			"start",
			"--release-image",
			image,
			"--enable-auto-update=false",
			"--enable-default-cluster-version=true",
			"--kubeconfig",
			path.Join(volumeMounts.Path(c.Name, cvoVolumeKubeconfig().Name), kas.KubeconfigKey),
			"--listen=",
			"--v=4",
		}
		c.Env = []corev1.EnvVar{
			{
				Name:  "PAYLOAD_OVERRIDE",
				Value: volumeMounts.Path(c.Name, cvoVolumePayload().Name),
			},
			{
				Name:  "EXCLUDE_MANIFESTS",
				Value: "internal-openshift-hosted",
			},
			{
				Name: "NODE_NAME",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "spec.nodeName",
					},
				},
			},
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func cvoVolumeUpdatePayloads() *corev1.Volume {
	return &corev1.Volume{
		Name: "update-payloads",
	}
}

func cvoVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func cvoVolumePayload() *corev1.Volume {
	return &corev1.Volume{
		Name: "payload",
	}
}

func buildCVOVolumeUpdatePayloads(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}

func buildCVOVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.KASServiceKubeconfigSecret("").Name
}

func buildCVOVolumePayload(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}
