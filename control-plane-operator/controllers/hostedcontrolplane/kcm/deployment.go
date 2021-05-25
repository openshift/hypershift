package kcm

import (
	"fmt"
	"path"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

const (
	AWSCloudProviderCredsKey = "credentials"
)

var (
	volumeMounts = util.PodVolumeMounts{
		kcmContainerMain().Name: {
			kcmVolumeConfig().Name:        "/etc/kubernetes/config",
			kcmVolumeRootCA().Name:        "/etc/kubernetes/certs/root-ca",
			kcmVolumeWorkLogs().Name:      "/var/log/kube-controller-manager",
			kcmVolumeKubeconfig().Name:    "/etc/kubernetes/secrets/svc-kubeconfig",
			kcmVolumeCertDir().Name:       "/var/run/kubernetes",
			kcmVolumeClusterSigner().Name: "/etc/kubernetes/certs/cluster-signer",
			kcmVolumeServiceSigner().Name: "/etc/kubernetes/certs/service-signer",
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
	cloudProviderCredsVolumeMount = util.PodVolumeMounts{
		kcmContainerMain().Name: {
			kcmVolumeCloudProviderCreds().Name: "/etc/kubernetes/secrets/cloud-provider",
		},
	}
	kcmLabels = map[string]string{
		"app": "kube-controller-manager",
	}
)

func (p *KubeControllerManagerParams) ReconcileDeployment(deployment *appsv1.Deployment, servingCA *corev1.ConfigMap) error {
	deployment.Spec.Replicas = pointer.Int32Ptr(int32(p.Replicas))
	deployment.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: kcmLabels,
	}
	deployment.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
	maxSurge := intstr.FromInt(3)
	maxUnavailable := intstr.FromInt(1)
	deployment.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{
		MaxSurge:       &maxSurge,
		MaxUnavailable: &maxUnavailable,
	}
	if deployment.Spec.Template.ObjectMeta.Labels == nil {
		deployment.Spec.Template.ObjectMeta.Labels = map[string]string{}
	}
	for k, v := range kcmLabels {
		deployment.Spec.Template.ObjectMeta.Labels[k] = v
	}
	deployment.Spec.Template.Spec = corev1.PodSpec{
		AutomountServiceAccountToken: pointer.BoolPtr(false),
		Containers: []corev1.Container{
			util.BuildContainer(kcmContainerMain(), p.buildKCMContainerMain),
		},
		Volumes: []corev1.Volume{
			util.BuildVolume(kcmVolumeConfig(), buildKCMVolumeConfig),
			util.BuildVolume(kcmVolumeRootCA(), buildKCMVolumeRootCA),
			util.BuildVolume(kcmVolumeWorkLogs(), buildKCMVolumeWorkLogs),
			util.BuildVolume(kcmVolumeKubeconfig(), buildKCMVolumeKubeconfig),
			util.BuildVolume(kcmVolumeClusterSigner(), buildKCMVolumeClusterSigner),
			util.BuildVolume(kcmVolumeCertDir(), buildKCMVolumeCertDir),
			util.BuildVolume(kcmVolumeServiceSigner(), buildKCMVolumeServiceSigner),
		},
	}
	p.LivenessProbes.ApplyTo(&deployment.Spec.Template.Spec)
	p.ReadinessProbes.ApplyTo(&deployment.Spec.Template.Spec)
	p.Scheduling.ApplyTo(&deployment.Spec.Template.Spec)
	p.SecurityContexts.ApplyTo(&deployment.Spec.Template.Spec)
	p.Resources.ApplyTo(&deployment.Spec.Template.Spec)
	if servingCA != nil {
		p.applyServingCAVolume(&deployment.Spec.Template.Spec, servingCA)
	}
	p.applyCloudConfigVolumeMount(&deployment.Spec.Template.Spec)
	p.applyCloudProviderCreds(&deployment.Spec.Template.Spec)
	return nil
}

func kcmContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "kube-controller-manager",
	}
}

func (p *KubeControllerManagerParams) buildKCMContainerMain(c *corev1.Container) {
	c.Image = p.HyperkubeImage
	c.Command = []string{
		"hyperkube",
		"kube-controller-manager",
	}
	c.Args = p.kcmArgs()
	c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
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

func kcmVolumeRootCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "root-ca",
	}
}

func buildKCMVolumeRootCA(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.RootCASecret("").Name,
	}
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

func (p *KubeControllerManagerParams) buildKCMVolumeCloudConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = p.CloudProviderConfig.Name
}

