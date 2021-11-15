package kubeapiserverproxy

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/manifests"
)

func ReconcileyDaemonset(
	ds *appsv1.DaemonSet,
	listenAddr string,
	listenPort int,
	haProxyImage string,
) func() error {
	return func() error {
		ds.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: map[string]string{"app": "kube-apiserver-proxy"},
		}
		ds.Spec.Template.Labels = map[string]string{"app": "kube-apiserver-proxy"}
		ds.Spec.Template.Spec.HostNetwork = true
		ds.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:  "haproxy",
			Image: haProxyImage,
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("13m"),
				corev1.ResourceMemory: resource.MustParse("16m"),
			}},
			LivenessProbe: &corev1.Probe{
				FailureThreshold:    3,
				InitialDelaySeconds: 120,
				PeriodSeconds:       120,
				SuccessThreshold:    1,
				Handler: corev1.Handler{HTTPGet: &corev1.HTTPGetAction{
					Path:   "/version",
					Scheme: corev1.URISchemeHTTPS,
					Host:   listenAddr,
					Port:   intstr.FromInt(listenPort),
				}},
				TimeoutSeconds: 60,
			},
			Command: []string{"haproxy", "-f", "/usr/local/etc/haproxy"},
			VolumeMounts: []corev1.VolumeMount{{
				Name:      "config",
				MountPath: "/usr/local/etc/haproxy",
			}},
		}}
		ds.Spec.Template.Spec.PriorityClassName = "system-node-critical"
		ds.Spec.Template.Spec.Volumes = []corev1.Volume{{
			Name: "config",
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{
				Name: manifests.KubeAPIServerProxyConfigMap().Name,
			}}},
		}}

		return nil
	}
}

func ReconcileConfigMap(listenAddr string, listenPort int, apiServerAddr string, apiServerPort int, cm *corev1.ConfigMap) func() error {
	return func() error {
		cm.Data = map[string]string{
			"haproxy.cfg": fmt.Sprintf(haproxyCFgTemplate, listenAddr, listenPort, apiServerAddr, apiServerPort),
		}
		return nil
	}
}

const haproxyCFgTemplate = `global
  maxconn 7000
  log stdout local0
  log stdout local1 notice

defaults
  mode tcp
  timeout client 10m
  timeout server 10m
  timeout connect 10s
  timeout client-fin 5s
  timeout server-fin 5s
  timeout queue 5s
  retries 3

frontend local_apiserver
	bind %s:%d
  log global
  mode tcp
  option tcplog
  default_backend remote_apiserver

backend remote_apiserver
  mode tcp
  log global
  option httpchk GET /version
  option log-health-checks
  default-server inter 10s fall 3 rise 3
	server controlplane %s:%d
`
