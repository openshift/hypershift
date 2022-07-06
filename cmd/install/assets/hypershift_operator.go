package assets

import (
	"fmt"

	"github.com/google/uuid"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/pkg/version"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/util"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"
)

const (
	// HypershiftOperatorPriortyClass is the priority class for the HO
	HypershiftOperatorPriortyClass = "hypershift-operator"

	// EtcdPriorityClass is for etcd pods.
	EtcdPriorityClass = "hypershift-etcd"

	// APICriticalPriorityClass is for pods that are required for API calls and
	// resource admission to succeed. This includes pods like kube-apiserver,
	// aggregated API servers, and webhooks.
	APICriticalPriorityClass = "hypershift-api-critical"

	// DefaultPriorityClass is for pods in the Hypershift control plane that are
	// not API critical but still need elevated priority.
	DefaultPriorityClass = "hypershift-control-plane"
)

type HyperShiftNamespace struct {
	Name                       string
	EnableOCPClusterMonitoring bool
}

func (o HyperShiftNamespace) Build() *corev1.Namespace {
	namespace := &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Namespace",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: o.Name,
		},
	}
	if o.EnableOCPClusterMonitoring {
		namespace.Labels = map[string]string{
			"openshift.io/cluster-monitoring": "true",
		}
	}
	return namespace
}

const (
	awsCredsSecretName            = "hypershift-operator-aws-credentials"
	oidcProviderS3CredsSecretName = "hypershift-operator-oidc-provider-s3-credentials"
	externaDNSCredsSecretName     = "external-dns-credentials"
)

type HyperShiftOperatorCredentialsSecret struct {
	Namespace  *corev1.Namespace
	CredsBytes []byte
	CredsKey   string
}

func (o HyperShiftOperatorCredentialsSecret) Build() *corev1.Secret {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      awsCredsSecretName,
			Namespace: o.Namespace.Name,
		},
		Data: map[string][]byte{
			o.CredsKey: o.CredsBytes,
		},
	}
	return secret
}

type HyperShiftOperatorOIDCProviderS3Secret struct {
	Namespace                      *corev1.Namespace
	OIDCStorageProviderS3CredBytes []byte
	CredsKey                       string
}

func (o HyperShiftOperatorOIDCProviderS3Secret) Build() *corev1.Secret {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      oidcProviderS3CredsSecretName,
			Namespace: o.Namespace.Name,
		},
		Data: map[string][]byte{
			o.CredsKey: o.OIDCStorageProviderS3CredBytes,
		},
	}
	return secret
}

type ExternalDNSCredsSecret struct {
	Namespace  *corev1.Namespace
	CredsBytes []byte
}

func (o ExternalDNSCredsSecret) Build() *corev1.Secret {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      externaDNSCredsSecretName,
			Namespace: o.Namespace.Name,
		},
		Data: map[string][]byte{
			"credentials": o.CredsBytes,
		},
	}
	return secret
}

type ExternalDNSDeployment struct {
	Namespace         *corev1.Namespace
	Image             string
	ServiceAccount    *corev1.ServiceAccount
	Provider          string
	DomainFilter      string
	CredentialsSecret *corev1.Secret
}

