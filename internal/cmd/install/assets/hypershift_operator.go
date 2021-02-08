package assets

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	Namespace     string
	OperatorImage string
}

func (o HyperShiftOperatorDeployment) Build() *appsv1.Deployment {
	deployment, err := newDeployment(mustAssetReader("hypershift-operator/operator-deployment.yaml"))
	if err != nil {
		panic(err)
	}
	deployment.Namespace = o.Namespace
	deployment.Spec.Template.Spec.Containers[0].Image = o.OperatorImage
	return deployment
}

type HyperShiftOperatorServiceAccount struct {
	Namespace string
}

func (o HyperShiftOperatorServiceAccount) Build() *corev1.ServiceAccount {
	sa := &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace,
			Name:      "operator",
		},
	}
	return sa
}

type HyperShiftOperatorClusterRole struct{}

func (o HyperShiftOperatorClusterRole) Build() *rbacv1.ClusterRole {
	role, err := newClusterRole(mustAssetReader("hypershift-operator/operator-clusterrole.yaml"))
	if err != nil {
		panic(err)
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
	return mustCustomResourceDefinition(mustAssetReader("hypershift-operator/hypershift.openshift.io_hostedclusters.yaml"))
}

type HyperShiftNodePoolsCustomResourceDefinition struct{}

func (o HyperShiftNodePoolsCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	return mustCustomResourceDefinition(mustAssetReader("hypershift-operator/hypershift.openshift.io_nodepools.yaml"))
}

type HyperShiftHostedControlPlaneCustomResourceDefinition struct{}

func (o HyperShiftHostedControlPlaneCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	crd := mustCustomResourceDefinition(mustAssetReader("hypershift-operator/hypershift.openshift.io_hostedcontrolplanes.yaml"))
	if crd.Labels == nil {
		crd.Labels = map[string]string{}
	}
	crd.Labels["cluster.x-k8s.io/v1alpha4"] = "v1alpha1"
	return crd
}

type HyperShiftExternalInfraClustersCustomResourceDefinition struct{}

func (o HyperShiftExternalInfraClustersCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	crd := mustCustomResourceDefinition(mustAssetReader("hypershift-operator/hypershift.openshift.io_externalinfraclusters.yaml"))
	if crd.Labels == nil {
		crd.Labels = map[string]string{}
	}
	crd.Labels["cluster.x-k8s.io/v1alpha4"] = "v1alpha1"
	return crd
}
