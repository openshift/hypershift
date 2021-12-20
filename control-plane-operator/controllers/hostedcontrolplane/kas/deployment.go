package kas

import (
	"fmt"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"path"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

const (
	kasNamedCertificateMountPathPrefix = "/etc/kubernetes/certs/named"
	configHashAnnotation               = "kube-apiserver.hypershift.openshift.io/config-hash"
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
			kasVolumeAuthTokenWebhookConfig().Name: "/etc/kubernetes/auth-token-webhook",
			kasVolumeKubeletClientCert().Name:      "/etc/kubernetes/certs/kubelet",
			kasVolumeKubeletClientCA().Name:        "/etc/kubernetes/certs/kubelet-ca",
			kasVolumeKonnectivityClientCert().Name: "/etc/kubernetes/certs/konnectivity-client",
			kasVolumeEgressSelectorConfig().Name:   "/etc/kubernetes/egress-selector",
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

	genericSecretEncryptionConfigFileVolumeMount = util.PodVolumeMounts{
		kasContainerMain().Name: {
			kasVolumeSecretEncryptionConfigFile().Name: "/etc/kubernetes/secret-encryption",
		},
	}
)

func kasLabels() map[string]string {
	return map[string]string{
		"app":                         "kube-apiserver",
		hyperv1.ControlPlaneComponent: "kube-apiserver",
	}
}

func ReconcileKubeAPIServerDeployment(deployment *appsv1.Deployment,
	ownerRef config.OwnerRef,
	deploymentConfig config.DeploymentConfig,
	namedCertificates []configv1.APIServerNamedServingCert,
	cloudProviderName string,
	cloudProviderConfigRef *corev1.LocalObjectReference,
	cloudProviderCreds *corev1.LocalObjectReference,
	images KubeAPIServerImages,
	config *corev1.ConfigMap,
	auditWebhookRef *corev1.LocalObjectReference,
	secretEncryptionData *hyperv1.SecretEncryptionSpec,
	aesCBCActiveKey []byte,
	aesCBCBackupKey []byte,
) error {

	configBytes, ok := config.Data[KubeAPIServerConfigKey]
	if !ok {
		return fmt.Errorf("kube apiserver configuration is not expected to be empty")
	}
	configHash := util.ComputeHash(configBytes)

	ownerRef.ApplyTo(deployment)
	maxSurge := intstr.FromInt(3)
	maxUnavailable := intstr.FromInt(0)

	// preserve existing resource requirements for main KAS container
	mainContainer := findContainer(kasContainerMain().Name, deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		resources := mainContainer.Resources
		if len(resources.Requests) > 0 || len(resources.Limits) > 0 {
			if deploymentConfig.Resources != nil {
				deploymentConfig.Resources[kasContainerMain().Name] = resources
			}
		}
	}

	deployment.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: kasLabels(),
	}
	deployment.Spec.Strategy = appsv1.DeploymentStrategy{
		Type: appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{
			MaxSurge:       &maxSurge,
			MaxUnavailable: &maxUnavailable,
		},
	}
	deployment.Spec.Template = corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: kasLabels(),
			Annotations: map[string]string{
				configHashAnnotation: configHash,
			},
		},
		Spec: corev1.PodSpec{
			DNSPolicy:       corev1.DNSClusterFirst,
			RestartPolicy:   corev1.RestartPolicyAlways,
			SecurityContext: &corev1.PodSecurityContext{},
			// The KAS takes 90 seconds to finish its graceful shutdown, give it enough
			// time to do that + 5 seconds margin. The shutdown sequence is described
			// in detail here: https://github.com/openshift/installer/blob/master/docs/dev/kube-apiserver-health-check.md
			TerminationGracePeriodSeconds: pointer.Int64Ptr(95),
			SchedulerName:                 corev1.DefaultSchedulerName,
			AutomountServiceAccountToken:  pointer.BoolPtr(false),
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
				util.BuildVolume(kasVolumeAuthTokenWebhookConfig(), buildKASVolumeAuthTokenWebhookConfig),
				util.BuildVolume(kasVolumeClientCA(), buildKASVolumeClientCA),
				util.BuildVolume(kasVolumeKubeletClientCert(), buildKASVolumeKubeletClientCert),
				util.BuildVolume(kasVolumeKubeletClientCA(), buildKASVolumeKubeletClientCA),
				util.BuildVolume(kasVolumeKonnectivityClientCert(), buildKASVolumeKonnectivityClientCert),
				util.BuildVolume(kasVolumeEgressSelectorConfig(), buildKASVolumeEgressSelectorConfig),
				util.BuildVolume(kasVolumeKubeconfig(), buildKASVolumeKubeconfig),
			},
		},
	}
	if len(images.Portieris) > 0 {
		applyPortieriesConfig(&deployment.Spec.Template.Spec, images.Portieris)
	}
	applyNamedCertificateMounts(namedCertificates, &deployment.Spec.Template.Spec)
	applyCloudConfigVolumeMount(cloudProviderConfigRef, &deployment.Spec.Template.Spec)
	util.ApplyCloudProviderCreds(&deployment.Spec.Template.Spec, cloudProviderName, cloudProviderCreds, images.TokenMinterImage, kasContainerMain().Name)

	if auditWebhookRef != nil {
		applyKASAuditWebhookConfigFileVolume(&deployment.Spec.Template.Spec, auditWebhookRef)
	}

	if secretEncryptionData != nil {
		applyGenericSecretEncryptionConfig(&deployment.Spec.Template.Spec)
		switch secretEncryptionData.Type {
		case hyperv1.KMS:
			if secretEncryptionData.KMS == nil {
				return fmt.Errorf("kms metadata not specified")
			}
			switch secretEncryptionData.KMS.Provider {
			case hyperv1.IBMCloud:
				err := applyIBMCloudKMSConfig(&deployment.Spec.Template.Spec, secretEncryptionData.KMS.IBMCloud, images.IBMCloudKMS)
				if err != nil {
					return err
				}
			case hyperv1.AWS:
				err := applyAWSKMSConfig(&deployment.Spec.Template.Spec, secretEncryptionData.KMS.AWS.ActiveKey, secretEncryptionData.KMS.AWS.BackupKey, secretEncryptionData.KMS.AWS.Auth, secretEncryptionData.KMS.AWS.Region, images.AWSKMS)
				if err != nil {
					return err
				}
			default:
				return fmt.Errorf("unrecognized secret encryption type %s", secretEncryptionData.Type)
			}
		case hyperv1.AESCBC:
			err := applyAESCBCKeyHashAnnotation(&deployment.Spec.Template, aesCBCActiveKey, aesCBCBackupKey)
			if err != nil {
				return err
			}
		default:
			//nothing needed to be done
		}
	}
	deploymentConfig.ApplyTo(deployment)
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
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.TerminationMessagePolicy = corev1.TerminationMessageReadFile
		c.TerminationMessagePath = corev1.TerminationMessagePathDefault
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
		c.TerminationMessagePolicy = corev1.TerminationMessageReadFile
		c.TerminationMessagePath = corev1.TerminationMessagePathDefault
		c.ImagePullPolicy = corev1.PullIfNotPresent
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
		c.TerminationMessagePolicy = corev1.TerminationMessageReadFile
		c.TerminationMessagePath = corev1.TerminationMessagePathDefault
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Command = []string{
			"hyperkube",
		}
		c.Args = []string{
			"kube-apiserver",
			fmt.Sprintf("--openshift-config=%s", path.Join(volumeMounts.Path(c.Name, kasVolumeConfig().Name), KubeAPIServerConfigKey)),
			"-v2",
		}

		c.Env = []corev1.EnvVar{{
			// Needed by the apirequest count controller, it uses this as its nodeName. Without this, all its requests fail validation
			// as the nodeName is empty. Should be using the hostname, but it appears os.Hostname() doesn't work so it falls back to
			// the value of this env var.
			// * Controller instantiation: https://github.com/openshift/kubernetes/blob/1b2affc8e97007139e70badd729981279d4f5f1b/openshift-kube-apiserver/openshiftkubeapiserver/patch.go#L88
			// * NodeName detection: https://github.com/openshift/kubernetes/blob/1b2affc8e97007139e70badd729981279d4f5f1b/openshift-kube-apiserver/openshiftkubeapiserver/patch.go#L131
			Name:      "HOST_IP",
			ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"}},
		}}
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
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = pointer.Int32Ptr(420)
	v.Secret.SecretName = manifests.KASLocalhostKubeconfigSecret("").Name
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
	if v.ConfigMap == nil {
		v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	}
	v.ConfigMap.DefaultMode = pointer.Int32Ptr(420)
	v.ConfigMap.Name = manifests.KASConfig("").Name
}
func kasVolumeAuditConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "audit-config",
	}
}
func buildKASVolumeAuditConfig(v *corev1.Volume) {
	if v.ConfigMap == nil {
		v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	}
	v.ConfigMap.DefaultMode = pointer.Int32Ptr(420)
	v.ConfigMap.Name = manifests.KASAuditConfig("").Name
}
func kasVolumeRootCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "root-ca",
	}
}
func buildKASVolumeRootCA(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = pointer.Int32Ptr(420)
	v.Secret.SecretName = manifests.RootCASecret("").Name
}

