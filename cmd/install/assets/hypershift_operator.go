package assets

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type HyperShiftNamespace struct {
	Name string
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
	return namespace
}

type HyperShiftOperatorDeployment struct {
	Namespace      *corev1.Namespace
	OperatorImage  string
	ServiceAccount *corev1.ServiceAccount
	Replicas       int32
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
						"name": "operator",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: o.ServiceAccount.Name,
					Containers: []corev1.Container{
						{
							Name:            "operator",
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
							},
							Command: []string{"/usr/bin/hypershift-operator"},
							Args:    []string{"run", "--namespace=$(MY_NAMESPACE)", "--deployment-name=operator", "--metrics-addr=:9000"},
							Ports: []corev1.ContainerPort{
								{
									Name:          "metrics",
									ContainerPort: 9000,
									Protocol:      corev1.ProtocolTCP,
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

type HyperShiftHostedClustersCustomResourceDefinition struct{}

func (o HyperShiftHostedClustersCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	return getCustomResourceDefinition("hypershift-operator/hypershift.openshift.io_hostedclusters.yaml")
}

type HyperShiftNodePoolsCustomResourceDefinition struct{}

func (o HyperShiftNodePoolsCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	return getCustomResourceDefinition("hypershift-operator/hypershift.openshift.io_nodepools.yaml")
}

type HyperShiftHostedControlPlaneCustomResourceDefinition struct{}

func (o HyperShiftHostedControlPlaneCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	crd := getCustomResourceDefinition("hypershift-operator/hypershift.openshift.io_hostedcontrolplanes.yaml")
	if crd.Labels == nil {
		crd.Labels = map[string]string{}
	}
	crd.Labels["cluster.x-k8s.io/v1alpha4"] = "v1alpha1"
	return crd
}

type HyperShiftExternalInfraClustersCustomResourceDefinition struct{}

func (o HyperShiftExternalInfraClustersCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	crd := getCustomResourceDefinition("hypershift-operator/hypershift.openshift.io_externalinfraclusters.yaml")
	if crd.Labels == nil {
		crd.Labels = map[string]string{}
	}
	crd.Labels["cluster.x-k8s.io/v1alpha4"] = "v1alpha1"
	return crd
}

type HyperShiftMachineConfigServersCustomResourceDefinition struct{}

func (o HyperShiftMachineConfigServersCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	crd := getCustomResourceDefinition("hypershift-operator/hypershift.openshift.io_machineconfigservers.yaml")
	if crd.Labels == nil {
		crd.Labels = map[string]string{}
	}
	crd.Labels["cluster.x-k8s.io/v1alpha4"] = "v1alpha1"
	return crd
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
	Namespace *corev1.Namespace
	Role      *rbacv1.Role
}

func (o HyperShiftOperatorPrometheusRoleBinding) Build() *rbacv1.RoleBinding {
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
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "prometheus-user-workload",
				Namespace: "openshift-user-workload-monitoring",
			},
		},
	}
	return binding
}

type HyperShiftServiceMonitor struct {
	Namespace *corev1.Namespace
}

func (o HyperShiftServiceMonitor) Build() *unstructured.Unstructured {
	serviceMonitorJSON := `
{
   "apiVersion": "monitoring.coreos.com/v1",
   "kind": "ServiceMonitor",
   "metadata": {
      "name": "operator"
   },
   "spec": {
      "endpoints": [
         {
            "interval": "30s",
            "port": "metrics"
         }
      ],
      "jobLabel": "component",
      "selector": {
         "matchLabels": {
            "name": "operator"
         }
      }
   }
}
`
	obj, err := runtime.Decode(unstructured.UnstructuredJSONScheme, []byte(serviceMonitorJSON))
	if err != nil {
		panic(err)
	}
	sm := obj.(*unstructured.Unstructured)
	sm.SetNamespace(o.Namespace.Name)
	return sm
}
