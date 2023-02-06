package nto

import (
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/util"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilpointer "k8s.io/utils/pointer"
)

const (
	operatorName       = "cluster-node-tuning-operator"
	metricsServiceName = "node-tuning-operator"
)

type Images struct {
	NodeTuningOperator string
	NodeTunedContainer string
}

type Params struct {
	ReleaseVersion          string
	AvailabilityProberImage string
	HostedClusterName       string
	Images                  Images
	DeploymentConfig        config.DeploymentConfig
	OwnerRef                config.OwnerRef
}

func NewParams(hcp *hyperv1.HostedControlPlane, version string, images map[string]string, setDefaultSecurityContext bool) Params {
	p := Params{
		Images: Images{
			NodeTuningOperator: images[operatorName],
			NodeTunedContainer: images[operatorName],
		},
		ReleaseVersion: version,
		OwnerRef:       config.OwnerRefFrom(hcp),
	}

	p.DeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	p.DeploymentConfig.SetDefaults(hcp, nil, utilpointer.IntPtr(1))
	p.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)

	p.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext
	p.HostedClusterName = hcp.Name

	return p
}

func ReconcileClusterNodeTuningOperatorMetricsService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(svc)
	svc.Annotations = map[string]string{
		"service.beta.openshift.io/serving-cert-secret-name": "node-tuning-operator-tls",
	}

	svc.Labels = map[string]string{
		"name":                        metricsServiceName,
		hyperv1.ControlPlaneComponent: operatorName,
	}
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Name:       "metrics",
			Port:       60000,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(60000),
		},
	}
	svc.Spec.Selector = map[string]string{
		"name": operatorName,
	}

	return nil
}

func ReconcileClusterNodeTuningOperatorServiceMonitor(sm *prometheusoperatorv1.ServiceMonitor, clusterID string, metricsSet metrics.MetricsSet, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sm)
	sm.Spec.Selector.MatchLabels = map[string]string{
		"name":                        metricsServiceName,
		hyperv1.ControlPlaneComponent: operatorName,
	}
	sm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{sm.Namespace},
	}
	targetPort := intstr.FromInt(60000)
	sm.Spec.Endpoints = []prometheusoperatorv1.Endpoint{
		{
			Interval:   "15s",
			TargetPort: &targetPort,
			Scheme:     "https",
			Path:       "/metrics",
			TLSConfig: &prometheusoperatorv1.TLSConfig{
				SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
					ServerName: metricsServiceName + "." + sm.Namespace + ".svc",
					Cert: prometheusoperatorv1.SecretOrConfigMap{
						Secret: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "node-tuning-operator-tls",
							},
							Key: "tls.crt",
						},
					},
					KeySecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "node-tuning-operator-tls",
						},
						Key: "tls.key",
					},
					CA: prometheusoperatorv1.SecretOrConfigMap{
						ConfigMap: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "openshift-service-ca.crt",
							},
							Key: "service-ca.crt",
						},
					},
				},
			},
			MetricRelabelConfigs: metrics.NTORelabelConfigs(metricsSet),
		},
	}

	util.ApplyClusterIDLabel(&sm.Spec.Endpoints[0], clusterID)

	return nil
}

func ReconcileRole(role *rbacv1.Role, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(role)
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{corev1.SchemeGroupVersion.Group},
			Resources: []string{
				"configmaps",
			},
			Verbs: []string{"*"},
		},
	}
	return nil
}

func ReconcileRoleBinding(rb *rbacv1.RoleBinding, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(rb)
	rb.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "Role",
		Name:     manifests.ClusterNodeTuningOperatorRole("").Name,
	}
	rb.Subjects = []rbacv1.Subject{
		{
			Kind: "ServiceAccount",
			Name: manifests.ClusterNodeTuningOperatorServiceAccount("").Name,
		},
	}
	return nil
}

func ReconcileServiceAccount(sa *corev1.ServiceAccount, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sa)
	return nil
}

func ReconcileDeployment(dep *appsv1.Deployment, params Params) error {
	params.OwnerRef.ApplyTo(dep)
	dep.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"name": operatorName,
		},
	}
	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = map[string]string{}
	}
	dep.Spec.Template.Annotations["target.workload.openshift.io/management"] = `{"effect": "PreferredDuringScheduling"}`
	if dep.Spec.Template.Labels == nil {
		dep.Spec.Template.Labels = map[string]string{}
	}
	dep.Spec.Template.Labels = map[string]string{
		"name":                        operatorName,
		"app":                         operatorName,
		hyperv1.ControlPlaneComponent: operatorName,
	}

	ntoArgs := []string{
		"-v=0",
	}

	var ntoEnv []corev1.EnvVar

	ntoEnv = append(ntoEnv, []corev1.EnvVar{
		{Name: "RELEASE_VERSION", Value: params.ReleaseVersion},
		{Name: "HYPERSHIFT", Value: "true"},
		{Name: "MY_NAMESPACE", ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{
				FieldPath: "metadata.namespace",
			},
		}},
		{Name: "WATCH_NAMESPACE", Value: "openshift-cluster-node-tuning-operator"},
		{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{
				FieldPath: "metadata.name",
			},
		}},
		{Name: "RESYNC_PERIOD", Value: "600"},
		{Name: "CLUSTER_NODE_TUNED_IMAGE", Value: params.Images.NodeTunedContainer},
		{Name: "KUBECONFIG", Value: "/etc/kubernetes/kubeconfig"},
	}...)

	dep.Spec.Template.Spec.ServiceAccountName = manifests.ClusterNodeTuningOperatorServiceAccount("").Name
	dep.Spec.Template.Spec.Containers = []corev1.Container{{
		Command: []string{"cluster-node-tuning-operator"},
		Args:    ntoArgs,
		Env:     ntoEnv,
		Name:    operatorName,
		Image:   params.Images.NodeTuningOperator,
		Ports: []corev1.ContainerPort{
			{Name: "metrics", ContainerPort: 60000},
		},
		ImagePullPolicy: corev1.PullIfNotPresent,
		Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("50Mi"),
		}},
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		VolumeMounts: []corev1.VolumeMount{
			{Name: "node-tuning-operator-tls", MountPath: "/etc/secrets"},
			{Name: "trusted-ca", MountPath: "/var/run/configmaps/trusted-ca/"},
			{Name: "hosted-kubeconfig", MountPath: "/etc/kubernetes"},
		},
	}}
	dep.Spec.Template.Spec.Volumes = []corev1.Volume{
		{Name: "node-tuning-operator-tls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "node-tuning-operator-tls"}}},
		{Name: "trusted-ca", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
			Optional:             utilpointer.Bool(true),
			LocalObjectReference: corev1.LocalObjectReference{Name: "trusted-ca"},
			Items: []corev1.KeyToPath{
				{Key: "ca-bundle.crt", Path: "tls-ca-bundle.pem"},
			},
		}}},
		{Name: "hosted-kubeconfig", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: manifests.KASServiceKubeconfigSecret("").Name}}},
	}

	params.DeploymentConfig.ApplyTo(dep)
	return nil
}