func (o ExternalDNSDeployment) Build() *appsv1.Deployment {
	replicas := int32(1)
	deployment := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: appsv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "external-dns",
			Namespace: o.Namespace.Name,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": "external-dns",
				},
			},
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name":                    "external-dns",
						"app":                     "external-dns",
						hyperv1.OperatorComponent: "external-dns",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: o.ServiceAccount.Name,
					Containers: []corev1.Container{
						{
							Name:            "external-dns",
							Image:           o.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{"/external-dns"},
							Args: []string{
								"--source=service",
								"--source=openshift-route",
								fmt.Sprintf("--domain-filter=%s", o.DomainFilter),
								fmt.Sprintf("--provider=%s", o.Provider),
								"--registry=txt",
								"--txt-suffix=-external-dns",
								fmt.Sprintf("--txt-owner-id=%s", uuid.NewString()),
							},
							Ports: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 7979}},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/healthz",
										Port:   intstr.FromInt(7979),
										Scheme: corev1.URISchemeHTTP,
									},
								},
								InitialDelaySeconds: 60,
								PeriodSeconds:       60,
								SuccessThreshold:    1,
								FailureThreshold:    5,
								TimeoutSeconds:      5,
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("20Mi"),
									corev1.ResourceCPU:    resource.MustParse("5m"),
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "credentials",
									MountPath: "/etc/provider",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "credentials",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: o.CredentialsSecret.Name,
								},
							},
						},
					},
				},
			},
		},
	}

	// Add platform specific settings
	switch o.Provider {
	case "aws":
		deployment.Spec.Template.Spec.Containers[0].Env = append(deployment.Spec.Template.Spec.Containers[0].Env,
			corev1.EnvVar{
				Name:  "AWS_SHARED_CREDENTIALS_FILE",
				Value: "/etc/provider/credentials",
			},
			corev1.EnvVar{
				Name: "AWS_REGION",
				// external-dns only makes route53 requests which is a global service,
				// thus we can assume us-east-1 without having to request it on the command line
				Value: "us-east-1",
			})
		deployment.Spec.Template.Spec.Containers[0].Args = append(deployment.Spec.Template.Spec.Containers[0].Args, "--aws-zone-type=public")
	}

	return deployment
}

type HyperShiftOperatorDeployment struct {
	AdditionalTrustBundle          *corev1.ConfigMap
	Namespace                      *corev1.Namespace
	OperatorImage                  string
	Images                         map[string]string
	ServiceAccount                 *corev1.ServiceAccount
	Replicas                       int32
	EnableOCPClusterMonitoring     bool
	EnableCIDebugOutput            bool
	EnableWebhook                  bool
	PrivatePlatform                string
	AWSPrivateSecret               *corev1.Secret
	AWSPrivateSecretKey            string
	AWSPrivateRegion               string
	OIDCBucketName                 string
	OIDCBucketRegion               string
	OIDCStorageProviderS3Secret    *corev1.Secret
	OIDCStorageProviderS3SecretKey string
	MetricsSet                     metrics.MetricsSet
	IncludeVersion                 bool
	UWMTelemetry                   bool
}