// TODO: generate separate volume to merge our CA with user-supplied CA
func kasVolumeClientCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "client-ca",
	}
}
func buildKASVolumeClientCA(v *corev1.Volume) {
	if v.ConfigMap == nil {
		v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	}
	v.ConfigMap.DefaultMode = pointer.Int32Ptr(420)
	v.ConfigMap.Name = manifests.CombinedCAConfigMap("").Name
}

func kasVolumeServerCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "server-crt",
	}
}
func buildKASVolumeServerCert(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = pointer.Int32Ptr(420)
	v.Secret.SecretName = manifests.KASServerCertSecret("").Name
}

func kasVolumeKubeletClientCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubelet-client-ca",
	}
}
func buildKASVolumeKubeletClientCA(v *corev1.Volume) {
	if v.ConfigMap == nil {
		v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	}
	v.ConfigMap.DefaultMode = pointer.Int32Ptr(420)
	v.ConfigMap.Name = manifests.CombinedCAConfigMap("").Name
}

func kasVolumeKonnectivityClientCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "konnectivity-client",
	}
}
func buildKASVolumeKonnectivityClientCert(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = pointer.Int32Ptr(420)
	v.Secret.SecretName = manifests.KonnectivityClientSecret("").Name
}

func kasVolumeAggregatorCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "aggregator-crt",
	}
}
func buildKASVolumeAggregatorCert(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = pointer.Int32Ptr(420)
	v.Secret.SecretName = manifests.KASAggregatorCertSecret("").Name
}

func kasVolumeAggregatorCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "aggregator-ca",
	}
}
func buildKASVolumeAggregatorCA(v *corev1.Volume) {
	if v.ConfigMap == nil {
		v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	}
	v.ConfigMap.DefaultMode = pointer.Int32Ptr(420)
	v.ConfigMap.Name = manifests.CombinedCAConfigMap("").Name
}

func kasVolumeEgressSelectorConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "egress-selector-config",
	}
}

func buildKASVolumeEgressSelectorConfig(v *corev1.Volume) {
	if v.ConfigMap == nil {
		v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	}
	v.ConfigMap.DefaultMode = pointer.Int32Ptr(420)
	v.ConfigMap.Name = manifests.KASEgressSelectorConfig("").Name
}

func kasVolumeServiceAccountKey() *corev1.Volume {
	return &corev1.Volume{
		Name: "svcacct-key",
	}
}
func buildKASVolumeServiceAccountKey(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = pointer.Int32Ptr(420)
	v.Secret.SecretName = manifests.ServiceAccountSigningKeySecret("").Name
}

func kasVolumeKubeletClientCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubelet-client-crt",
	}
}

func buildKASVolumeKubeletClientCert(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = pointer.Int32Ptr(420)
	v.Secret.SecretName = manifests.KASKubeletClientCertSecret("").Name
}

func kasVolumeEtcdClientCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "etcd-client-crt",
	}
}
func buildKASVolumeEtcdClientCert(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = pointer.Int32Ptr(420)
	v.Secret.SecretName = manifests.EtcdClientSecret("").Name
}

func kasVolumeOauthMetadata() *corev1.Volume {
	return &corev1.Volume{
		Name: "oauth-metadata",
	}
}
func buildKASVolumeOauthMetadata(v *corev1.Volume) {
	if v.ConfigMap == nil {
		v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	}
	v.ConfigMap.DefaultMode = pointer.Int32Ptr(420)
	v.ConfigMap.Name = manifests.KASOAuthMetadata("").Name
}

func kasVolumeAuthTokenWebhookConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "auth-token-webhook-config",
	}
}
func buildKASVolumeAuthTokenWebhookConfig(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = pointer.Int32Ptr(420)
	v.Secret.SecretName = manifests.KASAuthenticationTokenWebhookConfigSecret("").Name
}

func kasVolumeCloudConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "cloud-config",
	}
}

func buildKASVolumeCloudConfig(configMapName string) func(v *corev1.Volume) {
	return func(v *corev1.Volume) {
		if v.ConfigMap == nil {
			v.ConfigMap = &corev1.ConfigMapVolumeSource{}
		}
		v.ConfigMap.DefaultMode = pointer.Int32Ptr(420)
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

func findContainer(name string, containers []corev1.Container) *corev1.Container {
	for i, c := range containers {
		if c.Name == name {
			return &containers[i]
		}
	}
	return nil
}

func kasVolumeKMSSocket() *corev1.Volume {
	return &corev1.Volume{
		Name: "kms-socket",
	}
}

func buildVolumeKMSSocket(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}

func applyGenericSecretEncryptionConfig(podSpec *corev1.PodSpec) {
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
	container.Args = append(container.Args, fmt.Sprintf("--encryption-provider-config=%s/%s", genericSecretEncryptionConfigFileVolumeMount.Path(kasContainerMain().Name, kasVolumeSecretEncryptionConfigFile().Name), secretEncryptionConfigurationKey))
	podSpec.Volumes = append(podSpec.Volumes, util.BuildVolume(kasVolumeSecretEncryptionConfigFile(), buildVolumeSecretEncryptionConfigFile))
	container.VolumeMounts = append(container.VolumeMounts,
		genericSecretEncryptionConfigFileVolumeMount.ContainerMounts(kasContainerMain().Name)...)
}

func kasVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func buildKASVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KASServiceKubeconfigSecret("").Name,
	}
}
