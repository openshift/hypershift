package registryoperator

import (
	"bytes"
	"path"
	"text/template"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const (
	operatorName         = "cluster-image-registry-operator"
	workerNamespace      = "openshift-image-registry"
	workerServiceAccount = "cluster-image-registry-operator"
	metricsHostname      = "cluster-image-registry-operator"
	tokenFile            = "token"
	metricsPort          = 60000

	startScriptTemplateStr = `#!/bin/bash
set -euo pipefail

while true; do
   if [[ -f {{ .TokenDir }}/token ]]; then
      break
   fi
   echo "Waiting for client token"
   sleep 2
done

echo "{{ .WorkerNamespace }}" > "{{ .TokenDir }}/namespace"
cp "{{ .CABundle }}" "{{ .TokenDir }}/ca.crt"
export KUBERNETES_SERVICE_HOST=kube-apiserver
export KUBERNETES_SERVICE_PORT=$KUBE_APISERVER_SERVICE_PORT

while true; do
  if curl --fail --cacert {{ .TokenDir }}/ca.crt -H "Authorization: Bearer $(cat {{ .TokenDir }}/token)" "https://${KUBERNETES_SERVICE_HOST}:${KUBERNETES_SERVICE_PORT}/apis/config.openshift.io/v1/featuregates" &> /dev/null; then
    break
  fi
  echo "Waiting for access to featuregates resource"
  sleep 2
done

exec /usr/bin/cluster-image-registry-operator \
  --files="{{ .ServingCertDir }}/tls.crt" \
  --files="{{ .ServingCertDir }}/tls.key"
`
)

var startScript string

func init() {
	var err error
	startScriptTemplate := template.Must(template.New("script").Parse(startScriptTemplateStr))
	startScript, err = operatorStartScript(startScriptTemplate)
	if err != nil {
		panic(err.Error())
	}
}

func selectorLabels() map[string]string {
	return map[string]string{
		"name": operatorName,
	}
}

var (
	volumeMounts = util.PodVolumeMounts{
		containerMain().Name: {
			volumeClientToken().Name: "/var/run/secrets/kubernetes.io/serviceaccount",
			volumeServingCert().Name: "/etc/secrets",
			volumeCABundle().Name:    "/etc/certificate/ca",
		},
		containerClientTokenMinter().Name: {
			volumeClientToken().Name:     "/var/client-token",
			volumeAdminKubeconfig().Name: "/etc/kubernetes",
		},
		containerWebIdentityTokenMinter().Name: {
			volumeWebIdentityToken().Name: "/var/run/secrets/openshift/serviceaccount",
			volumeAdminKubeconfig().Name:  "/etc/kubernetes",
		},
	}
)

type Params struct {
	operatorImage            string
	tokenMinterImage         string
	platform                 hyperv1.PlatformType
	issuerURL                string
	releaseVersion           string
	registryImage            string
	prunerImage              string
	deploymentConfig         config.DeploymentConfig
	AzureCredentialsFilepath string
}

func NewParams(hcp *hyperv1.HostedControlPlane, version string, releaseImageProvider imageprovider.ReleaseImageProvider, userReleaseImageProvider imageprovider.ReleaseImageProvider, setDefaultSecurityContext bool) Params {
	params := Params{
		operatorImage:    releaseImageProvider.GetImage("cluster-image-registry-operator"),
		tokenMinterImage: releaseImageProvider.GetImage("token-minter"),
		platform:         hcp.Spec.Platform.Type,
		issuerURL:        hcp.Spec.IssuerURL,
		releaseVersion:   version,
		registryImage:    userReleaseImageProvider.GetImage("docker-registry"),
		prunerImage:      userReleaseImageProvider.GetImage("cli"),
		deploymentConfig: config.DeploymentConfig{
			Scheduling: config.Scheduling{
				PriorityClass: config.DefaultPriorityClass,
			},
			SetDefaultSecurityContext: setDefaultSecurityContext,
			Resources: config.ResourcesSpec{
				containerMain().Name: {
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("50Mi"),
						corev1.ResourceCPU:    resource.MustParse("10m"),
					},
				},
				containerClientTokenMinter().Name: {
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("10m"),
						corev1.ResourceMemory: resource.MustParse("30Mi"),
					},
				},
				containerWebIdentityTokenMinter().Name: {
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("10m"),
						corev1.ResourceMemory: resource.MustParse("30Mi"),
					},
				},
			},
		},
	}

	if azureutil.IsAroHCP() {
		params.AzureCredentialsFilepath = hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.ImageRegistry.CredentialsSecretName
	}

	params.deploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		params.deploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}
	params.deploymentConfig.SetDefaults(hcp, selectorLabels(), ptr.To(1))
	params.deploymentConfig.SetReleaseImageAnnotation(util.HCPControlPlaneReleaseImage(hcp))
	return params
}

