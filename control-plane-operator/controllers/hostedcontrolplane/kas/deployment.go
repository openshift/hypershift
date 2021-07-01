package kas

import (
	"fmt"
	"path"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

const (
	kasNamedCertificateMountPathPrefix = "/etc/kubernetes/certs/named"
)

var (
	volumeMounts = util.PodVolumeMounts{
		kasContainerBootstrap().Name: {
			kasVolumeBootstrapManifests().Name: "/work",
		},
		kasContainerApplyBootstrap().Name: {
			kasVolumeBootstrapManifests().Name:  "/work",
			kasVolumeLocalhostKubeconfig().Name: "/var/secrets/localhost-kubeconfig",
		},
		kasContainerMain().Name: {
			kasVolumeWorkLogs().Name:               "/var/log/kube-apiserver",
			kasVolumeConfig().Name:                 "/etc/kubernetes/config",
			kasVolumeAuditConfig().Name:            "/etc/kubernetes/audit",
			kasVolumeRootCA().Name:                 "/etc/kubernetes/certs/root-ca",
			kasVolumeServerCert().Name:             "/etc/kubernetes/certs/server",
			kasVolumeAggregatorCert().Name:         "/etc/kubernetes/certs/aggregator",
			kasVolumeAggregatorCA().Name:           "/etc/kubernetes/certs/aggregator-ca",
			kasVolumeClientCA().Name:               "/etc/kubernetes/certs/client-ca",
			kasVolumeEtcdClientCert().Name:         "/etc/kubernetes/certs/etcd",
			kasVolumeServiceAccountKey().Name:      "/etc/kubernetes/secrets/svcacct-key",
			kasVolumeOauthMetadata().Name:          "/etc/kubernetes/oauth",
			kasVolumeKubeletClientCert().Name:      "/etc/kubernetes/certs/kubelet",
			kasVolumeKubeletClientCA().Name:        "/etc/kubernetes/certs/kubelet-ca",
			kasVolumeKonnectivityClientCert().Name: "/etc/kubernetes/certs/konnectivity-client",
			kasVolumeEgressSelectorConfig().Name:   "/etc/kubernetes/egress-selector",
		},
		kasContainerPortieries().Name: {
			kasVolumeLocalhostKubeconfig().Name: "/etc/openshift/kubeconfig",
			kasVolumePortierisCerts().Name:      "/etc/certs",
		},
		kasContainerKMS().Name: {
			kasVolumeKMSSocket().Name: "/tmp",
			kasVolumeKMSKP().Name:     "/tmp/kp",
		},
	}

	cloudProviderConfigVolumeMount = util.PodVolumeMounts{
		kasContainerMain().Name: {
			kasVolumeCloudConfig().Name: "/etc/kubernetes/cloud",
		},
	}

	kasAuditWebhookConfigFileVolumeMount = util.PodVolumeMounts{
		kasContainerMain().Name: {
			kasAuditWebhookConfigFileVolume().Name: "/etc/kubernetes/auditwebhook",
		},
	}

	kasKMSVolumeMounts = util.PodVolumeMounts{
		kasContainerMain().Name: {
			kasVolumeKMSSocket().Name:     "/tmp",
			kasVolumeKMSConfigFile().Name: "/etc/kubernetes/kms-config",
		},
	}

	// volume mounts in apply bootstrap container
	applyWorkMountPath       = "/work"
	applyKubeconfigMountPath = "/var/secrets/localhost-kubeconfig"
)

var kasLabels = map[string]string{
	"app": "kube-apiserver",
}

func ReconcileKubeAPIServerDeployment(deployment *appsv1.Deployment,
	ownerRef config.OwnerRef,
	deploymentConfig config.DeploymentConfig,
	namedCertificates []configv1.APIServerNamedServingCert,
	cloudProviderConfigRef *corev1.LocalObjectReference,
	images KubeAPIServerImages,
	auditWebhookRef *corev1.LocalObjectReference,
	kmsKPInfo string,
	kmsKPRegion string,
) error {

	ownerRef.ApplyTo(deployment)
	maxSurge := intstr.FromInt(3)
	maxUnavailable := intstr.FromInt(0)
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: kasLabels,
		},
		Strategy: appsv1.DeploymentStrategy{
			Type: appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{
				MaxSurge:       &maxSurge,
				MaxUnavailable: &maxUnavailable,
			},
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: kasLabels,
			},
			Spec: corev1.PodSpec{
				AutomountServiceAccountToken: pointer.BoolPtr(false),
				InitContainers: []corev1.Container{
					util.BuildContainer(kasContainerBootstrap(), buildKASContainerBootstrap(images.ClusterConfigOperator)),
				},
				Containers: []corev1.Container{
					util.BuildContainer(kasContainerApplyBootstrap(), buildKASContainerApplyBootstrap(images.CLI)),
					util.BuildContainer(kasContainerMain(), buildKASContainerMain(images.HyperKube)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(kasVolumeBootstrapManifests(), buildKASVolumeBootstrapManifests),
					util.BuildVolume(kasVolumeLocalhostKubeconfig(), buildKASVolumeLocalhostKubeconfig),
					util.BuildVolume(kasVolumeWorkLogs(), buildKASVolumeWorkLogs),
					util.BuildVolume(kasVolumeConfig(), buildKASVolumeConfig),
					util.BuildVolume(kasVolumeAuditConfig(), buildKASVolumeAuditConfig),
					util.BuildVolume(kasVolumeRootCA(), buildKASVolumeRootCA),
					util.BuildVolume(kasVolumeServerCert(), buildKASVolumeServerCert),
					util.BuildVolume(kasVolumeAggregatorCert(), buildKASVolumeAggregatorCert),
					util.BuildVolume(kasVolumeAggregatorCA(), buildKASVolumeAggregatorCA),
					util.BuildVolume(kasVolumeServiceAccountKey(), buildKASVolumeServiceAccountKey),
					util.BuildVolume(kasVolumeEtcdClientCert(), buildKASVolumeEtcdClientCert),
					util.BuildVolume(kasVolumeOauthMetadata(), buildKASVolumeOauthMetadata),
					util.BuildVolume(kasVolumeClientCA(), buildKASVolumeClientCA),
					util.BuildVolume(kasVolumeKubeletClientCert(), buildKASVolumeKubeletClientCert),
					util.BuildVolume(kasVolumeKubeletClientCA(), buildKASVolumeKubeletClientCA),
					util.BuildVolume(kasVolumeKonnectivityClientCert(), buildKASVolumeKonnectivityClientCert),
					util.BuildVolume(kasVolumeEgressSelectorConfig(), buildKASVolumeEgressSelectorConfig),
				},
			},
		},
	}
	if len(images.Portieris) > 0 {
		deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, util.BuildContainer(kasContainerPortieries(), buildKASContainerPortieries(images.Portieris)))
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, util.BuildVolume(kasVolumePortierisCerts(), buildKASVolumePortierisCerts))
	}
	if len(images.KMS) > 0 {
		deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, util.BuildContainer(kasContainerKMS(), buildKASContainerKMS(images.KMS, kmsKPRegion, kmsKPInfo)))
		applyKMSConfig(&deployment.Spec.Template.Spec)
	}
	deploymentConfig.ApplyTo(deployment)
	applyNamedCertificateMounts(namedCertificates, &deployment.Spec.Template.Spec)
	applyCloudConfigVolumeMount(cloudProviderConfigRef, &deployment.Spec.Template.Spec)
	if auditWebhookRef != nil {
		applyKASAuditWebhookConfigFileVolume(&deployment.Spec.Template.Spec, auditWebhookRef)
	}
	return nil
}

