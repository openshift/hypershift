package manifests

import (
	"fmt"
	"strings"

	mcfgv1 "github.com/openshift/hypershift/thirdparty/machineconfigoperator/pkg/apis/machineconfiguration.openshift.io/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func HostedControlPlaneNamespace(hostedClusterNamespace, hostedClusterName string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", hostedClusterNamespace, strings.ReplaceAll(hostedClusterName, ".", "-")),
		},
	}
}

func KubeConfigSecret(hostedClusterNamespace string, hostedClusterName string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedClusterNamespace,
			Name:      hostedClusterName + "-admin-kubeconfig",
		},
	}
}

func KubeadminPasswordSecret(hostedClusterNamespace string, hostedClusterName string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedClusterNamespace,
			Name:      hostedClusterName + "-kubeadmin-password",
		},
	}
}

func MachineConfigAPIServerHAProxy() *mcfgv1.MachineConfig {
	return &mcfgv1.MachineConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "20-apiserver-haproxy",
		},
	}
}

func OperatorDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      "operator",
		},
	}
}
