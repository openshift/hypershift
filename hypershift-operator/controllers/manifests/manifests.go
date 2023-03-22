package manifests

import (
	"fmt"
	routev1 "github.com/openshift/api/route/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
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

func KubevirtInfraTempRoute(namespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubevirt-infra-temp-route",
			Namespace: namespace,
		},
	}
}

func ReconcileKubevirtInfraTempRoute(route *routev1.Route) error {
	route.Spec = routev1.RouteSpec{
		To: routev1.RouteTargetReference{
			Kind: "Service",
			Name: "kubevirt-infra-fake-service",
		},
		Path: "/",
		Port: &routev1.RoutePort{
			TargetPort: intstr.FromInt(80),
		},
	}
	return nil
}