func kasContainerBootstrap() *corev1.Container {
	return &corev1.Container{
		Name: "init-bootstrap",
	}
}

func buildKASContainerBootstrap(image string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Command = []string{
			"/bin/bash",
		}
		c.Args = []string{
			"-c",
			invokeBootstrapRenderScript(volumeMounts.Path(kasContainerBootstrap().Name, kasVolumeBootstrapManifests().Name)),
		}
		c.Image = image
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func kasContainerApplyBootstrap() *corev1.Container {
	return &corev1.Container{
		Name: "apply-bootstrap",
	}
}

func buildKASContainerApplyBootstrap(image string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.Command = []string{
			"/bin/bash",
		}
		c.Args = []string{
			"-c",
			applyBootstrapManifestsScript(volumeMounts.Path(c.Name, kasVolumeBootstrapManifests().Name)),
		}
		c.Env = []corev1.EnvVar{
			{
				Name:  "KUBECONFIG",
				Value: path.Join(volumeMounts.Path(c.Name, kasVolumeLocalhostKubeconfig().Name), KubeconfigKey),
			},
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func kasContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "kube-apiserver",
	}
}

func buildKASContainerMain(image string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.Command = []string{
			"hyperkube",
		}
		c.Args = []string{
			"kube-apiserver",
			fmt.Sprintf("--openshift-config=%s", path.Join(volumeMounts.Path(c.Name, kasVolumeConfig().Name), KubeAPIServerConfigKey)),
			"-v5",
		}
		c.WorkingDir = volumeMounts.Path(c.Name, kasVolumeWorkLogs().Name)
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func kasVolumeBootstrapManifests() *corev1.Volume {
	return &corev1.Volume{
		Name: "bootstrap-manifests",
	}
}

func buildKASVolumeBootstrapManifests(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}

func kasVolumeLocalhostKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "localhost-kubeconfig",
	}
}
func buildKASVolumeLocalhostKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KASLocalhostKubeconfigSecret("").Name,
	}
}

