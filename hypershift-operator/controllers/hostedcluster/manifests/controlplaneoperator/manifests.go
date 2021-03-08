package controlplaneoperator

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sutilspointer "k8s.io/utils/pointer"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	capiv1 "github.com/openshift/hypershift/thirdparty/clusterapi/api/v1alpha4"
)

const (
	hostedClusterAnnotation = "hypershift.openshift.io/cluster"
)

type OperatorDeployment struct {
	Namespace      *corev1.Namespace
	OperatorImage  string
	ServiceAccount *corev1.ServiceAccount
}

func (o OperatorDeployment) Build() *appsv1.Deployment {
	deployment := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: appsv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "control-plane-operator",
			Namespace: o.Namespace.Name,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: k8sutilspointer.Int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": "control-plane-operator",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name": "control-plane-operator",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: o.ServiceAccount.Name,
					Containers: []corev1.Container{
						{
							Name:            "control-plane-operator",
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
							Command: []string{"/usr/bin/control-plane-operator"},
							Args:    []string{"run", "--namespace", "$(MY_NAMESPACE)", "--deployment-name", "control-plane-operator"},
						},
					},
				},
			},
		},
	}
	return deployment
}

type OperatorServiceAccount struct {
	Namespace *corev1.Namespace
}

func (o OperatorServiceAccount) Build() *corev1.ServiceAccount {
	sa := &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "control-plane-operator",
		},
	}
	return sa
}

type OperatorClusterRole struct{}

func (o OperatorClusterRole) Build() *rbacv1.ClusterRole {
	role := &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRole",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "control-plane-operator",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"apiextensions.k8s.io"},
				Resources: []string{"customresourcedefinitions"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"config.openshift.io"},
				Resources: []string{"*"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"operator.openshift.io"},
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
		},
	}
	return role
}

type OperatorClusterRoleBinding struct {
	ClusterRole    *rbacv1.ClusterRole
	ServiceAccount *corev1.ServiceAccount
}

func (o OperatorClusterRoleBinding) Build() *rbacv1.ClusterRoleBinding {
	binding := &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "control-plane-operator" + o.ServiceAccount.Namespace,
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

type OperatorRole struct {
	Namespace *corev1.Namespace
}

func (o OperatorRole) Build() *rbacv1.Role {
	role := &rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Role",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "control-plane-operator",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"hypershift.openshift.io"},
				Resources: []string{"*"},
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
				APIGroups: []string{"route.openshift.io"},
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

type OperatorRoleBinding struct {
	Role           *rbacv1.Role
	ServiceAccount *corev1.ServiceAccount
}

func (o OperatorRoleBinding) Build() *rbacv1.RoleBinding {
	binding := &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Role.Namespace,
			Name:      "control-plane-operator",
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

type CAPICluster struct {
	Namespace     *corev1.Namespace
	HostedCluster *hyperv1.HostedCluster
}

func (o CAPICluster) Build() *capiv1.Cluster {
	cluster := &capiv1.Cluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Cluster",
			APIVersion: capiv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      o.HostedCluster.GetName(),
			Annotations: map[string]string{
				hostedClusterAnnotation: ctrlclient.ObjectKeyFromObject(o.HostedCluster).String(),
			},
		},
		Spec: capiv1.ClusterSpec{
			ControlPlaneEndpoint: capiv1.APIEndpoint{},
			ControlPlaneRef: &corev1.ObjectReference{
				APIVersion: "hypershift.openshift.io/v1alpha1",
				Kind:       "HostedControlPlane",
				Namespace:  o.Namespace.Name,
				Name:       o.HostedCluster.GetName(),
			},
			InfrastructureRef: &corev1.ObjectReference{
				APIVersion: "hypershift.openshift.io/v1alpha1",
				Kind:       "ExternalInfraCluster",
				Namespace:  o.Namespace.Name,
				Name:       o.HostedCluster.GetName(),
			},
		},
	}
	return cluster
}

func HostedControlPlaneName(namespace string, hostedClusterName string) types.NamespacedName {
	return types.NamespacedName{Namespace: namespace, Name: hostedClusterName}
}

type HostedControlPlane struct {
	Namespace           *corev1.Namespace
	HostedCluster       *hyperv1.HostedCluster
	ProviderCredentials *corev1.Secret
	PullSecret          *corev1.Secret
	SSHKey              *corev1.Secret
}

func (o HostedControlPlane) Build() *hyperv1.HostedControlPlane {
	name := HostedControlPlaneName(o.Namespace.Name, o.HostedCluster.Name)
	hcp := &hyperv1.HostedControlPlane{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HostedControlPlane",
			APIVersion: hyperv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: name.Namespace,
			Name:      name.Name,
			Annotations: map[string]string{
				hostedClusterAnnotation: ctrlclient.ObjectKeyFromObject(o.HostedCluster).String(),
			},
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			ProviderCreds: corev1.LocalObjectReference{
				Name: o.ProviderCredentials.Name,
			},
			PullSecret: corev1.LocalObjectReference{
				Name: o.PullSecret.Name,
			},
			SSHKey: corev1.LocalObjectReference{
				Name: o.SSHKey.Name,
			},
			ServiceCIDR:    o.HostedCluster.Spec.Networking.ServiceCIDR,
			PodCIDR:        o.HostedCluster.Spec.Networking.PodCIDR,
			MachineCIDR:    o.HostedCluster.Spec.Networking.MachineCIDR,
			ReleaseImage:   o.HostedCluster.Spec.Release.Image,
			InfraID:        o.HostedCluster.Spec.InfraID,
			Platform:       o.HostedCluster.Spec.Platform,
			ServiceType:    o.HostedCluster.Spec.ControlPlaneServiceType,
			ServiceAddress: o.HostedCluster.Spec.ControlPlaneServiceTypeNodePortAddress,
		},
	}
	return hcp
}

type ExternalInfraCluster struct {
	Namespace     *corev1.Namespace
	HostedCluster *hyperv1.HostedCluster
}

func (o ExternalInfraCluster) Build() *hyperv1.ExternalInfraCluster {
	eic := &hyperv1.ExternalInfraCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ExternalInfraCluster",
			APIVersion: hyperv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      o.HostedCluster.GetName(),
			Annotations: map[string]string{
				hostedClusterAnnotation: ctrlclient.ObjectKeyFromObject(o.HostedCluster).String(),
			},
		},
		Spec: hyperv1.ExternalInfraClusterSpec{
			ComputeReplicas: o.HostedCluster.Spec.InitialComputeReplicas,
		},
	}
	if o.HostedCluster.Spec.Platform.AWS != nil {
		eic.Spec.Region = o.HostedCluster.Spec.Platform.AWS.Region
	}
	return eic
}