func (o HyperShiftOperatorDeployment) Build() *appsv1.Deployment {
	args := []string{
		"run",
		"--namespace=$(MY_NAMESPACE)",
		"--pod-name=$(MY_NAME)",
		"--metrics-addr=:9000",
		fmt.Sprintf("--enable-ocp-cluster-monitoring=%t", o.EnableOCPClusterMonitoring),
		fmt.Sprintf("--enable-ci-debug-output=%t", o.EnableCIDebugOutput),
		fmt.Sprintf("--private-platform=%s", o.PrivatePlatform),
	}

	var volumeMounts []corev1.VolumeMount
	var volumes []corev1.Volume
	envVars := []corev1.EnvVar{
		{
			Name: "MY_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			},
		},
		{
			Name: "MY_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
		metrics.MetricsSetToEnv(o.MetricsSet),
	}

	if o.EnableWebhook {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "serving-cert",
			MountPath: "/var/run/secrets/serving-cert",
		})
		volumes = append(volumes, corev1.Volume{
			Name: "serving-cert",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: "manager-serving-cert",
				},
			},
		})
		args = append(args,
			"--cert-dir=/var/run/secrets/serving-cert",
		)
	}

	if len(o.OIDCBucketName) > 0 && len(o.OIDCBucketRegion) > 0 && len(o.OIDCStorageProviderS3SecretKey) > 0 &&
		o.OIDCStorageProviderS3Secret != nil && len(o.OIDCStorageProviderS3Secret.Name) > 0 {
		args = append(args,
			"--oidc-storage-provider-s3-bucket-name="+o.OIDCBucketName,
			"--oidc-storage-provider-s3-region="+o.OIDCBucketRegion,
			"--oidc-storage-provider-s3-credentials=/etc/oidc-storage-provider-s3-creds/"+o.OIDCStorageProviderS3SecretKey,
		)
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "oidc-storage-provider-s3-creds",
			MountPath: "/etc/oidc-storage-provider-s3-creds",
		})
		volumes = append(volumes, corev1.Volume{
			Name: "oidc-storage-provider-s3-creds",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: o.OIDCStorageProviderS3Secret.Name,
				},
			},
		})
	}

	if o.UWMTelemetry {
		args = append(args, "--enable-uwm-telemetry-remote-write")
	}

	image := o.OperatorImage

	if mapImage, ok := o.Images["hypershift-operator"]; ok {
		image = mapImage
	}
	tagMapping := images.TagMapping()
	for tag, ref := range o.Images {
		if envVar, exists := tagMapping[tag]; exists {
			envVars = append(envVars, corev1.EnvVar{
				Name:  envVar,
				Value: ref,
			})
		}
	}

	privatePlatformType := hyperv1.PlatformType(o.PrivatePlatform)
	if privatePlatformType != hyperv1.NonePlatform {
		// Add generic provider credentials secret volume
		volumes = append(volumes, corev1.Volume{
			Name: "credentials",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: o.AWSPrivateSecret.Name,
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "credentials",
			MountPath: "/etc/provider",
		})

		// Add platform specific settings
		switch privatePlatformType {
		case hyperv1.AWSPlatform:
			envVars = append(envVars,
				corev1.EnvVar{
					Name:  "AWS_SHARED_CREDENTIALS_FILE",
					Value: "/etc/provider/" + o.AWSPrivateSecretKey,
				},
				corev1.EnvVar{
					Name:  "AWS_REGION",
					Value: o.AWSPrivateRegion,
				},
				corev1.EnvVar{
					Name:  "AWS_SDK_LOAD_CONFIG",
					Value: "1",
				})
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      "token",
				MountPath: "/var/run/secrets/openshift/serviceaccount",
			})
			volumes = append(volumes, corev1.Volume{
				Name: "token",
				VolumeSource: corev1.VolumeSource{
					Projected: &corev1.ProjectedVolumeSource{
						Sources: []corev1.VolumeProjection{
							{
								ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
									Audience: "openshift",
									Path:     "token",
								},
							},
						},
					},
				},
			})
		}
	}

	deployment := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: appsv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "operator",
			Namespace: o.Namespace.Name,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &o.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": "operator",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name":                    "operator",
						hyperv1.OperatorComponent: "operator",
						"app":                     "operator",
					},
				},
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						PodAntiAffinity: &corev1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
								{
									LabelSelector: &metav1.LabelSelector{
										MatchExpressions: []metav1.LabelSelectorRequirement{
											{
												Key:      "name",
												Operator: metav1.LabelSelectorOpIn,
												Values:   []string{"operator"},
											},
										},
									},
									TopologyKey: "kubernetes.io/hostname",
								},
							},
						},
					},
					PriorityClassName:  HypershiftOperatorPriortyClass,
					ServiceAccountName: o.ServiceAccount.Name,
					Containers: []corev1.Container{
						{
							Name: "operator",
							// needed since hypershift operator runs with anyuuid scc
							SecurityContext: &corev1.SecurityContext{
								RunAsUser: k8sutilspointer.Int64Ptr(1000),
							},
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env:             envVars,
							Command:         []string{"/usr/bin/hypershift-operator"},
							Args:            args,
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/metrics",
										Port:   intstr.FromInt(9000),
										Scheme: corev1.URISchemeHTTP,
									},
								},
								InitialDelaySeconds: int32(60),
								PeriodSeconds:       int32(60),
								SuccessThreshold:    int32(1),
								FailureThreshold:    int32(5),
								TimeoutSeconds:      int32(5),
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/metrics",
										Port:   intstr.FromInt(9000),
										Scheme: corev1.URISchemeHTTP,
									},
								},
								InitialDelaySeconds: int32(15),
								PeriodSeconds:       int32(60),
								SuccessThreshold:    int32(1),
								FailureThreshold:    int32(3),
								TimeoutSeconds:      int32(5),
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "metrics",
									ContainerPort: 9000,
									Protocol:      corev1.ProtocolTCP,
								},
								{
									Name:          "manager",
									ContainerPort: 9443,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("150Mi"),
									corev1.ResourceCPU:    resource.MustParse("10m"),
								},
							},
							VolumeMounts: volumeMounts,
						},
					},
					Volumes: volumes,
				},
			},
		},
	}

	if o.IncludeVersion {
		deployment.Annotations = map[string]string{
			"hypershift.openshift.io/install-cli-version": version.String(),
		}
	}

	if o.AdditionalTrustBundle != nil {
		// Add trusted-ca mount with optional configmap
		util.DeploymentAddTrustBundleVolume(&corev1.LocalObjectReference{Name: o.AdditionalTrustBundle.Name}, deployment)
	}

	return deployment
}