func kasVolumeWorkLogs() *corev1.Volume {
	return &corev1.Volume{
		Name: "logs",
	}
}
func buildKASVolumeWorkLogs(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}
func kasVolumeConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kas-config",
	}
}
func buildKASVolumeConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.KASConfig("").Name
}
func kasVolumeAuditConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "audit-config",
	}
}
func buildKASVolumeAuditConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.KASAuditConfig("").Name
}
func kasVolumeRootCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "root-ca",
	}
}
func buildKASVolumeRootCA(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.RootCASecret("").Name,
	}
}

// TODO: generate separate volume to merge our CA with user-supplied CA
func kasVolumeClientCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "client-ca",
	}
}
func buildKASVolumeClientCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.CombinedCAConfigMap("").Name
}

func kasVolumeServerCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "server-crt",
	}
}
func buildKASVolumeServerCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KASServerCertSecret("").Name,
	}
}

func kasVolumeKubeletClientCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubelet-client-ca",
	}
}
func buildKASVolumeKubeletClientCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.CombinedCAConfigMap("").Name
}

func kasVolumeKonnectivityClientCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "konnectivity-client",
	}
}
func buildKASVolumeKonnectivityClientCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KonnectivityClientSecret("").Name,
	}
}

func kasVolumeAggregatorCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "aggregator-crt",
	}
}
func buildKASVolumeAggregatorCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KASAggregatorCertSecret("").Name,
	}
}

func kasVolumeAggregatorCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "aggregator-ca",
	}
}
func buildKASVolumeAggregatorCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.CombinedCAConfigMap("").Name
}

func kasVolumeEgressSelectorConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "egress-selector-config",
	}
}
func buildKASVolumeEgressSelectorConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.KASEgressSelectorConfig("").Name
}

func kasVolumeServiceAccountKey() *corev1.Volume {
	return &corev1.Volume{
		Name: "svcacct-key",
	}
}
func buildKASVolumeServiceAccountKey(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.ServiceAccountSigningKeySecret("").Name,
	}
}

func kasVolumeKubeletClientCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubelet-client-crt",
	}
}

func buildKASVolumeKubeletClientCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KASKubeletClientCertSecret("").Name,
	}
}

func kasVolumeEtcdClientCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "etcd-client-crt",
	}
}
func buildKASVolumeEtcdClientCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.EtcdClientSecret("").Name,
	}
}

