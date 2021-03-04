package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sutilspointer "k8s.io/utils/pointer"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

type HostedControlPlaneNamespace struct {
	HostedCluster *hyperv1.HostedCluster
}

func (o HostedControlPlaneNamespace) Build() *corev1.Namespace {
	namespace := &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Namespace",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: o.HostedCluster.Name,
		},
	}
	return namespace
}

type ProviderCredentials struct {
	Namespace *corev1.Namespace
	Data      []byte
}

func (o ProviderCredentials) Build() *corev1.Secret {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "provider-creds",
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

type KubeConfigSecret struct {
	HostedCluster *hyperv1.HostedCluster
	Data          []byte
}

func (o KubeConfigSecret) Build() *corev1.Secret {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.HostedCluster.Namespace,
			Name:      o.HostedCluster.Name + "-admin-kubeconfig",
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"kubeconfig": o.Data},
	}
	return secret
}