type HyperShiftOperatorService struct {
	Namespace *corev1.Namespace
}

func (o HyperShiftOperatorService) Build() *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "operator",
			Labels: map[string]string{
				"name": "operator",
			},
			Annotations: map[string]string{
				"service.beta.openshift.io/serving-cert-secret-name": "manager-serving-cert",
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				"name": "operator",
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "metrics",
					Protocol:   corev1.ProtocolTCP,
					Port:       9393,
					TargetPort: intstr.FromString("metrics"),
				},
				{
					Name:       "manager",
					Protocol:   corev1.ProtocolTCP,
					Port:       443,
					TargetPort: intstr.FromString("manager"),
				},
			},
		},
	}
}

type ExternalDNSServiceAccount struct {
	Namespace *corev1.Namespace
}

func (o ExternalDNSServiceAccount) Build() *corev1.ServiceAccount {
	sa := &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "external-dns",
		},
	}
	return sa
}

type ExternalDNSClusterRole struct{}

func (o ExternalDNSClusterRole) Build() *rbacv1.ClusterRole {
	role := &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRole",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "external-dns",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"route.openshift.io"},
				Resources: []string{"*"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{
					"endpoints",
					"services",
					"nodes",
					"pods",
				},
				Verbs: []string{"get", "list", "watch"},
			},
		},
	}
	return role
}

type ExternalDNSClusterRoleBinding struct {
	ClusterRole    *rbacv1.ClusterRole
	ServiceAccount *corev1.ServiceAccount
}

func (o ExternalDNSClusterRoleBinding) Build() *rbacv1.ClusterRoleBinding {
	binding := &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "external-dns",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     o.ClusterRole.Name,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      o.ServiceAccount.Name,
				Namespace: o.ServiceAccount.Namespace,
			},
		},
	}
	return binding
}

type HyperShiftOperatorServiceAccount struct {
	Namespace *corev1.Namespace
}

func (o HyperShiftOperatorServiceAccount) Build() *corev1.ServiceAccount {
	sa := &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "operator",
		},
	}
	return sa
}

type HyperShiftOperatorClusterRole struct{}