func kasVolumeOauthMetadata() *corev1.Volume {
	return &corev1.Volume{
		Name: "oauth-metadata",
	}
}
func buildKASVolumeOauthMetadata(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.KASOAuthMetadata("").Name
}

func kasVolumeCloudConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "cloud-config",
	}
}

func buildKASVolumeCloudConfig(configMapName string) func(v *corev1.Volume) {
	return func(v *corev1.Volume) {
		v.ConfigMap = &corev1.ConfigMapVolumeSource{}
		v.ConfigMap.Name = configMapName
	}
}

func applyCloudConfigVolumeMount(configRef *corev1.LocalObjectReference, podSpec *corev1.PodSpec) {
	if configRef != nil && configRef.Name != "" {
		podSpec.Volumes = append(podSpec.Volumes, util.BuildVolume(kasVolumeCloudConfig(), buildKASVolumeCloudConfig(configRef.Name)))
		var container *corev1.Container
		for i, c := range podSpec.Containers {
			if c.Name == kasContainerMain().Name {
				container = &podSpec.Containers[i]
				break
			}
		}
		if container == nil {
			panic("main kube apiserver container not found in spec")
		}
		container.VolumeMounts = append(container.VolumeMounts,
			cloudProviderConfigVolumeMount.ContainerMounts(kasContainerMain().Name)...)
	}
}

func invokeBootstrapRenderScript(workDir string) string {
	var script = `#!/bin/sh
cd /tmp
mkdir input output
/usr/bin/cluster-config-operator render \
   --config-output-file config \
   --asset-input-dir /tmp/input \
   --asset-output-dir /tmp/output
cp /tmp/output/manifests/* %[1]s
`
	return fmt.Sprintf(script, workDir)
}

func applyBootstrapManifestsScript(workDir string) string {
	var script = `#!/bin/sh
while true; do
  if oc apply -f %[1]s; then
    echo "Bootstrap manifests applied successfully."
    break
  fi
  sleep 1
done
while true; do
  sleep 1000
done
`
	return fmt.Sprintf(script, workDir)
}

func applyNamedCertificateMounts(certs []configv1.APIServerNamedServingCert, spec *corev1.PodSpec) {
	var container *corev1.Container
	for i := range spec.Containers {
		if spec.Containers[i].Name == kasContainerMain().Name {
			container = &spec.Containers[i]
			break
		}
	}
	if container == nil {
		panic("Kube APIServer container not found")
	}
	for i, namedCert := range certs {
		volumeName := fmt.Sprintf("named-cert-%d", i+1)
		spec.Volumes = append(spec.Volumes, corev1.Volume{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: namedCert.ServingCertificate.Name,
				},
			},
		})
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      volumeName,
			MountPath: fmt.Sprintf("%s-%d", kasNamedCertificateMountPathPrefix, i+1),
		})
	}
}

func kasAuditWebhookConfigFileVolume() *corev1.Volume {
	return &corev1.Volume{
		Name: "kas-audit-webhook",
	}
}

func buildKASAuditWebhookConfigFileVolume(auditWebhookRef *corev1.LocalObjectReference) func(v *corev1.Volume) {
	return func(v *corev1.Volume) {
		v.Secret = &corev1.SecretVolumeSource{}
		v.Secret.SecretName = auditWebhookRef.Name
	}
}

func applyKASAuditWebhookConfigFileVolume(podSpec *corev1.PodSpec, auditWebhookRef *corev1.LocalObjectReference) {
	podSpec.Volumes = append(podSpec.Volumes, util.BuildVolume(kasAuditWebhookConfigFileVolume(), buildKASAuditWebhookConfigFileVolume(auditWebhookRef)))
	var container *corev1.Container
	for i, c := range podSpec.Containers {
		if c.Name == kasContainerMain().Name {
			container = &podSpec.Containers[i]
			break
		}
	}
	if container == nil {
		panic("main kube apiserver container not found in spec")
	}
	container.VolumeMounts = append(container.VolumeMounts,
		kasAuditWebhookConfigFileVolumeMount.ContainerMounts(kasContainerMain().Name)...)
}

func kasContainerPortieries() *corev1.Container {
	return &corev1.Container{
		Name: "portieris",
	}
}

