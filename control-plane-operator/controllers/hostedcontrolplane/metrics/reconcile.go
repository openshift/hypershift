package metrics

import (
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/render"
	utililty "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
	"github.com/openshift/hypershift/hypershift-operator/controllers/util"
	monitoring "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"
)

var (
	roksMetricsLabels      = map[string]string{"app": "metrics"}
	roksMetricPusherLabels = map[string]string{"app": "push-gateway"}
)

func ReconcileRoksMetricsDeployment(cm *corev1.ConfigMap, ownerRef config.OwnerRef, sa *corev1.ServiceAccount, roksMetricsImage string) error {
	ownerRef.ApplyTo(cm)
	roksMetricDeployment := manifests.RoksMetricsDeployment()
	if err := reconcileRoksMetricsDeployment(roksMetricDeployment, sa, roksMetricsImage); err != nil {
		return err
	}
	return utililty.ReconcileWorkerManifest(cm, roksMetricDeployment)
}

func reconcileRoksMetricsDeployment(deployment *appsv1.Deployment, sa *corev1.ServiceAccount, roksMetricsImage string) error {
	defaultMode := int32(420)
	maxSurge := intstr.FromInt(2)
	maxUnavailable := intstr.FromInt(1)
	deployment.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{
		MaxSurge:       &maxSurge,
		MaxUnavailable: &maxUnavailable,
	}
	if len(render.NewClusterParams().RestartDate) > 0 {
		deployment.Spec.Template.ObjectMeta.Annotations = map[string]string{
			"openshift.io/restartedAt": render.NewClusterParams().RestartDate,
		}
	}
	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: k8sutilspointer.Int32Ptr(1),
		Selector: &metav1.LabelSelector{
			MatchLabels: roksMetricsLabels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: roksMetricsLabels,
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: sa.Name,
				PriorityClassName:  "system-cluster-critical",
				Volumes: []corev1.Volume{
					{
						Name: "serving-cert",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								DefaultMode: &defaultMode,
								SecretName:  "serving-cert",
								Optional:    util.True(),
							},
						},
					},
				},
				Tolerations: []corev1.Toleration{
					{
						Key:      "multi-az-worker",
						Operator: "Equal",
						Value:    "true",
						Effect:   corev1.TaintEffectNoSchedule,
					},
				},
				Containers: []corev1.Container{
					{
						Name:            "metrics",
						Image:           roksMetricsImage,
						ImagePullPolicy: corev1.PullAlways,
						Ports: []corev1.ContainerPort{
							{
								Name:          "https",
								ContainerPort: 8443,
							},
						},
						Command: []string{"/usr/bin/roks-metrics"},
						Args: []string{
							"--alsologtostderr",
							"--v=3",
							"--listen=:8443",
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "serving-cert",
								ReadOnly:  true,
								MountPath: "/var/run/secrets/serving-cert",
							},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("50Mi"),
							},
						},
					},
				},
			},
		},
	}
	return nil
}

func ReconcileRoksMetricsClusterRole(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(cm)
	role := manifests.RoksMetricsClusterRole()
	if err := reconcileRoksMetricsClusterRole(role); err != nil {
		return err
	}
	return utililty.ReconcileWorkerManifest(cm, role)
}

func reconcileRoksMetricsClusterRole(role *rbacv1.ClusterRole) error {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"config.openshift.io"},
			Resources: []string{"infrastructures", "featuregates", "proxies"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"build.openshift.io"},
			Resources: []string{"builds"},
			Verbs:     []string{"get", "list", "watch"},
		},
	}
	return nil
}

func ReconcileRoksMetricsRoleBinding(cm *corev1.ConfigMap, ownerRef config.OwnerRef, role *rbacv1.ClusterRole, sa *corev1.ServiceAccount) error {
	ownerRef.ApplyTo(cm)
	roksMetricRoleBinding := manifests.RoksMetricsRoleBinding()
	if err := reconcileRoksMetricsRoleBinding(roksMetricRoleBinding, role, sa); err != nil {
		return err
	}
	return utililty.ReconcileWorkerManifest(cm, roksMetricRoleBinding)
}

func reconcileRoksMetricsRoleBinding(binding *rbacv1.ClusterRoleBinding, role *rbacv1.ClusterRole, sa *corev1.ServiceAccount) error {
	binding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     role.Name,
	}

	binding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      sa.Name,
			Namespace: sa.Namespace,
		},
	}

	return nil
}

