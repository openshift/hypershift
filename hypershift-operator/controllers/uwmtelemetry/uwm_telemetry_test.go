package uwmtelemetry

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/monitoring"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"
)

const sampleTelemetryConfig = `
matches:
#
# owners: (@openshift/openshift-team-monitoring, @smarterclayton)
#
# cluster:usage recording rules summarize important usage information
# about the cluster that points to specific features or component usage
# that may help identify problems or specific workloads. For example,
# cluster:usage:openshift:build:rate24h would show the number of builds
# executed within a 24h period so as to determine whether the current
# cluster is using builds and may be susceptible to eviction due to high
# disk usage from build temporary directories.
# All metrics under this prefix must have low (1-5) cardinality and must
# be well-scoped and follow proper naming and scoping conventions.
- '{__name__=~"cluster:usage:.*"}'
#
# owners: (@openshift/openshift-team-monitoring, @smarterclayton)
#
# count:up0 contains the count of cluster monitoring sources being marked as down.
# This information is relevant to the health of the registered
# cluster monitoring sources on a cluster. This metric allows telemetry
# to identify when an update causes a service to begin to crash-loop or
# flake.
- '{__name__="count:up0"}'
#
# owners: (@openshift/openshift-team-monitoring, @smarterclayton)
#
# count:up1 contains the count of cluster monitoring sources being marked as up.
# This information is relevant to the health of the registered
# cluster monitoring sources on a cluster. This metric allows telemetry
# to identify when an update causes a service to begin to crash-loop or
# flake.
- '{__name__="count:up1"}'
#
# owners: (@openshift/openshift-team-cincinnati)
#
# cluster_version reports what payload and version the cluster is being
# configured to and is used to identify what versions are on a cluster that
# is experiencing problems.
#
# consumers: (@openshift/openshift-team-cluster-manager)
- '{__name__="cluster_version"}'
#
# owners: (@openshift/openshift-team-cincinnati)
#
# cluster_version_available_updates reports the channel and version server
# the cluster is configured to use and how many updates are available. This
# is used to ensure that updates are being properly served to clusters.
#
# consumers: (@openshift/openshift-team-cluster-manager)
- '{__name__="cluster_version_available_updates"}'
#
# owners: (@openshift/openshift-team-cincinnati)
#
# cluster_version_capability reports the names of enabled and available
# cluster capabilities.  This is used to gauge the popularity of optional
# components and exposure to any component-specific issues.
- '{__name__="cluster_version_capability"}'
`

