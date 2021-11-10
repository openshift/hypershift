package assets

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"
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

type HyperShiftOperatorAWSCredentialsSecret struct {
	Namespace     *corev1.Namespace
	AWSCredsBytes []byte
}

func (o HyperShiftOperatorAWSCredentialsSecret) Build() *corev1.Secret {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-operator-creds",
			Namespace: o.Namespace.Name,
		},
		Data: map[string][]byte{
			"credentials": o.AWSCredsBytes,
		},
	}
	return secret
}

type HyperShiftOperatorDeployment struct {
	Namespace                  *corev1.Namespace
	OperatorImage              string
	ServiceAccount             *corev1.ServiceAccount
	Replicas                   int32
	EnableOCPClusterMonitoring bool
	EnableCIDebugOutput        bool
	AWSRegion                  string
}

func (o HyperShiftOperatorDeployment) Build() *appsv1.Deployment {
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
					ServiceAccountName: o.ServiceAccount.Name,
					Containers: []corev1.Container{
						{
							Name: "operator",
							// needed since hypershift operator runs with anyuuid scc
							SecurityContext: &corev1.SecurityContext{
								RunAsUser: k8sutilspointer.Int64Ptr(1000),
							},
							Image:           o.OperatorImage,
							ImagePullPolicy: corev1.PullAlways,
							Env: []corev1.EnvVar{
								{
									Name: "MY_NAMESPACE",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.namespace",
										},
									},
								},
								{
									Name:  "AWS_SHARED_CREDENTIALS_FILE",
									Value: "/etc/aws/credentials",
								},
							},
							Command: []string{"/usr/bin/hypershift-operator"},
							Args:    []string{"run", "--namespace=$(MY_NAMESPACE)", "--deployment-name=operator", "--metrics-addr=:9000", fmt.Sprintf("--enable-ocp-cluster-monitoring=%t", o.EnableOCPClusterMonitoring), fmt.Sprintf("--enable-ci-debug-output=%t", o.EnableCIDebugOutput), fmt.Sprintf("--aws-region=%s", o.AWSRegion)},
							Ports: []corev1.ContainerPort{
								{
									Name:          "metrics",
									ContainerPort: 9000,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "credentials",
									MountPath: "/etc/aws",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "credentials",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: "hypershift-operator-creds",
								},
							},
						},
					},
				},
			},
		},
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
			},
		},
	}
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
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{
					"events",
					"configmaps",
					"pods",
					"pods/log",
					"secrets",
					"nodes",
					"namespaces",
					"serviceaccounts",
					"services",
				},
				Verbs: []string{"*"},
			},
			{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments"},
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

type HyperShiftControlPlanePriorityClass struct{}

func (o HyperShiftControlPlanePriorityClass) Build() *schedulingv1.PriorityClass {
	return &schedulingv1.PriorityClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PriorityClass",
			APIVersion: schedulingv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-control-plane",
		},
		Value:         100000000,
		GlobalDefault: false,
		Description:   "This priority class should be used for hypershift control plane pods not critical to serving the API.",
	}
}

type HyperShiftAPICriticalPriorityClass struct{}

func (o HyperShiftAPICriticalPriorityClass) Build() *schedulingv1.PriorityClass {
	return &schedulingv1.PriorityClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PriorityClass",
			APIVersion: schedulingv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-api-critical",
		},
		Value:         100001000,
		GlobalDefault: false,
		Description:   "This priority class should be used for hypershift control plane pods critical to serving the API.",
	}
}

type HyperShiftEtcdPriorityClass struct{}

func (o HyperShiftEtcdPriorityClass) Build() *schedulingv1.PriorityClass {
	return &schedulingv1.PriorityClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PriorityClass",
			APIVersion: schedulingv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-etcd",
		},
		Value:         100002000,
		GlobalDefault: false,
		Description:   "This priority class should be used for hypershift etcd pods.",
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
