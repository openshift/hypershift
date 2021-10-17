package manifests

import (
	routev1 "github.com/openshift/api/route/v1"
	monitoring "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func RoksMetricsNameSpace() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-roks-metrics",
		},
	}
}

func RoksMetricsNameSpaceWorkerManifest(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "user-manifest-roks-metrics-namespace",
		},
	}
}

func RoksMetricsRoute() *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "roks-metrics",
			Namespace: "openshift-roks-metrics",
		},
	}
}

func RoksMetricsRouteWorkerManifest(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-roks-metrics-route",
			Namespace: namespace,
		},
	}
}

func RoksMetricsService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "roks-metrics",
			Namespace: "openshift-roks-metrics",
		},
	}
}

func RoksMetricsServiceWorkerManifest(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-roks-metrics-service",
			Namespace: namespace,
		},
	}
}

func RoksMetricsServiceMonitor() *monitoring.ServiceMonitor {
	return &monitoring.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-roks-metrics",
			Name:      "roks-metrics",
		},
	}
}

func RoksMetricsServiceMonitorWorkerManifest(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-roks-metrics-service-monitor",
			Namespace: namespace,
		},
	}
}

func RoksMetricsServiceAccount() *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "roks-metrics",
			Namespace: "openshift-roks-metrics",
		},
	}
}

func RoksMetricsServiceAccountWorkerManifest(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-roks-metrics-service-account",
			Namespace: namespace,
		},
	}
}

func RoksMetricsClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "roks-metrics",
			Namespace: "openshift-roks-metrics",
		},
	}
}

func RoksMetricsClusterRoleWorkerManifest(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-roks-metrics-cluster-role",
			Namespace: namespace,
		},
	}
}

func RoksMetricsRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "roks-metrics",
			Namespace: "openshift-roks-metrics",
		},
	}
}

func RoksMetricsRoleBindingWorkerManifest(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-roks-metrics-cluster-role-binding",
			Namespace: namespace,
		},
	}
}

func RoksMetricsRole() *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "roks-metrics",
			Namespace: "openshift-roks-metrics",
		},
	}
}

func RoksMetricsRoleWorkerManifest(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-roks-metrics-role",
			Namespace: namespace,
		},
	}
}

func RoksMetricsDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "roks-metrics",
			Namespace: "openshift-roks-metrics",
		},
	}
}

func RoksMetricsDeploymentWorkerManifest(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-roks-metrics-deployment",
			Namespace: namespace,
		},
	}
}

func PrometheusK8sRoleBinding() *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prometheus-k8s",
			Namespace: "openshift-roks-metrics",
		},
	}
}

func PrometheusK8sRoleBindingWorkerManifest(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-roks-metrics-prometheus-k8s-rolebinding",
			Namespace: namespace,
		},
	}
}

func MetricPusherRoute() *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "push-gateway",
			Namespace: "openshift-roks-metrics",
		},
	}
}

func MetricPusherRouteWorkerManifest(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-roks-metrics-pusher-route",
			Namespace: namespace,
		},
	}
}

func MetricPusherService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "push-gateway",
			Namespace: "openshift-roks-metrics",
		},
	}
}

func MetricPusherServiceWorkerManifest(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-roks-metrics-pusher-service",
			Namespace: namespace,
		},
	}
}

func MetricPusherServiceMonitor() *monitoring.ServiceMonitor {
	return &monitoring.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "push-gateway",
			Namespace: "openshift-roks-metrics",
		},
	}
}

func MetricPusherServiceMonitorWorkerManifest(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-roks-metrics-pusher-service-monitor",
			Namespace: namespace,
		},
	}
}

func MetricPusherDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "push-gateway",
			Namespace: "openshift-roks-metrics",
		},
	}
}

func MetricPusherDeploymentWorkerManifest(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-roks-metrics-pusher-deployment",
			Namespace: namespace,
		},
	}
}
