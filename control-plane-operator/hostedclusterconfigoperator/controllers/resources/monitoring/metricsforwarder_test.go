package monitoring

import (
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func TestReconcileMetricsForwarderDeployment(t *testing.T) {
	tests := []struct {
		name       string
		image      string
		configHash string
	}{
		{
			name:       "When a config hash is provided it should set the checksum annotation",
			image:      "quay.io/openshift/haproxy:latest",
			configHash: "abc123",
		},
		{
			name:       "When an empty config hash is provided it should set an empty checksum annotation",
			image:      "quay.io/openshift/haproxy:latest",
			configHash: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			deployment := &appsv1.Deployment{}

			err := ReconcileMetricsForwarderDeployment(deployment, tt.image, tt.configHash)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(deployment.Spec.Template.Annotations).To(HaveKeyWithValue("metrics-forwarder-config-checksum", tt.configHash))
			g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
			g.Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal(tt.image))
		})
	}
}

func TestReconcileMetricsForwarderDeployment_ConfigHashChangeTrigersRollout(t *testing.T) {
	t.Run("When the config hash changes it should update the pod template annotation", func(t *testing.T) {
		g := NewGomegaWithT(t)
		deployment := &appsv1.Deployment{}

		err := ReconcileMetricsForwarderDeployment(deployment, "img:v1", "hash-a")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(deployment.Spec.Template.Annotations["metrics-forwarder-config-checksum"]).To(Equal("hash-a"))

		err = ReconcileMetricsForwarderDeployment(deployment, "img:v1", "hash-b")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(deployment.Spec.Template.Annotations["metrics-forwarder-config-checksum"]).To(Equal("hash-b"))
	})
}

func TestReconcileMetricsForwarderConfigMap(t *testing.T) {
	t.Run("When a route host is provided it should render haproxy.cfg with the correct backend", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cm := &corev1.ConfigMap{}

		err := ReconcileMetricsForwarderConfigMap(cm, "metrics-proxy.example.com")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cm.Data).To(HaveKey("haproxy.cfg"))
		g.Expect(cm.Data["haproxy.cfg"]).To(ContainSubstring("metrics-proxy.example.com:443"))
		g.Expect(cm.Data["haproxy.cfg"]).To(ContainSubstring(fmt.Sprintf("bind *:%d", metricsForwarderPort)))
		g.Expect(cm.Data["haproxy.cfg"]).To(ContainSubstring("mode tcp"))
	})

	t.Run("When route host changes it should produce a different config hash", func(t *testing.T) {
		g := NewGomegaWithT(t)

		cmA := &corev1.ConfigMap{}
		_ = ReconcileMetricsForwarderConfigMap(cmA, "host-a.example.com")
		cmB := &corev1.ConfigMap{}
		_ = ReconcileMetricsForwarderConfigMap(cmB, "host-b.example.com")

		hashA := util.HashSimple(cmA.Data["haproxy.cfg"])
		hashB := util.HashSimple(cmB.Data["haproxy.cfg"])
		g.Expect(hashA).ToNot(Equal(hashB))
	})

	t.Run("When called on a nil-data ConfigMap it should initialize Data", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cm := &corev1.ConfigMap{}
		g.Expect(cm.Data).To(BeNil())

		err := ReconcileMetricsForwarderConfigMap(cm, "host.example.com")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cm.Data).ToNot(BeNil())
	})
}

func TestReconcileMetricsForwarderServingCA(t *testing.T) {
	t.Run("When a CA cert is provided it should set ca.crt in the ConfigMap", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cm := &corev1.ConfigMap{}
		caCert := []byte("-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----")

		err := ReconcileMetricsForwarderServingCA(cm, caCert)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cm.Data).To(HaveKeyWithValue("ca.crt", string(caCert)))
	})

	t.Run("When called on a nil-data ConfigMap it should initialize Data", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cm := &corev1.ConfigMap{}

		err := ReconcileMetricsForwarderServingCA(cm, []byte("cert"))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cm.Data).ToNot(BeNil())
	})
}