func TestReconcileUWMConfigContent(t *testing.T) {
	tests := []struct {
		name     string
		initial  string
		expected string
	}{
		{
			name: "no existing config",
			expected: `prometheus:
  remoteWrite:
  - authorization:
      credentials:
        key: token
        name: telemetry-remote-write
      type: Bearer
    queueConfig:
      batchSendDeadline: 1m
      capacity: 30000
      maxBackoff: 256s
      maxSamplesPerSend: 10000
      minBackoff: 1s
    url: https://infogw.api.openshift.com/metrics/v1/receive
`,
		},
		{
			name: "other keys present should be preserved",
			initial: `foo: bar
goo: baz
prometheus:
  random1: one
`,
			expected: `foo: bar
goo: baz
prometheus:
  random1: one
  remoteWrite:
  - authorization:
      credentials:
        key: token
        name: telemetry-remote-write
      type: Bearer
    queueConfig:
      batchSendDeadline: 1m
      capacity: 30000
      maxBackoff: 256s
      maxSamplesPerSend: 10000
      minBackoff: 1s
    url: https://infogw.api.openshift.com/metrics/v1/receive
`,
		},
		{
			name: "other remote write configs should be preserved",
			initial: `prometheus:
  remoteWrite:
  - queueConfig:
      batchSendDeadline: 5m
    url: http://www.example.com
`,
			expected: `prometheus:
  remoteWrite:
  - queueConfig:
      batchSendDeadline: 5m
    url: http://www.example.com
  - authorization:
      credentials:
        key: token
        name: telemetry-remote-write
      type: Bearer
    queueConfig:
      batchSendDeadline: 1m
      capacity: 30000
      maxBackoff: 256s
      maxSamplesPerSend: 10000
      minBackoff: 1s
    url: https://infogw.api.openshift.com/metrics/v1/receive
`,
		},
		{
			name: "existing telemetry config should be updated",
			initial: `prometheus:
  remoteWrite:
  - queueConfig:
      batchSendDeadline: 5m
    url: http://www.example.com
  - authorization:
      credentials:
        key: token
        name: different-secret
      type: Bearer
    queueConfig:
      batchSendDeadline: 1m
      capacity: 100
      maxBackoff: 36s
      maxSamplesPerSend: 50
      minBackoff: 3s
    url: https://infogw.api.openshift.com/metrics/v1/receive
`,
			expected: `prometheus:
  remoteWrite:
  - queueConfig:
      batchSendDeadline: 5m
    url: http://www.example.com
  - authorization:
      credentials:
        key: token
        name: telemetry-remote-write
      type: Bearer
    queueConfig:
      batchSendDeadline: 1m
      capacity: 30000
      maxBackoff: 256s
      maxSamplesPerSend: 10000
      minBackoff: 1s
    url: https://infogw.api.openshift.com/metrics/v1/receive
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			cm := &corev1.ConfigMap{}
			if len(test.initial) > 0 {
				cm.Data = map[string]string{
					"config.yaml": test.initial,
				}
			}
			err := reconcileUWMConfigContent(cm, nil)
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(cm.Data["config.yaml"]).To(Equal(test.expected))
		})
	}
}

func TestTelemetryConfigToRelabelConfig(t *testing.T) {
	expectedRegex := "(count:up0|count:up1|cluster_version|cluster_version_available_updates|cluster_version_capability)"
	inputCM := &corev1.ConfigMap{
		Data: map[string]string{
			"metrics.yaml": sampleTelemetryConfig,
		},
	}
	g := NewGomegaWithT(t)
	outputConfig, err := telemetryConfigToRelabelConfig(inputCM)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(outputConfig.Regex).To(Equal(expectedRegex))
}

func TestReconcile(t *testing.T) {
	validateMonitoringConfig := func(g *WithT, cm *corev1.ConfigMap) {
		content := cm.Data["config.yaml"]
		g.Expect(content).ToNot(BeEmpty())
		parsedContent := map[string]interface{}{}
		err := yaml.Unmarshal([]byte(content), &parsedContent)
		g.Expect(err).ToNot(HaveOccurred())
		value, found, err := unstructured.NestedBool(parsedContent, "enableUserWorkload")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(found).To(BeTrue())
		g.Expect(value).To(BeTrue())
	}

	validateUWMConfig := func(g *WithT, cm *corev1.ConfigMap) {
		content := cm.Data["config.yaml"]
		g.Expect(content).ToNot(BeEmpty())
		parsedContent := map[string]interface{}{}
		err := yaml.Unmarshal([]byte(content), &parsedContent)
		g.Expect(err).ToNot(HaveOccurred())
		remoteWriteConfigs, found, err := unstructured.NestedSlice(parsedContent, "prometheus", "remoteWrite")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(found).To(BeTrue())
		g.Expect(len(remoteWriteConfigs)).To(Equal(1))
		rawConfig, ok := remoteWriteConfigs[0].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		url, exists, err := unstructured.NestedString(rawConfig, "url")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeTrue())
		g.Expect(url).To(Equal(telemetryRemoteWriteURL))
	}

	clusterID := "fake-cluster-id"

	validateRemoteWriteSecret := func(g *WithT, cm *corev1.Secret) {
		token := cm.Data["token"]
		g.Expect(token).ToNot(BeEmpty())
		decodedToken, err := base64.StdEncoding.DecodeString(string(token))
		g.Expect(err).ToNot(HaveOccurred())
		value := map[string]interface{}{}
		err = json.Unmarshal(decodedToken, &value)
		g.Expect(err).ToNot(HaveOccurred())
		actualClusterID, exists, err := unstructured.NestedString(value, "cluster_id")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeTrue())
		g.Expect(actualClusterID).To(Equal(clusterID))
	}

	clusterVersion := func() *configv1.ClusterVersion {
		cv := monitoring.ClusterVersion()
		cv.Spec.ClusterID = configv1.ClusterID(clusterID)
		return cv
	}

	telemeterClientSecret := func() *corev1.Secret {
		secret := monitoring.TelemeterClientSecret()
		secret.Data = map[string][]byte{"token": []byte("fake-token")}
		return secret
	}

	telemetryConfig := func() *corev1.ConfigMap {
		cm := monitoring.TelemetryConfigRules()
		cm.Data = map[string]string{"metrics.yaml": sampleTelemetryConfig}
		return cm
	}

	tests := []struct {
		name     string
		existing []client.Object
		validate func(*WithT, client.Client)
	}{
		{
			name:     "no monitoring namespace",
			validate: func(g *WithT, c client.Client) {},
		},
		{
			name:     "monitoring namespace exists",
			existing: []client.Object{monitoring.MonitoringNamespace()},
			validate: func(g *WithT, c client.Client) {
				monitoringConfig := monitoring.MonitoringConfig()
				err := c.Get(t.Context(), client.ObjectKeyFromObject(monitoringConfig), monitoringConfig)
				g.Expect(err).ToNot(HaveOccurred())
				validateMonitoringConfig(g, monitoringConfig)
			},
		},
		{
			name: "uwm exists",
			existing: []client.Object{
				monitoring.MonitoringNamespace(),
				monitoring.UWMNamespace(),
				clusterVersion(),
				telemeterClientSecret(),
				telemetryConfig(),
			},
			validate: func(g *WithT, c client.Client) {
				monitoringConfig := monitoring.MonitoringConfig()
				err := c.Get(t.Context(), client.ObjectKeyFromObject(monitoringConfig), monitoringConfig)
				g.Expect(err).ToNot(HaveOccurred())
				validateMonitoringConfig(g, monitoringConfig)
				uwmConfig := monitoring.UWMConfig()
				err = c.Get(t.Context(), client.ObjectKeyFromObject(uwmConfig), uwmConfig)
				g.Expect(err).ToNot(HaveOccurred())
				validateUWMConfig(g, uwmConfig)
				remoteWriteSecret := monitoring.UWMRemoteWriteSecret()
				err = c.Get(t.Context(), client.ObjectKeyFromObject(remoteWriteSecret), remoteWriteSecret)
				g.Expect(err).ToNot(HaveOccurred())
				validateRemoteWriteSecret(g, remoteWriteSecret)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			ns := "hypershift"
			deployment := manifests.OperatorDeployment(ns)
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(deployment).WithObjects(test.existing...).Build()
			reconciler := &Reconciler{
				Client:                 c,
				CreateOrUpdateProvider: upsert.New(true),
				errorHandler:           func(obj client.Object, err error) error { return err },
				Namespace:              ns,
			}
			req := ctrl.Request{}
			req.Name = "operator"
			req.Namespace = ns
			_, err := reconciler.Reconcile(t.Context(), req)
			g.Expect(err).ToNot(HaveOccurred())
			test.validate(g, c)
		})
	}
}
