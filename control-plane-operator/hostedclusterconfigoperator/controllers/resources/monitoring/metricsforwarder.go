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
	metricsForwarderPort     = 8443
)

func ReconcileMetricsForwarderDeployment(deployment *appsv1.Deployment, haproxyImage string) error {
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
							{
								Name:      "ca-cert",
								MountPath: "/etc/haproxy/certs",
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
					{
						Name: "ca-cert",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName: "metrics-forwarder-ca",
							},
						},
					},
				},
			},
		},
	}
	return nil
}

func ReconcileMetricsForwarderConfigMap(cm *corev1.ConfigMap, routeHost string) error {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data["haproxy.cfg"] = fmt.Sprintf(`global
    log stdout format raw local0

defaults
    mode http
    timeout connect 5s
    timeout client 30s
    timeout server 30s

frontend metrics_frontend
    bind *:%d
    default_backend metrics_proxy

backend metrics_proxy
    server proxy %s:443 ssl verify required ca-file /etc/haproxy/certs/ca.crt sni str(%s)
    http-request set-header Host %s
`, metricsForwarderPort, routeHost, routeHost, routeHost)
	return nil
}

func ReconcileMetricsForwarderCASecret(secret *corev1.Secret, caCert []byte) error {
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data["ca.crt"] = caCert
	return nil
}

func ReconcileMetricsForwarderBearerTokenSecret(secret *corev1.Secret) error {
	// This Secret is of type kubernetes.io/service-account-token. Kubernetes
	// automatically populates its "token" field with a long-lived token for
	// the referenced SA. Prometheus then reads this token via the PodMonitor's
	// Authorization.Credentials reference.
	secret.Type = corev1.SecretTypeServiceAccountToken
	if secret.Annotations == nil {
		secret.Annotations = map[string]string{}
	}
	secret.Annotations[corev1.ServiceAccountNameKey] = "prometheus-k8s"
	return nil
}

func ReconcileMetricsForwarderPodMonitor(podMonitor *prometheusoperatorv1.PodMonitor, componentNames []string, bearerTokenSecretName string) error {
	podMonitor.Spec.Selector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "control-plane-metrics-forwarder",
		},
	}

	http := prometheusoperatorv1.Scheme("http")

	sorted := make([]string, len(componentNames))
	copy(sorted, componentNames)
	slices.Sort(sorted)

	// The metrics-proxy requires a bearer token from the guest cluster's
	// prometheus-k8s SA. Prometheus sends the token in the Authorization header;
	// HAProxy forwards it transparently to the metrics-proxy backend.
	auth := &prometheusoperatorv1.SafeAuthorization{
		Credentials: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: bearerTokenSecretName,
			},
			Key: "token",
		},
	}

	endpoints := make([]prometheusoperatorv1.PodMetricsEndpoint, 0, len(sorted))
	for _, name := range sorted {
		endpoints = append(endpoints, prometheusoperatorv1.PodMetricsEndpoint{
			Port: ptr.To(metricsForwarderPortName),
			Path: fmt.Sprintf("/metrics/%s", name),
			Scheme: &http,
			// HonorLabels preserves the original metric labels (job, namespace,
			// pod, instance, etc.) as injected by the metrics-proxy, instead of
			// overwriting them with the PodMonitor's scrape labels. This ensures
			// dashboards and recording rules that reference e.g. job="kube-apiserver"
			// work identically to standalone OpenShift.
			HonorLabels: true,
			HTTPConfigWithProxy: prometheusoperatorv1.HTTPConfigWithProxy{
				HTTPConfig: prometheusoperatorv1.HTTPConfig{
					HTTPConfigWithoutTLS: prometheusoperatorv1.HTTPConfigWithoutTLS{
						Authorization: auth,
					},
				},
			},
		})
	}

	podMonitor.Spec.PodMetricsEndpoints = endpoints
	return nil
}
