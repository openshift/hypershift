package uwmtelemetry

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
)

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
			cm := &corev1.ConfigMap{}
			if len(test.initial) > 0 {
				cm.Data = map[string]string{
					"config.yaml": test.initial,
				}
			}
			reconcileUWMConfigContent(cm, nil)
			if actual := cm.Data["config.yaml"]; test.expected != actual {
				t.Errorf("actual different than expected: %s", cmp.Diff(test.expected, actual))
			}
		})
	}
}

func TestTelemetryConfigToRelabelConfig(t *testing.T) {
	const inputConfig = `
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

	expectedRegex := "(count:up0|count:up1|cluster_version|cluster_version_available_updates|cluster_version_capability)"

	inputCM := &corev1.ConfigMap{
		Data: map[string]string{
			"metrics.yaml": inputConfig,
		},
	}
	outputConfig, err := telemetryConfigToRelabelConfig(inputCM)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outputConfig == nil {
		t.Fatalf("output config is nil")
	}
	if outputConfig.Regex != expectedRegex {
		t.Errorf("actual: %s, expected: %s", outputConfig.Regex, expectedRegex)
	}

}
