package powervs

import (
	"bytes"
	"fmt"
	"text/template"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilpointer "k8s.io/utils/pointer"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	supportconfig "github.com/openshift/hypershift/support/config"
)

const (
	ccmContainerName       = "cloud-controller-manager"
	kubeConfigFileBasePath = "/etc/kubernetes"
	secretMountPath        = "/etc/vpc"
	ccmConfigMapMountPath  = "/etc/ibm"
	replicas               = 1
)

const ccmConfigTemplateData = `
[global]
version = 1.1.0
[kubernetes]
config-file = {{.ConfigFile}}
[provider]
cluster-default-provider = g2
accountID = {{.AccountID}}
clusterID = {{.ClusterID}}
g2workerServiceAccountID = {{.G2workerServiceAccountID}}
g2Credentials = {{.G2Credentials}}
g2ResourceGroupName = {{.G2ResourceGroupName}}
g2VpcSubnetNames = {{.G2VpcSubnetNames}}
g2VpcName = {{.G2VpcName}}
region = {{.Region}}
powerVSCloudInstanceID = {{.PowerVSCloudInstanceID}}
powerVSRegion = {{.PowerVSRegion}}
powerVSZone = {{.PowerVSZone}}`

var ccmConfigTemplate = template.Must(template.New("ccmConfigMap").Parse(ccmConfigTemplateData))

func ReconcileCCMConfigMap(ccmConfig *corev1.ConfigMap, hcp *hyperv1.HostedControlPlane) error {
	config := map[string]string{
		"ConfigFile":               fmt.Sprintf("%s/kubeconfig", kubeConfigFileBasePath),
		"AccountID":                hcp.Spec.Platform.PowerVS.AccountID,
		"ClusterID":                hcp.Name,
		"G2workerServiceAccountID": hcp.Spec.Platform.PowerVS.AccountID,
		"G2Credentials":            fmt.Sprintf("%s/%s", secretMountPath, "ibmcloud_api_key"),
		"G2ResourceGroupName":      hcp.Spec.Platform.PowerVS.ResourceGroup,
		"G2VpcSubnetNames":         hcp.Spec.Platform.PowerVS.VPC.Subnet,
		"G2VpcName":                hcp.Spec.Platform.PowerVS.VPC.Name,
		"Region":                   hcp.Spec.Platform.PowerVS.VPC.Region,
		"PowerVSCloudInstanceID":   hcp.Spec.Platform.PowerVS.ServiceInstanceID,
		"PowerVSRegion":            hcp.Spec.Platform.PowerVS.Region,
		"PowerVSZone":              hcp.Spec.Platform.PowerVS.Zone,
	}

	configData := &bytes.Buffer{}
	err := ccmConfigTemplate.Execute(configData, config)
	if err != nil {
		return fmt.Errorf("error while parsing ccm config map template %v", err)
	}

	if ccmConfig.Data == nil {
		ccmConfig.Data = map[string]string{}
	}

	ccmConfig.Data[ccmConfig.Name] = configData.String()

	return nil
}

func ReconcileCCMDeployment(deployment *appsv1.Deployment, hcp *hyperv1.HostedControlPlane, ccmConfig *corev1.ConfigMap, releaseImageProvider imageprovider.ReleaseImageProvider, setDefaultSecurityContext bool) error {
	commandToExec := []string{
		"/bin/ibm-cloud-controller-manager",
		"--authentication-skip-lookup",
		"--bind-address=$(POD_IP_ADDRESS)",
		"--use-service-account-credentials=true",
		"--configure-cloud-routes=false",
		"--cloud-provider=ibm",
		fmt.Sprintf("--cloud-config=%s/%s", ccmConfigMapMountPath, ccmConfig.Name),
		"--profiling=false",
		"--leader-elect=true",
		"--leader-elect-lease-duration=137s",
		"--leader-elect-renew-deadline=107s",
		"--leader-elect-retry-period=26s",
		"--tls-cipher-suites=TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,TLS_AES_128_GCM_SHA256,TLS_CHACHA20_POLY1305_SHA256,TLS_AES_256_GCM_SHA384",
		fmt.Sprintf("--kubeconfig=%s/kubeconfig", kubeConfigFileBasePath),
		"--use-service-account-credentials=false",
	}

	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: utilpointer.Int32(int32(replicas)),
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"k8s-app": deployment.Name},
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{"k8s-app": deployment.Name},
			},
			Spec: corev1.PodSpec{
				TerminationGracePeriodSeconds: utilpointer.Int64(90),
				Containers: []corev1.Container{
					{
						Name:            ccmContainerName,
						Image:           releaseImageProvider.GetImage("powervs-cloud-controller-manager"),
						ImagePullPolicy: corev1.PullIfNotPresent,
						Env: []corev1.EnvVar{
							{
								Name: "POD_IP_ADDRESS",
								ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										FieldPath: "status.podIP",
									},
								},
							},
							{
								Name:  "VPCCTL_CLOUD_CONFIG",
								Value: fmt.Sprintf("%s/%s", ccmConfigMapMountPath, ccmConfig.Name),
							},
							{
								Name:  "ENABLE_VPC_PUBLIC_ENDPOINT",
								Value: "true",
							},
						},
						Command: commandToExec,
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path:   "/healthz",
									Port:   intstr.IntOrString{IntVal: 10258},
									Scheme: "HTTPS",
								},
							},
							InitialDelaySeconds: 300,
							TimeoutSeconds:      5,
						},
						Ports: []corev1.ContainerPort{
							{
								Name:          "https",
								Protocol:      "TCP",
								ContainerPort: 10258,
							},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								"cpu":    resource.MustParse("75m"),
								"memory": resource.MustParse("60Mi"),
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      manifests.KASServiceKubeconfigSecret("").Name,
								MountPath: kubeConfigFileBasePath,
							},
							{
								Name:      ccmConfig.Name,
								MountPath: ccmConfigMapMountPath,
							},
							{
								Name:      hcp.Spec.Platform.PowerVS.KubeCloudControllerCreds.Name,
								MountPath: secretMountPath,
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: manifests.KASServiceKubeconfigSecret("").Name,
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName:  manifests.KASServiceKubeconfigSecret("").Name,
								DefaultMode: utilpointer.Int32(400),
							},
						},
					},
					{
						Name: ccmConfig.Name,
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								DefaultMode:          utilpointer.Int32(420),
								LocalObjectReference: corev1.LocalObjectReference{Name: ccmConfig.Name},
							},
						},
					},
					{
						Name: hcp.Spec.Platform.PowerVS.KubeCloudControllerCreds.Name,
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName:  hcp.Spec.Platform.PowerVS.KubeCloudControllerCreds.Name,
								DefaultMode: utilpointer.Int32(400),
							},
						},
					},
				},
			},
		},
	}

	deploymentConfig := supportconfig.DeploymentConfig{
		Scheduling: supportconfig.Scheduling{
			PriorityClass: supportconfig.DefaultPriorityClass,
		},
		SetDefaultSecurityContext: setDefaultSecurityContext,
	}

	deploymentConfig.SetDefaults(hcp, nil, utilpointer.Int(replicas))
	deploymentConfig.ApplyTo(deployment)

	return nil
}
