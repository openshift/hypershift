package kcm

import (
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/util"
)

const (
	AWSCloudProviderCredsKey = "credentials"
	configHashAnnotation     = "kube-controller-manager.hypershift.openshift.io/config-hash"
)

var (
	volumeMounts = util.PodVolumeMounts{
		kcmContainerMain().Name: {
			kcmVolumeConfig().Name:        "/etc/kubernetes/config",
			kcmVolumeCombinedCA().Name:    "/etc/kubernetes/certs/combined-ca",
			kcmVolumeWorkLogs().Name:      "/var/log/kube-controller-manager",
			kcmVolumeKubeconfig().Name:    "/etc/kubernetes/secrets/svc-kubeconfig",
			kcmVolumeCertDir().Name:       "/var/run/kubernetes",
			kcmVolumeClusterSigner().Name: "/etc/kubernetes/certs/cluster-signer",
			kcmVolumeServiceSigner().Name: "/etc/kubernetes/certs/service-signer",
			kcmVolumeServerCert().Name:    "/etc/kubernetes/certs/server",
		},
	}
	serviceServingCAMount = util.PodVolumeMounts{
		kcmContainerMain().Name: {
			kcmVolumeServiceServingCA().Name: "/etc/kubernetes/certs/service-ca",
		},
	}
	cloudProviderConfigVolumeMount = util.PodVolumeMounts{
		kcmContainerMain().Name: {
			kcmVolumeCloudConfig().Name: "/etc/kubernetes/cloud",
		},
	}
)

func kcmLabels() map[string]string {
	return map[string]string{
		"app":                         "kube-controller-manager",
		hyperv1.ControlPlaneComponent: "kube-controller-manager",
	}
}

func ReconcileDeployment(deployment *appsv1.Deployment, config, servingCA *corev1.ConfigMap, p *KubeControllerManagerParams, apiPort *int32) error {
	// preserve existing resource requirements for main KCM container
	mainContainer := util.FindContainer(kcmContainerMain().Name, deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		p.DeploymentConfig.SetContainerResourcesIfPresent(mainContainer)
	}

	deployment.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: kcmLabels(),
	}
	deployment.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
	maxSurge := intstr.FromInt(3)
	maxUnavailable := intstr.FromInt(1)
	args := kcmArgs(p)
	deployment.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{
		MaxSurge:       &maxSurge,
		MaxUnavailable: &maxUnavailable,
	}
	if deployment.Spec.Template.ObjectMeta.Labels == nil {
		deployment.Spec.Template.ObjectMeta.Labels = map[string]string{}
	}
	for k, v := range kcmLabels() {
		deployment.Spec.Template.ObjectMeta.Labels[k] = v
	}

	configBytes, ok := config.Data[KubeControllerManagerConfigKey]
	if !ok {
		return fmt.Errorf("kube controller manager: configuration is not present in %s configmap", config.Name)
	}
	if deployment.Spec.Template.ObjectMeta.Annotations == nil {
		deployment.Spec.Template.ObjectMeta.Annotations = map[string]string{}
	}
	deployment.Spec.Template.ObjectMeta.Annotations[configHashAnnotation] = util.ComputeHash(configBytes)

	deployment.Spec.Template.Spec = corev1.PodSpec{
		AutomountServiceAccountToken: pointer.BoolPtr(false),
		Containers: []corev1.Container{
			util.BuildContainer(kcmContainerMain(), buildKCMContainerMain(p.HyperkubeImage, args, DefaultPort)),
		},
		Volumes: []corev1.Volume{
			util.BuildVolume(kcmVolumeConfig(), buildKCMVolumeConfig),
			util.BuildVolume(kcmVolumeCombinedCA(), buildKCMVolumeCombinedCA),
			util.BuildVolume(kcmVolumeWorkLogs(), buildKCMVolumeWorkLogs),
			util.BuildVolume(kcmVolumeKubeconfig(), buildKCMVolumeKubeconfig),
			util.BuildVolume(kcmVolumeClusterSigner(), buildKCMVolumeClusterSigner),
			util.BuildVolume(kcmVolumeCertDir(), buildKCMVolumeCertDir),
			util.BuildVolume(kcmVolumeServiceSigner(), buildKCMVolumeServiceSigner),
			util.BuildVolume(kcmVolumeServerCert(), buildKCMVolumeServerCert),
		},
	}
	p.DeploymentConfig.ApplyTo(deployment)
	if servingCA != nil {
		applyServingCAVolume(&deployment.Spec.Template.Spec, servingCA)
	}
	applyCloudConfigVolumeMount(&deployment.Spec.Template.Spec, p.CloudProviderConfig, p.CloudProvider)
	util.ApplyCloudProviderCreds(&deployment.Spec.Template.Spec, p.CloudProvider, p.CloudProviderCreds, p.TokenMinterImage, kcmContainerMain().Name)

	util.AvailabilityProber(kas.InClusterKASReadyURL(deployment.Namespace, apiPort), p.AvailabilityProberImage, &deployment.Spec.Template.Spec)
	return nil
}

func kcmContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "kube-controller-manager",
	}
}

func buildKCMContainerMain(image string, args []string, port int32) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.Command = []string{
			"hyperkube",
			"kube-controller-manager",
		}
		c.Args = args
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "client",
				ContainerPort: port,
				Protocol:      corev1.ProtocolTCP,
			},
		}
	}
}

func kcmVolumeConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kcm-config",
	}
}

func buildKCMVolumeConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{
		LocalObjectReference: corev1.LocalObjectReference{
			Name: manifests.KCMConfig("").Name,
		},
	}
}

func kcmVolumeCombinedCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "combined-ca",
	}
}

func buildKCMVolumeCombinedCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.CombinedCAConfigMap("").Name
	v.ConfigMap.DefaultMode = pointer.Int32Ptr(420)
}

func kcmVolumeWorkLogs() *corev1.Volume {
	return &corev1.Volume{
		Name: "logs",
	}
}

func buildKCMVolumeWorkLogs(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}

func kcmVolumeServiceSigner() *corev1.Volume {
	return &corev1.Volume{
		Name: "service-signer",
	}
}

func buildKCMVolumeServiceSigner(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.ServiceAccountSigningKeySecret("").Name,
	}
}

func kcmVolumeCertDir() *corev1.Volume {
	return &corev1.Volume{
		Name: "certs",
	}
}

func buildKCMVolumeCertDir(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}

func kcmVolumeClusterSigner() *corev1.Volume {
	return &corev1.Volume{
		Name: "cluster-signer",
	}
}

func buildKCMVolumeClusterSigner(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.ClusterSignerCASecret("").Name,
	}
}

func kcmVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func buildKCMVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KASServiceKubeconfigSecret("").Name,
	}
}

func kcmVolumeServiceServingCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "service-serving-ca",
	}
}

func kcmVolumeCloudConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "cloud-config",
	}
}

func buildKCMVolumeCloudConfig(cloudProviderConfigName string, cloudProviderName string) func(v *corev1.Volume) {
	return func(v *corev1.Volume) {
		if cloudProviderName == azure.Provider {
			v.Secret = &corev1.SecretVolumeSource{SecretName: cloudProviderConfigName}
		} else {
			v.ConfigMap = &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: cloudProviderConfigName}}
		}
	}
}

func kcmVolumeServerCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "server-crt",
	}
}
func buildKCMVolumeServerCert(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = pointer.Int32Ptr(420)
	v.Secret.SecretName = manifests.KCMServerCertSecret("").Name
}

type serviceCAVolumeBuilder string

func (name serviceCAVolumeBuilder) buildKCMVolumeServiceServingCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = string(name)
}

func applyServingCAVolume(ps *corev1.PodSpec, cm *corev1.ConfigMap) {
	builder := serviceCAVolumeBuilder(cm.Name)
	ps.Volumes = append(ps.Volumes, util.BuildVolume(kcmVolumeServiceServingCA(), builder.buildKCMVolumeServiceServingCA))
	var container *corev1.Container
	for i, c := range ps.Containers {
		if c.Name == kcmContainerMain().Name {
			container = &ps.Containers[i]
			break
		}
	}
	if container == nil {
		panic("did not find the main kcm container in pod spec")
	}
	container.VolumeMounts = append(container.VolumeMounts, serviceServingCAMount.ContainerMounts(kcmContainerMain().Name)...)
}