func (o HyperShiftOperatorClusterRole) Build() *rbacv1.ClusterRole {
	role := &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRole",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-operator",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"hypershift.openshift.io"},
				Resources: []string{"*"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"config.openshift.io"},
				Resources: []string{"*"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"apiextensions.k8s.io"},
				Resources: []string{"customresourcedefinitions"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"batch"},
				Resources: []string{"cronjobs", "jobs"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"coordination.k8s.io"},
				Resources: []string{
					"leases",
				},
				Verbs: []string{"*"},
			},
			{
				APIGroups: []string{"networking.k8s.io"},
				Resources: []string{"networkpolicies"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{
					"bootstrap.cluster.x-k8s.io",
					"controlplane.cluster.x-k8s.io",
					"infrastructure.cluster.x-k8s.io",
					"machines.cluster.x-k8s.io",
					"exp.infrastructure.cluster.x-k8s.io",
					"addons.cluster.x-k8s.io",
					"exp.cluster.x-k8s.io",
					"cluster.x-k8s.io",
					"monitoring.coreos.com",
				},
				Resources: []string{"*"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"policy"},
				Resources: []string{"poddisruptionbudgets"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"operator.openshift.io"},
				Resources: []string{"*"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"route.openshift.io"},
				Resources: []string{"*"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"security.openshift.io"},
				Resources: []string{"securitycontextconstraints"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"rbac.authorization.k8s.io"},
				Resources: []string{"*"},
				Verbs: []string{
					"get",
					"list",
					"watch",
					"create",
					"update",
					"patch",
					"delete",
				},
			},
			{
				APIGroups: []string{""},
				Resources: []string{
					"events",
					"configmaps",
					"persistentvolumeclaims",
					"pods",
					"pods/log",
					"secrets",
					"nodes",
					"namespaces",
					"serviceaccounts",
					"services",
					"endpoints",
				},
				Verbs: []string{"*"},
			},
			{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments", "statefulsets"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"etcd.database.coreos.com"},
				Resources: []string{"*"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"machine.openshift.io"},
				Resources: []string{"*"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"monitoring.coreos.com"},
				Resources: []string{"podmonitors"},
				Verbs:     []string{"get", "list", "watch", "create", "update"},
			},
			{
				APIGroups: []string{"capi-provider.agent-install.openshift.io"},
				Resources: []string{"*"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"operator.openshift.io"},
				Resources: []string{"ingresscontrollers"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"kubevirt.io"},
				Resources: []string{"virtualmachineinstances", "virtualmachines"},
				Verbs:     []string{"*"},
			},
			{ // This allows hypershift operator to grant RBAC permissions for agents, clusterDeployments and agentClusterInstalls to the capi-provider-agent
				APIGroups: []string{"agent-install.openshift.io"},
				Resources: []string{"agents"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"extensions.hive.openshift.io"},
				Resources: []string{"agentclusterinstalls"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"hive.openshift.io"},
				Resources: []string{"clusterdeployments"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"discovery.k8s.io"},
				Resources: []string{"endpointslices"},
				Verbs:     []string{"list", "watch"},
			},
		},
	}
	return role
}

type HyperShiftOperatorClusterRoleBinding struct {
	ClusterRole    *rbacv1.ClusterRole
	ServiceAccount *corev1.ServiceAccount
}

func (o HyperShiftOperatorClusterRoleBinding) Build() *rbacv1.ClusterRoleBinding {
	binding := &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-operator",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     o.ClusterRole.Name,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      o.ServiceAccount.Name,
				Namespace: o.ServiceAccount.Namespace,
			},
		},
	}
	return binding
}

type HyperShiftOperatorRole struct {
	Namespace *corev1.Namespace
}

func (o HyperShiftOperatorRole) Build() *rbacv1.Role {
	role := &rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Role",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "hypershift-operator",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"coordination.k8s.io"},
				Resources: []string{
					"leases",
				},
				Verbs: []string{"*"},
			},
		},
	}
	return role
}

type HyperShiftOperatorRoleBinding struct {
	Role           *rbacv1.Role
	ServiceAccount *corev1.ServiceAccount
}

func (o HyperShiftOperatorRoleBinding) Build() *rbacv1.RoleBinding {
	binding := &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.ServiceAccount.Namespace,
			Name:      "hypershift-operator",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     o.Role.Name,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      o.ServiceAccount.Name,
				Namespace: o.ServiceAccount.Namespace,
			},
		},
	}
	return binding
}

func HyperShiftControlPlanePriorityClass() *schedulingv1.PriorityClass {
	return &schedulingv1.PriorityClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PriorityClass",
			APIVersion: schedulingv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: DefaultPriorityClass,
		},
		Value:         100000000,
		GlobalDefault: false,
		Description:   "This priority class should be used for hypershift control plane pods not critical to serving the API.",
	}
}