func kcmVolumeCloudProviderCreds() *corev1.Volume {
	return &corev1.Volume{
		Name: "cloud-creds",
	}
}

func (p *KubeControllerManagerParams) buildKCMVolumeCloudProviderCreds(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = p.CloudProviderCreds.Name
}

type serviceCAVolumeBuilder string

func (name serviceCAVolumeBuilder) buildKCMVolumeServiceServingCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = string(name)
}

func (p *KubeControllerManagerParams) applyServingCAVolume(ps *corev1.PodSpec, cm *corev1.ConfigMap) {
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

func (p *KubeControllerManagerParams) kcmArgs() []string {
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
	if providerConfig := p.cloudProviderConfig(); providerConfig != "" {
		args = append(args, fmt.Sprintf("--cloud-config=%s", providerConfig))
	}
	if p.CloudProvider != "" {
		args = append(args, fmt.Sprintf("--cloud-provider=%s", p.CloudProvider))
	}
	args = append(args, []string{
		fmt.Sprintf("--cert-dir=%s", cpath(kcmVolumeCertDir().Name, "")),
		fmt.Sprintf("--cluster-cidr=%s", clusterCIDR(&p.Network.Spec)),
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
		fmt.Sprintf("--root-ca-file=%s", cpath(kcmVolumeRootCA().Name, pki.CASignerCertMapKey)),
		fmt.Sprintf("--secure-port=%d", DefaultPort),
		fmt.Sprintf("--service-account-private-key-file=%s", cpath(kcmVolumeServiceSigner().Name, pki.ServiceSignerPrivateKey)),
		fmt.Sprintf("--service-cluster-ip-range=%s", serviceCIDR(&p.Network.Spec)),
		"--use-service-account-credentials=true",
		"--experimental-cluster-signing-duration=26280h",
	}...)
	for _, f := range config.FeatureGates(&p.FeatureGate.Spec.FeatureGateSelection) {
		args = append(args, fmt.Sprintf("--feature-gates=%s", f))
	}
	return args
}

func (p *KubeControllerManagerParams) cloudProviderConfig() string {
	if p.CloudProviderConfig.Name != "" {
		cfgDir := cloudProviderConfigVolumeMount.Path(kcmContainerMain().Name, kcmVolumeCloudConfig().Name)
		return path.Join(cfgDir, cloud.ProviderConfigKey(p.CloudProvider))
	}
	return ""
}

func (p *KubeControllerManagerParams) applyCloudConfigVolumeMount(podSpec *corev1.PodSpec) {
	if p.CloudProviderConfig.Name == "" {
		return
	}
	podSpec.Volumes = append(podSpec.Volumes, util.BuildVolume(kcmVolumeCloudConfig(), p.buildKCMVolumeCloudConfig))
	container := mustContainer(podSpec, kcmContainerMain().Name)
	container.VolumeMounts = append(container.VolumeMounts,
		cloudProviderConfigVolumeMount.ContainerMounts(kcmContainerMain().Name)...)
}

func (p *KubeControllerManagerParams) applyCloudProviderCreds(podSpec *corev1.PodSpec) {
	if p.CloudProviderCreds.Name == "" {
		return
	}
	podSpec.Volumes = append(podSpec.Volumes, util.BuildVolume(kcmVolumeCloudProviderCreds(), p.buildKCMVolumeCloudProviderCreds))
	container := mustContainer(podSpec, kcmContainerMain().Name)
	container.VolumeMounts = append(container.VolumeMounts,
		cloudProviderCredsVolumeMount.ContainerMounts(kcmContainerMain().Name)...)

	switch p.CloudProvider {
	case aws.Provider:
		credsPath := path.Join(cloudProviderCredsVolumeMount.Path(kcmContainerMain().Name, kcmVolumeCloudProviderCreds().Name), AWSCloudProviderCredsKey)
		container.Env = append(container.Env,
			corev1.EnvVar{
				Name:  "AWS_SHARED_CREDENTIALS_FILE",
				Value: credsPath,
			},
			corev1.EnvVar{
				Name:  "AWS_EC2_METADATA_DISABLED",
				Value: "true",
			})
	}
}

func clusterCIDR(networkSpec *configv1.NetworkSpec) string {
	if len(networkSpec.ClusterNetwork) > 0 {
		return networkSpec.ClusterNetwork[0].CIDR
	}
	return ""
}

func serviceCIDR(networkSpec *configv1.NetworkSpec) string {
	if len(networkSpec.ServiceNetwork) > 0 {
		return networkSpec.ServiceNetwork[0]
	}
	return ""
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