func ReconcileRocksMetricsServiceMonitor(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(cm)
	roksMetricServiceMonitor := manifests.RoksMetricsServiceMonitor()
	if err := reconcileRocksMetricsServiceMonitor(roksMetricServiceMonitor); err != nil {
		return err
	}
	return utililty.ReconcileWorkerManifest(cm, roksMetricServiceMonitor)
}

func reconcileRocksMetricsServiceMonitor(svcMonitor *monitoring.ServiceMonitor) error {
	svcMonitor.Spec.Selector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "metrics",
		},
	}
	relabelConfigs := []*monitoring.RelabelConfig{
		{
			Action:       "drop",
			SourceLabels: []string{"__name__"},
			Regex:        "apiserver_.*",
		},
		{
			Action:       "drop",
			SourceLabels: []string{"__name__"},
			Regex:        "go_.*",
		},
		{
			Action:       "drop",
			SourceLabels: []string{"__name__"},
			Regex:        "promhttp_.*",
		},
	}

	svcMonitor.Spec.Endpoints = []monitoring.Endpoint{{
		Interval:             "30s",
		Port:                 "https",
		Scheme:               "https",
		Path:                 "/metrics",
		MetricRelabelConfigs: relabelConfigs,
		TLSConfig: &monitoring.TLSConfig{
			CAFile: "/etc/prometheus/configmaps/serving-certs-ca-bundle/service-ca.crt",
			SafeTLSConfig: monitoring.SafeTLSConfig{
				ServerName: "roks-metrics.openshift-roks-metrics.svc",
			},
		},
	}}

	return nil
}

func ReconcileRocksMetricsService(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(cm)
	roksMetricService := manifests.RoksMetricsService()
	if err := reconcileRocksMetricsService(roksMetricService); err != nil {
		return err
	}
	return utililty.ReconcileWorkerManifest(cm, roksMetricService)
}

func reconcileRocksMetricsService(svc *corev1.Service) error {
	svc.Spec.Selector = map[string]string{
		"app": "metrics",
	}
	var portSpec corev1.ServicePort
	if len(svc.Spec.Ports) > 0 {
		portSpec = svc.Spec.Ports[0]
	} else {
		svc.Spec.Ports = []corev1.ServicePort{portSpec}
	}
	portSpec.Port = int32(8443)
	portSpec.Name = "https"
	portSpec.Protocol = corev1.ProtocolTCP
	portSpec.TargetPort = intstr.FromInt(8443)
	svc.Spec.Ports[0] = portSpec
	svc.Spec.Type = corev1.ServiceTypeClusterIP
	return nil
}

func ReconcilePrometheusRoleBinding(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(cm)
	roksMetricRoleBinding := manifests.PrometheusK8sRoleBinding()
	if err := reconcilePrometheusRoleBinding(roksMetricRoleBinding); err != nil {
		return err
	}
	return utililty.ReconcileWorkerManifest(cm, roksMetricRoleBinding)
}

func reconcilePrometheusRoleBinding(binding *rbacv1.RoleBinding) error {
	binding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     "prometheus-k8s",
	}

	binding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      "prometheus-k8s",
			Namespace: "openshift-monitoring",
		},
	}

	return nil
}

func ReconcileRoksMetricsPusherDeployment(cm *corev1.ConfigMap, ownerRef config.OwnerRef, sa *corev1.ServiceAccount, roksMetricsImage string) error {
	ownerRef.ApplyTo(cm)
	roksMetricPusherDeployment := manifests.MetricPusherDeployment()
	if err := reconcileRoksMetricsPusherDeployment(roksMetricPusherDeployment, sa, roksMetricsImage); err != nil {
		return err
	}
	return utililty.ReconcileWorkerManifest(cm, roksMetricPusherDeployment)
}