func ReconcileDeployment(deployment *appsv1.Deployment, params Params) error {
	podLabels := selectorLabels()
	// Set the app label for the pod but do not add to MatchLabels since it is immutable
	podLabels["app"] = "cluster-image-registry-operator"
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: selectorLabels(),
		},
		Strategy: appsv1.DeploymentStrategy{
			Type: appsv1.RecreateDeploymentStrategyType,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: podLabels,
			},
			Spec: corev1.PodSpec{
				AutomountServiceAccountToken: ptr.To(false),
				Containers: []corev1.Container{
					util.BuildContainer(containerMain(), buildMainContainer(params.operatorImage, params.registryImage, params.prunerImage, params.releaseVersion)),
					util.BuildContainer(containerClientTokenMinter(), buildClientTokenMinter(params.tokenMinterImage, params.issuerURL)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(volumeClientToken(), buildVolumeClientToken),
					util.BuildVolume(volumeServingCert(), buildVolumeServingCert),
					util.BuildVolume(volumeAdminKubeconfig(), buildVolumeAdminKubeconfig),
					util.BuildVolume(volumeCABundle(), buildVolumeCABundle),
				},
			},
		},
	}

	switch params.platform {
	case hyperv1.AWSPlatform:
		deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers,
			util.BuildContainer(containerWebIdentityTokenMinter(), buildWebIdentityTokenMinter(params.tokenMinterImage)))
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
			util.BuildVolume(volumeWebIdentityToken(), buildVolumeWebIdentityToken))
		deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(deployment.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{
				Name:      volumeWebIdentityToken().Name,
				MountPath: "/var/run/secrets/openshift/serviceaccount",
			},
		)
	}
	// For managed Azure deployments, we pass an environment variable, MANAGED_AZURE_HCP_CREDENTIALS_FILE_PATH, so
	// we authenticate with Azure API through UserAssignedCredential authentication. We also mount the
	// SecretProviderClass for the Secrets Store CSI driver to use; it will grab the JSON object stored in the
	// MANAGED_AZURE_HCP_CREDENTIALS_FILE_PATH and mount it as a volume in the image registry pod in the path.
	if azureutil.IsAroHCP() {
		deployment.Spec.Template.Spec.Containers[0].Env = append(deployment.Spec.Template.Spec.Containers[0].Env,
			azureutil.CreateEnvVarsForAzureManagedIdentity(params.AzureCredentialsFilepath)...)

		if deployment.Spec.Template.Spec.Containers[0].VolumeMounts == nil {
			deployment.Spec.Template.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{}
		}
		deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(deployment.Spec.Template.Spec.Containers[0].VolumeMounts,
			azureutil.CreateVolumeMountForAzureSecretStoreProviderClass(config.ManagedAzureImageRegistrySecretStoreVolumeName),
		)

		if deployment.Spec.Template.Spec.Volumes == nil {
			deployment.Spec.Template.Spec.Volumes = []corev1.Volume{}
		}
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
			azureutil.CreateVolumeForAzureSecretStoreProviderClass(config.ManagedAzureImageRegistrySecretStoreVolumeName, config.ManagedAzureImageRegistrySecretStoreProviderClassName),
		)
	}

	params.deploymentConfig.ApplyTo(deployment)
	return nil
}

func containerMain() *corev1.Container {
	return &corev1.Container{
		Name: "cluster-image-registry-operator",
	}
}

func operatorStartScript(startScriptTemplate *template.Template) (string, error) {
	out := &bytes.Buffer{}
	params := struct {
		WorkerNamespace string
		TokenDir        string
		CABundle        string
		ServingCertDir  string
	}{
		WorkerNamespace: workerNamespace,
		TokenDir:        volumeMounts.Path(containerMain().Name, volumeClientToken().Name),
		CABundle:        path.Join(volumeMounts.Path(containerMain().Name, volumeCABundle().Name), certs.CASignerCertMapKey),
		ServingCertDir:  volumeMounts.Path(containerMain().Name, volumeServingCert().Name),
	}
	if err := startScriptTemplate.Execute(out, params); err != nil {
		return "", err
	}
	return out.String(), nil
}