func HyperShiftAPICriticalPriorityClass() *schedulingv1.PriorityClass {
	return &schedulingv1.PriorityClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PriorityClass",
			APIVersion: schedulingv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: APICriticalPriorityClass,
		},
		Value:         100001000,
		GlobalDefault: false,
		Description:   "This priority class should be used for hypershift control plane pods critical to serving the API.",
	}
}

func HyperShiftEtcdPriorityClass() *schedulingv1.PriorityClass {
	return &schedulingv1.PriorityClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PriorityClass",
			APIVersion: schedulingv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: EtcdPriorityClass,
		},
		Value:         100002000,
		GlobalDefault: false,
		Description:   "This priority class should be used for hypershift etcd pods.",
	}
}

func HypershiftOperatorPriorityClass() *schedulingv1.PriorityClass {
	return &schedulingv1.PriorityClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: HypershiftOperatorPriortyClass,
		},
		Value:         100003000,
		GlobalDefault: false,
		Description:   "This priority class is used for hypershift operator pods",
	}
}

type HyperShiftPrometheusRole struct {
	Namespace *corev1.Namespace
}

func (o HyperShiftPrometheusRole) Build() *rbacv1.Role {
	role := &rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Role",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "prometheus",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{
					"services",
					"endpoints",
					"pods",
				},
				Verbs: []string{"get", "list", "watch"},
			},
		},
	}
	return role
}

type HyperShiftOperatorPrometheusRoleBinding struct {
	Namespace                  *corev1.Namespace
	Role                       *rbacv1.Role
	EnableOCPClusterMonitoring bool
}

func (o HyperShiftOperatorPrometheusRoleBinding) Build() *rbacv1.RoleBinding {
	subject := rbacv1.Subject{
		Kind:      "ServiceAccount",
		Name:      "prometheus-user-workload",
		Namespace: "openshift-user-workload-monitoring",
	}
	if o.EnableOCPClusterMonitoring {
		subject.Name = "prometheus-k8s"
		subject.Namespace = "openshift-monitoring"
	}
	binding := &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "prometheus",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     o.Role.Name,
		},
		Subjects: []rbacv1.Subject{subject},
	}
	return binding
}

type HyperShiftServiceMonitor struct {
	Namespace *corev1.Namespace
}

func (o HyperShiftServiceMonitor) Build() *prometheusoperatorv1.ServiceMonitor {
	return &prometheusoperatorv1.ServiceMonitor{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceMonitor",
			APIVersion: prometheusoperatorv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "operator",
		},
		Spec: prometheusoperatorv1.ServiceMonitorSpec{
			JobLabel: "component",
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": "operator",
				},
			},
			Endpoints: []prometheusoperatorv1.Endpoint{
				{
					Interval: "30s",
					Port:     "metrics",
				},
			},
		},
	}
}

type HypershiftRecordingRule struct {
	Namespace *corev1.Namespace
}

func (r HypershiftRecordingRule) Build() *prometheusoperatorv1.PrometheusRule {
	rule := &prometheusoperatorv1.PrometheusRule{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PrometheusRule",
			APIVersion: prometheusoperatorv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.Namespace.Name,
			Name:      "metrics",
		},
	}

	rule.Spec = recordingRuleSpec()
	return rule
}

type HyperShiftClientClusterRole struct{}

func (o HyperShiftClientClusterRole) Build() *rbacv1.ClusterRole {
	role := &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRole",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-client",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"hypershift.openshift.io"},
				Resources: []string{"hostedclusters", "nodepools"},
				Verbs:     []string{"*"},
			},
		},
	}
	return role
}

type HyperShiftClientServiceAccount struct {
	Namespace *corev1.Namespace
}

func (o HyperShiftClientServiceAccount) Build() *corev1.ServiceAccount {
	sa := &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "hypershift-client",
		},
	}
	return sa
}

type HyperShiftClientClusterRoleBinding struct {
	ClusterRole    *rbacv1.ClusterRole
	ServiceAccount *corev1.ServiceAccount
	GroupName      string
}