func reconcileRoksMetricsPusherDeployment(deployment *appsv1.Deployment, sa *corev1.ServiceAccount, roksMetricsImage string) error {
	defaultMode := int32(420)
	maxSurge := intstr.FromInt(2)
	maxUnavailable := intstr.FromInt(1)
	deployment.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{
		MaxSurge:       &maxSurge,
		MaxUnavailable: &maxUnavailable,
	}
	if len(render.NewClusterParams().RestartDate) > 0 {
		deployment.Spec.Template.ObjectMeta.Annotations = map[string]string{
			"openshift.io/restartedAt": render.NewClusterParams().RestartDate,
		}
	}
	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: k8sutilspointer.Int32Ptr(1),
		Selector: &metav1.LabelSelector{
			MatchLabels: roksMetricPusherLabels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: roksMetricPusherLabels,
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: sa.Name,
				PriorityClassName:  "system-cluster-critical",
				Volumes: []corev1.Volume{
					{
						Name: "serving-cert",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								DefaultMode: &defaultMode,
								SecretName:  "serving-cert",
								Optional:    util.True(),
							},
						},
					},
				},
				Tolerations: []corev1.Toleration{
					{
						Key:      "multi-az-worker",
						Operator: "Equal",
						Value:    "true",
						Effect:   corev1.TaintEffectNoSchedule,
					},
				},
				Containers: []corev1.Container{
					{
						Name: "push-gateway",

						Image:           roksMetricsImage,
						ImagePullPolicy: corev1.PullAlways,
						Command:         []string{"pushgateway"},
						Ports: []corev1.ContainerPort{
							{
								Name:          "http",
								ContainerPort: 9091,
							},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("50Mi"),
							},
						},
					},
				},
			},
		},
	}
	return nil
}

func ReconcileRocksMetricsPusherServiceMonitor(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(cm)
	roksMetricPusherServiceMonitor := manifests.RoksMetricsServiceMonitor()
	if err := reconcileRocksMetricsPusherServiceMonitor(roksMetricPusherServiceMonitor); err != nil {
		return err
	}
	return utililty.ReconcileWorkerManifest(cm, roksMetricPusherServiceMonitor)
}

func reconcileRocksMetricsPusherServiceMonitor(svcMonitor *monitoring.ServiceMonitor) error {
	svcMonitor.Spec.Selector = metav1.LabelSelector{
		MatchLabels: roksMetricPusherLabels,
	}

	svcMonitor.Spec.Endpoints = []monitoring.Endpoint{{
		Interval:    "30s",
		Port:        "http",
		Path:        "/metrics",
		HonorLabels: true,
	}}

	return nil
}

func ReconcileRocksMetricsPusherService(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(cm)
	roksMetricPusherService := manifests.MetricPusherService()
	if err := reconcileRocksMetricsPusherService(roksMetricPusherService); err != nil {
		return err
	}
	return utililty.ReconcileWorkerManifest(cm, roksMetricPusherService)
}

func reconcileRocksMetricsPusherService(svc *corev1.Service) error {
	svc.Spec.Selector = roksMetricPusherLabels
	var portSpec corev1.ServicePort
	if len(svc.Spec.Ports) > 0 {
		portSpec = svc.Spec.Ports[0]
	} else {
		svc.Spec.Ports = []corev1.ServicePort{portSpec}
	}
	portSpec.Port = int32(9091)
	portSpec.Name = "https"
	portSpec.Protocol = corev1.ProtocolTCP
	portSpec.TargetPort = intstr.FromInt(9091)
	svc.Spec.Ports[0] = portSpec
	svc.Spec.Type = corev1.ServiceTypeClusterIP
	return nil
}

func ReconcileRocksMetricsServiceAccount(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(cm)
	roksMetricServiceAccount := manifests.RoksMetricsServiceAccount()
	if err := reconcileRocksMetricsServiceAccount(roksMetricServiceAccount); err != nil {
		return err
	}
	return utililty.ReconcileWorkerManifest(cm, roksMetricServiceAccount)
}

func reconcileRocksMetricsServiceAccount(sa *corev1.ServiceAccount) error {
	sa.Namespace = "openshift-roks-metrics"
	return nil
}

func ReconcileRoksMetricsNameSpace(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(cm)
	roksMetricNameSpace := manifests.RoksMetricsNameSpace()
	if err := reconcileRocksMetricsNameSpace(roksMetricNameSpace); err != nil {
		return err
	}
	return utililty.ReconcileWorkerManifest(cm, roksMetricNameSpace)
}

func reconcileRocksMetricsNameSpace(ns *corev1.Namespace) error {
	ns.Namespace = "openshift-roks-metrics"
	ns.Labels = map[string]string{
		"openshift.io/cluster-monitoring": "true",
	}
	return nil
}
