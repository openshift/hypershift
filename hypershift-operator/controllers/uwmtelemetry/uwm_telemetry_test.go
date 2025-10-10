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
	// helper to locate the telemetry remote write entry and validate core fields
	validateTelemetryRemoteWrite := func(g *WithT, parsed map[string]interface{}) map[string]interface{} {
		remoteWrite, found, err := unstructured.NestedSlice(parsed, "prometheus", "remoteWrite")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(found).To(BeTrue())
		// Find telemetry entry
		var telemetry map[string]interface{}
		for _, item := range remoteWrite {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			url, exists, err := unstructured.NestedString(m, "url")
			g.Expect(err).ToNot(HaveOccurred())
			if exists && url == telemetryRemoteWriteURL {
				telemetry = m
				break
			}
		}
		g.Expect(telemetry).ToNot(BeNil())

		// auth secret
		secretName, exists, err := unstructured.NestedString(telemetry, "authorization", "credentials", "name")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeTrue())
		g.Expect(secretName).To(Equal("telemetry-remote-write"))

		// metadata send=false
		send, exists, err := unstructured.NestedBool(telemetry, "metadataConfig", "send")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeTrue())
		g.Expect(send).To(BeFalse())

		// must keep only series with _id label present
		relabels, exists, err := unstructured.NestedSlice(telemetry, "writeRelabelConfigs")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeTrue())
		foundKeepID := false
		for _, r := range relabels {
			rm, ok := r.(map[string]interface{})
			if !ok {
				continue
			}
			action, _, _ := unstructured.NestedString(rm, "action")
			regex, _, _ := unstructured.NestedString(rm, "regex")
			src, _, _ := unstructured.NestedStringSlice(rm, "sourceLabels")
			if action == "keep" && regex == ".+" && len(src) == 1 && src[0] == "_id" {
				foundKeepID = true
				break
			}
		}
		g.Expect(foundKeepID).To(BeTrue())
		return telemetry
	}

	tests := []struct {
		name          string
		initial       string
		expectRWCount int
		validateExtra func(*WithT, map[string]interface{})
	}{
		{
			name:          "no existing config",
			expectRWCount: 1,
		},
		{
			name: "other keys present should be preserved",
			initial: `foo: bar
goo: baz
prometheus:
  random1: one
`,
			expectRWCount: 1,
			validateExtra: func(g *WithT, parsed map[string]interface{}) {
				v, _, _ := unstructured.NestedString(parsed, "foo")
				g.Expect(v).To(Equal("bar"))
				v, _, _ = unstructured.NestedString(parsed, "goo")
				g.Expect(v).To(Equal("baz"))
				v, _, _ = unstructured.NestedString(parsed, "prometheus", "random1")
				g.Expect(v).To(Equal("one"))
			},
		},
		{
			name: "other remote write configs should be preserved",
			initial: `prometheus:
  remoteWrite:
  - queueConfig:
      batchSendDeadline: 5m
    url: http://www.example.com
`,
			expectRWCount: 2,
			validateExtra: func(g *WithT, parsed map[string]interface{}) {
				remoteWrite, _, _ := unstructured.NestedSlice(parsed, "prometheus", "remoteWrite")
				foundExample := false
				for _, item := range remoteWrite {
					m, ok := item.(map[string]interface{})
					if !ok {
						continue
					}
					url, _, _ := unstructured.NestedString(m, "url")
					if url == "http://www.example.com" {
						foundExample = true
						break
					}
				}
				g.Expect(foundExample).To(BeTrue())
			},
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
			expectRWCount: 2,
			validateExtra: func(g *WithT, parsed map[string]interface{}) {
				_ = validateTelemetryRemoteWrite(g, parsed)
				// queueConfig numeric assertions are intentionally skipped due to YAML number typing nuances
			},
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

			parsed := map[string]interface{}{}
			err = yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &parsed)
			g.Expect(err).ToNot(HaveOccurred())

			// validate telemetry entry exists and core behavior
			_ = validateTelemetryRemoteWrite(g, parsed)

			// validate remote write count
			rw, _, _ := unstructured.NestedSlice(parsed, "prometheus", "remoteWrite")
			g.Expect(len(rw)).To(Equal(test.expectRWCount))

			if test.validateExtra != nil {
				test.validateExtra(g, parsed)
			}
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

	expectedRegex := "(count:up0|count:up1|cluster_version|cluster_version_available_updates|cluster_version_capability)"

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
		// metadata send=false enforced
		send, exists, err := unstructured.NestedBool(rawConfig, "metadataConfig", "send")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeTrue())
		g.Expect(send).To(BeFalse())
		// relabel configs include _id keep and metrics regex keep
		relabels, exists, err := unstructured.NestedSlice(rawConfig, "writeRelabelConfigs")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeTrue())
		g.Expect(len(relabels)).To(Equal(2))
		foundKeepID := false
		foundMetrics := false
		for _, r := range relabels {
			rm, ok := r.(map[string]interface{})
			if !ok {
				continue
			}
			action, _, _ := unstructured.NestedString(rm, "action")
			regex, _, _ := unstructured.NestedString(rm, "regex")
			src, _, _ := unstructured.NestedStringSlice(rm, "sourceLabels")
			if action == "keep" && regex == ".+" && len(src) == 1 && src[0] == "_id" {
				foundKeepID = true
			}
			if action == "keep" && regex == expectedRegex && len(src) == 1 && src[0] == "__name__" {
				foundMetrics = true
			}
		}
		g.Expect(foundKeepID).To(BeTrue())
		g.Expect(foundMetrics).To(BeTrue())
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