func (o HyperShiftClientClusterRoleBinding) Build() *rbacv1.ClusterRoleBinding {
	binding := &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-client",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     o.ClusterRole.Name,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      o.ServiceAccount.Name,
				Namespace: o.ServiceAccount.Namespace,
			},
			{
				Kind:     "Group",
				APIGroup: "rbac.authorization.k8s.io",
				Name:     o.GroupName,
			},
		},
	}
	return binding
}

type HyperShiftReaderClusterRole struct{}

func (o HyperShiftReaderClusterRole) Build() *rbacv1.ClusterRole {
	role := &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRole",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-readers",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"hypershift.openshift.io"},
				Resources: []string{"*"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"config.openshift.io"},
				Resources: []string{"*"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"apiextensions.k8s.io"},
				Resources: []string{"customresourcedefinitions"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"networking.k8s.io"},
				Resources: []string{"networkpolicies"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{
					"bootstrap.cluster.x-k8s.io",
					"controlplane.cluster.x-k8s.io",
					"infrastructure.cluster.x-k8s.io",
					"machines.cluster.x-k8s.io",
					"exp.infrastructure.cluster.x-k8s.io",
					"addons.cluster.x-k8s.io",
					"exp.cluster.x-k8s.io",
					"cluster.x-k8s.io",
				},
				Resources: []string{"*"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"operator.openshift.io"},
				Resources: []string{"*"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"route.openshift.io"},
				Resources: []string{"*"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"security.openshift.io"},
				Resources: []string{"securitycontextconstraints"},
				Verbs:     []string{"get", "list", "watch", "use"},
			},
			{
				APIGroups: []string{"rbac.authorization.k8s.io"},
				Resources: []string{"*"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{
					"events",
					"configmaps",
					"pods",
					"pods/log",
					"nodes",
					"namespaces",
					"serviceaccounts",
					"services",
				},
				Verbs: []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"etcd.database.coreos.com"},
				Resources: []string{"*"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"machine.openshift.io"},
				Resources: []string{"*"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"monitoring.coreos.com"},
				Resources: []string{"podmonitors"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"capi-provider.agent-install.openshift.io"},
				Resources: []string{"*"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}
	return role
}

type HyperShiftReaderClusterRoleBinding struct {
	ClusterRole *rbacv1.ClusterRole
	GroupName   string
}

func (o HyperShiftReaderClusterRoleBinding) Build() *rbacv1.ClusterRoleBinding {
	binding := &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-readers",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     o.ClusterRole.Name,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:     "Group",
				APIGroup: "rbac.authorization.k8s.io",
				Name:     o.GroupName,
			},
		},
	}
	return binding
}

type HyperShiftValidatingWebhookConfiguration struct {
	Namespace *corev1.Namespace
}

func (o HyperShiftValidatingWebhookConfiguration) Build() *admissionregistrationv1.ValidatingWebhookConfiguration {
	scope := admissionregistrationv1.NamespacedScope
	path := "/validate-hypershift-openshift-io-v1alpha1-hostedcluster"
	sideEffects := admissionregistrationv1.SideEffectClassNone
	timeout := int32(10)
	validatingWebhookConfiguration := &admissionregistrationv1.ValidatingWebhookConfiguration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ValidatingWebhookConfiguration",
			APIVersion: admissionregistrationv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      hyperv1.GroupVersion.Group,
			Annotations: map[string]string{
				"service.beta.openshift.io/inject-cabundle": "true",
			},
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "hostedclusters.hypershift.openshift.io",
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{
							// NOTE: uncomment if we want to do create time validation
							//admissionregistrationv1.Create,
							admissionregistrationv1.Update,
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"hypershift.openshift.io"},
							APIVersions: []string{"v1alpha1"},
							Resources:   []string{"hostedclusters"},
							Scope:       &scope,
						},
					},
				},
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					Service: &admissionregistrationv1.ServiceReference{
						Namespace: "hypershift",
						Name:      "operator",
						Path:      &path,
					},
				},
				SideEffects:             &sideEffects,
				AdmissionReviewVersions: []string{"v1"},
				TimeoutSeconds:          &timeout,
			},
		},
	}
	return validatingWebhookConfiguration
}