func TestReconcileMetricsForwarderPodMonitor(t *testing.T) {
	t.Run("When component names are provided it should create sorted endpoints", func(t *testing.T) {
		g := NewGomegaWithT(t)
		pm := &prometheusoperatorv1.PodMonitor{}

		err := ReconcileMetricsForwarderPodMonitor(pm, []string{"etcd", "kube-apiserver", "controller-manager"}, "metrics-proxy.example.com")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(pm.Spec.PodMetricsEndpoints).To(HaveLen(3))

		// Verify sorted order.
		g.Expect(pm.Spec.PodMetricsEndpoints[0].Path).To(Equal("/metrics/controller-manager"))
		g.Expect(pm.Spec.PodMetricsEndpoints[1].Path).To(Equal("/metrics/etcd"))
		g.Expect(pm.Spec.PodMetricsEndpoints[2].Path).To(Equal("/metrics/kube-apiserver"))
	})

	t.Run("When endpoints are created they should use the tls-client-certificate-auth scrape class", func(t *testing.T) {
		g := NewGomegaWithT(t)
		pm := &prometheusoperatorv1.PodMonitor{}

		err := ReconcileMetricsForwarderPodMonitor(pm, []string{"kube-apiserver"}, "metrics-proxy.example.com")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(pm.Spec.ScrapeClassName).ToNot(BeNil())
		g.Expect(*pm.Spec.ScrapeClassName).To(Equal("tls-client-certificate-auth"))
	})

	t.Run("When endpoints are created they should reference the metrics-proxy-serving-ca ConfigMap", func(t *testing.T) {
		g := NewGomegaWithT(t)
		pm := &prometheusoperatorv1.PodMonitor{}

		err := ReconcileMetricsForwarderPodMonitor(pm, []string{"kube-apiserver"}, "metrics-proxy.example.com")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(pm.Spec.PodMetricsEndpoints).To(HaveLen(1))

		ep := pm.Spec.PodMetricsEndpoints[0]
		g.Expect(ep.TLSConfig).ToNot(BeNil())
		g.Expect(ep.TLSConfig.CA.ConfigMap).ToNot(BeNil())
		g.Expect(ep.TLSConfig.CA.ConfigMap.Name).To(Equal("metrics-proxy-serving-ca"))
		g.Expect(ep.TLSConfig.CA.ConfigMap.Key).To(Equal("ca.crt"))
	})

	t.Run("When endpoints are created they should set HonorLabels and ServerName", func(t *testing.T) {
		g := NewGomegaWithT(t)
		pm := &prometheusoperatorv1.PodMonitor{}
		routeHost := "metrics-proxy.example.com"

		err := ReconcileMetricsForwarderPodMonitor(pm, []string{"kube-apiserver"}, routeHost)
		g.Expect(err).ToNot(HaveOccurred())

		ep := pm.Spec.PodMetricsEndpoints[0]
		g.Expect(ep.HonorLabels).To(BeTrue())
		g.Expect(ep.TLSConfig.ServerName).ToNot(BeNil())
		g.Expect(*ep.TLSConfig.ServerName).To(Equal(routeHost))
	})

	t.Run("When component names are provided in unsorted order it should not modify the input slice", func(t *testing.T) {
		g := NewGomegaWithT(t)
		pm := &prometheusoperatorv1.PodMonitor{}
		input := []string{"z-component", "a-component"}

		err := ReconcileMetricsForwarderPodMonitor(pm, input, "host.example.com")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(input[0]).To(Equal("z-component"))
		g.Expect(input[1]).To(Equal("a-component"))
	})

	t.Run("When the selector is set it should match the metrics forwarder app label", func(t *testing.T) {
		g := NewGomegaWithT(t)
		pm := &prometheusoperatorv1.PodMonitor{}

		err := ReconcileMetricsForwarderPodMonitor(pm, []string{"kube-apiserver"}, "host.example.com")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(pm.Spec.Selector.MatchLabels).To(HaveKeyWithValue("app", "control-plane-metrics-forwarder"))
	})

	t.Run("When the endpoint scheme is set it should use HTTPS", func(t *testing.T) {
		g := NewGomegaWithT(t)
		pm := &prometheusoperatorv1.PodMonitor{}

		err := ReconcileMetricsForwarderPodMonitor(pm, []string{"kube-apiserver"}, "host.example.com")
		g.Expect(err).ToNot(HaveOccurred())

		ep := pm.Spec.PodMetricsEndpoints[0]
		g.Expect(ep.Scheme).ToNot(BeNil())
		g.Expect(strings.ToLower(string(*ep.Scheme))).To(Equal("https"))
	})
}