func buildMainContainer(image, registryImage, prunerImage, releaseVersion string) func(*corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
		c.Command = []string{
			"/bin/bash",
		}
		c.Args = []string{
			"-c",
			startScript,
		}
		c.Env = []corev1.EnvVar{
			{
				Name:  "RELEASE_VERSION",
				Value: releaseVersion,
			},
			{
				Name:  "WATCH_NAMESPACE",
				Value: workerNamespace,
			},
			{
				Name:  "OPERATOR_NAME",
				Value: operatorName,
			},
			{
				Name:  "OPERATOR_IMAGE",
				Value: image,
			},
			{
				Name:  "IMAGE",
				Value: registryImage,
			},
			{
				Name:  "IMAGE_PRUNER",
				Value: prunerImage,
			},
			{
				Name:  "AZURE_ENVIRONMENT_FILEPATH",
				Value: "/tmp/azurestackcloud.json",
			},
			{
				Name:  "OPERATOR_IMAGE_VERSION",
				Value: releaseVersion,
			},
		}
		proxy.SetEnvVars(&c.Env)
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
		c.Ports = []corev1.ContainerPort{
			{
				ContainerPort: metricsPort,
				Name:          "metrics",
			},
		}
	}
}

func containerClientTokenMinter() *corev1.Container {
	return &corev1.Container{
		Name: "client-token-minter",
	}
}

func buildClientTokenMinter(image, issuerURL string) func(*corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
		c.Command = []string{
			"/usr/bin/control-plane-operator",
			"token-minter",
		}
		c.Args = []string{
			"--service-account-namespace",
			workerNamespace,
			"--service-account-name",
			workerServiceAccount,
			"--token-file",
			path.Join(volumeMounts.Path(c.Name, volumeClientToken().Name), tokenFile),
			"--token-audience",
			issuerURL,
			"--kubeconfig",
			path.Join(volumeMounts.Path(c.Name, volumeAdminKubeconfig().Name), kas.KubeconfigKey),
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func containerWebIdentityTokenMinter() *corev1.Container {
	return &corev1.Container{
		Name: "token-minter",
	}
}

func buildWebIdentityTokenMinter(image string) func(*corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
		c.Command = []string{
			"/usr/bin/control-plane-operator",
			"token-minter",
		}
		c.Args = []string{
			"--service-account-namespace",
			workerNamespace,
			"--service-account-name",
			workerServiceAccount,
			"--token-file",
			path.Join(volumeMounts.Path(c.Name, volumeWebIdentityToken().Name), tokenFile),
			"--kubeconfig",
			path.Join(volumeMounts.Path(c.Name, volumeAdminKubeconfig().Name), kas.KubeconfigKey),
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func volumeClientToken() *corev1.Volume {
	return &corev1.Volume{
		Name: "client-token",
	}
}

func buildVolumeClientToken(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}

func volumeServingCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "serving-cert",
	}
}

func buildVolumeServingCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName:  manifests.ImageRegistryOperatorServingCert("").Name,
		DefaultMode: ptr.To[int32](0640),
	}
}

func volumeAdminKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func buildVolumeAdminKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName:  manifests.KASServiceKubeconfigSecret("").Name,
		DefaultMode: ptr.To[int32](0640),
	}
}

func volumeCABundle() *corev1.Volume {
	return &corev1.Volume{
		Name: "ca-bundle",
	}
}

func buildVolumeCABundle(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName:  manifests.RootCASecret("").Name,
		DefaultMode: ptr.To[int32](0640),
	}
}

func volumeWebIdentityToken() *corev1.Volume {
	return &corev1.Volume{
		Name: "web-identity-token",
	}
}

func buildVolumeWebIdentityToken(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}

func ReconcilePodMonitor(pm *prometheusoperatorv1.PodMonitor, clusterID string, metricsSet metrics.MetricsSet) {
	pm.Spec.Selector.MatchLabels = selectorLabels()
	pm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{pm.Namespace},
	}
	pm.Spec.PodMetricsEndpoints = []prometheusoperatorv1.PodMetricsEndpoint{
		{
			Interval: "60s",
			Port:     "metrics",
			Path:     "/metrics",
			Scheme:   "https",
			TLSConfig: &prometheusoperatorv1.SafeTLSConfig{
				ServerName: ptr.To(metricsHostname),
				CA: prometheusoperatorv1.SecretOrConfigMap{
					ConfigMap: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: manifests.RootCAConfigMap(pm.Namespace).Name,
						},
						Key: certs.CASignerCertMapKey,
					},
				},
			},
			MetricRelabelConfigs: metrics.RegistryOperatorRelabelConfigs(metricsSet),
		},
	}

	util.ApplyClusterIDLabelToPodMonitor(&pm.Spec.PodMetricsEndpoints[0], clusterID)
}
