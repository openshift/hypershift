package monitoring

import (
	"fmt"
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const (
	metricsForwarderPortName = "metrics"
	metricsForwarderPort     = 9443
)

func ReconcileMetricsForwarderDeployment(deployment *appsv1.Deployment, haproxyImage string, configHash string) error {
	labels := map[string]string{
		"app": "control-plane-metrics-forwarder",
	}

	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: ptr.To[int32](1),
		Selector: &metav1.LabelSelector{
			MatchLabels: labels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: labels,
				Annotations: map[string]string{
					"metrics-forwarder-config-checksum": configHash,
				},
			},
			Spec: corev1.PodSpec{
				AutomountServiceAccountToken: ptr.To(false),
				SecurityContext: &corev1.PodSecurityContext{
					RunAsNonRoot: ptr.To(true),
				},
				Containers: []corev1.Container{
					{
						Name:  "haproxy",
						Image: haproxyImage,
						Command: []string{
							"haproxy",
							"-f",
							"/usr/local/etc/haproxy/haproxy.cfg",
						},
						Ports: []corev1.ContainerPort{
							{
								Name:          metricsForwarderPortName,
								ContainerPort: metricsForwarderPort,
								Protocol:      corev1.ProtocolTCP,
							},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("32Mi"),
							},
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							RunAsNonRoot:             ptr.To(true),
							SeccompProfile: &corev1.SeccompProfile{
								Type: corev1.SeccompProfileTypeRuntimeDefault,
							},
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
								Add:  []corev1.Capability{"NET_BIND_SERVICE"},
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "config",
								MountPath: "/usr/local/etc/haproxy",
								ReadOnly:  true,
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "config",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "metrics-forwarder-config",
								},
							},
						},
					},
				},
			},
		},
	}
	return nil
}

// ReconcileMetricsForwarderConfigMap builds the HAProxy configuration for TCP
// passthrough mode. HAProxy is opaque to TLS — the TLS ClientHello (including
// SNI) passes through transparently to the metrics-proxy.
func ReconcileMetricsForwarderConfigMap(cm *corev1.ConfigMap, routeHost string) error {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data["haproxy.cfg"] = fmt.Sprintf(`global
    log stdout format raw local0

defaults
    mode tcp
    timeout connect 5s
    timeout client 30s
    timeout server 30s

frontend metrics_proxy
    bind *:%d
    default_backend metrics_proxy_backend

backend metrics_proxy_backend
    server metrics-proxy %s:443
`, metricsForwarderPort, routeHost)
	return nil
}

// ReconcileMetricsForwarderServingCA syncs the metrics-proxy serving CA into
// a ConfigMap in the guest cluster. Prometheus uses this CA to verify the
// metrics-proxy's TLS certificate through the TCP passthrough proxy.
func ReconcileMetricsForwarderServingCA(cm *corev1.ConfigMap, caCert []byte) error {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data["ca.crt"] = string(caCert)
	return nil
}

// ReconcileMetricsForwarderPodMonitor builds a PodMonitor with one endpoint per
// control-plane component. It uses the tls-client-certificate-auth scrape class
// so CMO automatically injects the client cert from metrics-client-certs.
// The per-endpoint CA override references the metrics-proxy-serving-ca ConfigMap,
// which takes precedence over the scrape class's caFile (service-serving CA).
func ReconcileMetricsForwarderPodMonitor(podMonitor *prometheusoperatorv1.PodMonitor, componentNames []string, routeHost string) error {
	podMonitor.Spec.Selector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "control-plane-metrics-forwarder",
		},
	}

	scrapeClass := "tls-client-certificate-auth"
	podMonitor.Spec.ScrapeClassName = &scrapeClass

	https := prometheusoperatorv1.Scheme("https")

	sorted := make([]string, len(componentNames))
	copy(sorted, componentNames)
	slices.Sort(sorted)

	endpoints := make([]prometheusoperatorv1.PodMetricsEndpoint, 0, len(sorted))
	for _, name := range sorted {
		endpoints = append(endpoints, prometheusoperatorv1.PodMetricsEndpoint{
			Port:   ptr.To(metricsForwarderPortName),
			Path:   fmt.Sprintf("/metrics/%s", name),
			Scheme: &https,
			// HonorLabels preserves the original metric labels (job, namespace,
			// pod, instance, etc.) as injected by the metrics-proxy, instead of
			// overwriting them with the PodMonitor's scrape labels. This ensures
			// dashboards and recording rules that reference e.g. job="kube-apiserver"
			// work identically to standalone OpenShift.
			HonorLabels: true,
			HTTPConfigWithProxy: prometheusoperatorv1.HTTPConfigWithProxy{
				HTTPConfig: prometheusoperatorv1.HTTPConfig{
					TLSConfig: &prometheusoperatorv1.SafeTLSConfig{
						ServerName: &routeHost,
						// Override the scrape class's caFile with the metrics-proxy CA.
						// Per Prometheus Operator docs, per-endpoint CA takes precedence
						// over the scrape class's corresponding field.
						CA: prometheusoperatorv1.SecretOrConfigMap{
							ConfigMap: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "metrics-proxy-serving-ca",
								},
								Key: "ca.crt",
							},
						},
					},
				},
			},
		})
	}

	podMonitor.Spec.PodMetricsEndpoints = endpoints
	return nil
}