func buildKASContainerPortieries(image string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullAlways
		c.Command = []string{
			"/portieris",
		}
		c.Args = []string{
			"--kubeconfig=/etc/openshift/kubeconfig/kubeconfig",
			"--alsologtostderr",
			"-v=4",
		}
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: 8000,
				Protocol:      corev1.ProtocolTCP,
			},
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func kasVolumePortierisCerts() *corev1.Volume {
	return &corev1.Volume{
		Name: "portieris-certs",
	}
}

func buildKASVolumePortierisCerts(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: v.Name,
	}
}

func kasContainerKMS() *corev1.Container {
	return &corev1.Container{
		Name: "kms",
	}
}

func kasVolumeKMSSocket() *corev1.Volume {
	return &corev1.Volume{
		Name: "kms-socket",
	}
}

func buildVolumeKMSSocket(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}

func kasVolumeKMSKP() *corev1.Volume {
	return &corev1.Volume{
		Name: "kms-kp",
	}
}

func buildVolumeKMSKP(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.KASKMSWDEKSecret("").Name
	optionalMount := true
	v.Secret.Optional = &optionalMount
}

func kasVolumeKMSConfigFile() *corev1.Volume {
	return &corev1.Volume{
		Name: "kms-config",
	}
}

func buildVolumeKMSConfigFile(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.KASKMSConfigFile("").Name

}

func buildKASContainerKMS(image string, region string, kmsInfo string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullAlways
		c.Env = []corev1.EnvVar{
			corev1.EnvVar{
				Name:  "LOG_LEVEL",
				Value: "info",
			},
			corev1.EnvVar{
				Name:  "NUM_LEN_BYTES",
				Value: "4",
			},
			corev1.EnvVar{
				Name:  "CACHE_TIMEOUT_IN_HOURS",
				Value: "1",
			},
			corev1.EnvVar{
				Name:  "RESTART_DELAY_IN_SECONDS",
				Value: "0",
			},
			corev1.EnvVar{
				Name:  "UNIX_SOCKET_PATH",
				Value: "/tmp/keyprotectprovider.sock",
			},
			corev1.EnvVar{
				Name:  "KP_TIMEOUT",
				Value: "10",
			},
			corev1.EnvVar{
				Name:  "KP_WDEK_PATH",
				Value: "/tmp/kp/wdek",
			},
			corev1.EnvVar{
				Name:  "KP_STATE_PATH",
				Value: "/tmp/kp/state",
			},
			corev1.EnvVar{
				Name:  "HEALTHZ_PATH",
				Value: "/healthz",
			},
			corev1.EnvVar{
				Name:  "HEALTHZ_PORT",
				Value: ":8081",
			},
			corev1.EnvVar{
				Name:  "KP_DATA_JSON",
				Value: kmsInfo,
			},
			corev1.EnvVar{
				Name:  "REGION",
				Value: region,
			},
		}
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: 8001,
				Protocol:      corev1.ProtocolTCP,
			},
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func applyKMSConfig(podSpec *corev1.PodSpec) {
	podSpec.Volumes = append(podSpec.Volumes, util.BuildVolume(kasVolumeKMSKP(), buildVolumeKMSKP), util.BuildVolume(kasVolumeKMSSocket(), buildVolumeKMSSocket), util.BuildVolume(kasVolumeKMSConfigFile(), buildVolumeKMSConfigFile))
	var container *corev1.Container
	for i, c := range podSpec.Containers {
		if c.Name == kasContainerMain().Name {
			container = &podSpec.Containers[i]
			break
		}
	}
	container.Args = append(container.Args, fmt.Sprintf("--encryption-provider-config=%s/config.yaml", kasKMSVolumeMounts.Path(kasContainerMain().Name, kasVolumeKMSConfigFile().Name)))
	if container == nil {
		panic("main kube apiserver container not found in spec")
	}
	container.VolumeMounts = append(container.VolumeMounts,
		kasKMSVolumeMounts.ContainerMounts(kasContainerMain().Name)...)
}
