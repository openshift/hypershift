package manifests

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sutilspointer "k8s.io/utils/pointer"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func HostedControlPlaneNamespaceName(hostedClusterNamespace, hostedClusterName string) types.NamespacedName {
	return types.NamespacedName{
		Name: fmt.Sprintf("%s-%s", hostedClusterNamespace, hostedClusterName),
	}
}

func ProviderCredentialsName(hostedControlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: hostedControlPlaneNamespace,
		Name:      "provider-creds",
	}
}

type ProviderCredentials struct {
	Namespace *corev1.Namespace
	Data      []byte
}

func (o ProviderCredentials) Build() *corev1.Secret {
	name := ProviderCredentialsName(o.Namespace.Name)
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: name.Namespace,
			Name:      name.Name,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"credentials": o.Data},
	}
	return secret
}

type PullSecret struct {
	Namespace *corev1.Namespace
	Data      []byte
}

func (o PullSecret) Build() *corev1.Secret {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "pull-secret",
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{".dockerconfigjson": o.Data},
	}
	return secret
}

type SSHKey struct {
	Namespace *corev1.Namespace
	Data      []byte
}

func (o SSHKey) Build() *corev1.Secret {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "ssh-key",
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"id_rsa.pub": o.Data},
	}
	return secret
}

type DefaultNodePool struct {
	HostedCluster *hyperv1.HostedCluster
}

func (o DefaultNodePool) Build() *hyperv1.NodePool {
	nodePool := &hyperv1.NodePool{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NodePool",
			APIVersion: hyperv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.HostedCluster.GetNamespace(),
			Name:      o.HostedCluster.GetName(),
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: o.HostedCluster.GetName(),
			NodeCount:   k8sutilspointer.Int32Ptr(int32(o.HostedCluster.Spec.InitialComputeReplicas)),
		},
		Status: hyperv1.NodePoolStatus{},
	}
	if o.HostedCluster.Spec.Platform.AWS != nil {
		nodePool.Spec.Platform.AWS = o.HostedCluster.Spec.Platform.AWS.NodePoolDefaults
	}
	return nodePool
}

func KubeConfigSecretName(hostedClusterNamespace string, hostedClusterName string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: hostedClusterNamespace,
		Name:      hostedClusterName + "-admin-kubeconfig",
	}
}

type KubeConfigSecret struct {
	HostedCluster *hyperv1.HostedCluster
}

func (o KubeConfigSecret) Build() *corev1.Secret {
	name := KubeConfigSecretName(o.HostedCluster.Namespace, o.HostedCluster.Name)
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: name.Namespace,
			Name:      name.Name,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{},
	}
	return secret
}