func kcmArgs(p *KubeControllerManagerParams) []string {
	cpath := func(vol, file string) string {
		return path.Join(volumeMounts.Path(kcmContainerMain().Name, vol), file)
	}
	kubeConfigPath := cpath(kcmVolumeKubeconfig().Name, kas.KubeconfigKey)
	args := []string{
		fmt.Sprintf("--openshift-config=%s", cpath(kcmVolumeConfig().Name, KubeControllerManagerConfigKey)),
		fmt.Sprintf("--kubeconfig=%s", kubeConfigPath),
		fmt.Sprintf("--authentication-kubeconfig=%s", kubeConfigPath),
		fmt.Sprintf("--authorization-kubeconfig=%s", kubeConfigPath),
		"--allocate-node-cidrs=true",
	}
	if providerConfig := cloudProviderConfig(p.CloudProvider, p.CloudProviderConfig); providerConfig != "" {
		args = append(args, fmt.Sprintf("--cloud-config=%s", providerConfig))
	}
	if p.CloudProvider != "" {
		args = append(args, fmt.Sprintf("--cloud-provider=%s", p.CloudProvider))
	}
	args = append(args, []string{
		fmt.Sprintf("--cert-dir=%s", cpath(kcmVolumeCertDir().Name, "")),
		fmt.Sprintf("--cluster-cidr=%s", p.PodCIDR),
		fmt.Sprintf("--cluster-signing-cert-file=%s", cpath(kcmVolumeClusterSigner().Name, pki.CASignerCertMapKey)),
		fmt.Sprintf("--cluster-signing-key-file=%s", cpath(kcmVolumeClusterSigner().Name, pki.CASignerKeyMapKey)),
		"--configure-cloud-routes=false",
		"--controllers=*",
		"--controllers=-ttl",
		"--controllers=-bootstrapsigner",
		"--controllers=-tokencleaner",
		"--enable-dynamic-provisioning=true",
		"--flex-volume-plugin-dir=/etc/kubernetes/kubelet-plugins/volume/exec",
		"--kube-api-burst=300",
		"--kube-api-qps=150",
		"--leader-elect-resource-lock=configmaps",
		"--leader-elect=true",
		"--leader-elect-retry-period=3s",
		"--port=0",
		fmt.Sprintf("--root-ca-file=%s", cpath(kcmVolumeCombinedCA().Name, pki.CASignerCertMapKey)),
		fmt.Sprintf("--secure-port=%d", DefaultPort),
		fmt.Sprintf("--service-account-private-key-file=%s", cpath(kcmVolumeServiceSigner().Name, pki.ServiceSignerPrivateKey)),
		fmt.Sprintf("--service-cluster-ip-range=%s", p.ServiceCIDR),
		"--use-service-account-credentials=true",
		"--experimental-cluster-signing-duration=17520h",
		fmt.Sprintf("--tls-cert-file=%s", cpath(kcmVolumeServerCert().Name, corev1.TLSCertKey)),
		fmt.Sprintf("--tls-private-key-file=%s", cpath(kcmVolumeServerCert().Name, corev1.TLSPrivateKeyKey)),
	}...)
	for _, f := range p.FeatureGates() {
		args = append(args, fmt.Sprintf("--feature-gates=%s", f))
	}
	return args
}

func cloudProviderConfig(cloudProvider string, configRef *corev1.LocalObjectReference) string {
	if configRef != nil && configRef.Name != "" {
		cfgDir := cloudProviderConfigVolumeMount.Path(kcmContainerMain().Name, kcmVolumeCloudConfig().Name)
		return path.Join(cfgDir, cloud.ProviderConfigKey(cloudProvider))
	}
	return ""
}

func applyCloudConfigVolumeMount(podSpec *corev1.PodSpec, cloudProviderConfigRef *corev1.LocalObjectReference, cloudProvider string) {
	if cloudProviderConfigRef == nil || cloudProviderConfigRef.Name == "" {
		return
	}
	podSpec.Volumes = append(podSpec.Volumes, util.BuildVolume(kcmVolumeCloudConfig(), buildKCMVolumeCloudConfig(cloudProviderConfigRef.Name, cloudProvider)))
	container := mustContainer(podSpec, kcmContainerMain().Name)
	container.VolumeMounts = append(container.VolumeMounts,
		cloudProviderConfigVolumeMount.ContainerMounts(kcmContainerMain().Name)...)
}

func mustContainer(podSpec *corev1.PodSpec, name string) *corev1.Container {
	var container *corev1.Container
	for i, c := range podSpec.Containers {
		if c.Name == name {
			container = &podSpec.Containers[i]
			break
		}
	}
	if container == nil {
		panic(fmt.Sprintf("expected container %s not found pod spec", name))
	}
	return container
}
