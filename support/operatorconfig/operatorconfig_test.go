package operatorconfig

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	operatorv1 "github.com/openshift/api/operator/v1"
)

func TestFieldPaths(t *testing.T) {
	testCases := []struct {
		name     string
		config   *hyperv1.OperatorConfiguration
		expected []string
	}{
		{
			name:     "When the configuration is nil, it should report no settings",
			config:   nil,
			expected: nil,
		},
		{
			name:     "When the configuration is empty, it should report no settings",
			config:   &hyperv1.OperatorConfiguration{},
			expected: nil,
		},
		{
			name: "When a scalar setting is nested two levels deep, it should report its dotted path",
			config: &hyperv1.OperatorConfiguration{
				KubeAPIServer: hyperv1.KubeAPIServerOperatorSpec{
					ComponentLogLevelSpec: hyperv1.ComponentLogLevelSpec{LogLevel: hyperv1.Debug},
				},
			},
			expected: []string{"kubeAPIServer.logLevel"},
		},
		{
			name: "When several settings are set across operators, it should report every leaf sorted",
			config: &hyperv1.OperatorConfiguration{
				KubeAPIServer: hyperv1.KubeAPIServerOperatorSpec{
					ComponentLogLevelSpec: hyperv1.ComponentLogLevelSpec{LogLevel: hyperv1.Trace},
				},
				Etcd: hyperv1.EtcdOperatorSpec{
					ComponentLogLevelSpec: hyperv1.ComponentLogLevelSpec{LogLevel: hyperv1.Debug},
				},
				ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
					OVNKubernetesConfig: &hyperv1.OVNKubernetesConfig{
						IPv4: &hyperv1.OVNIPv4Config{
							InternalJoinSubnet:          "100.66.0.0/16",
							InternalTransitSwitchSubnet: "100.69.0.0/16",
						},
					},
				},
			},
			expected: []string{
				"clusterNetworkOperator.ovnKubernetesConfig.ipv4.internalJoinSubnet",
				"clusterNetworkOperator.ovnKubernetesConfig.ipv4.internalTransitSwitchSubnet",
				"etcd.logLevel",
				"kubeAPIServer.logLevel",
			},
		},
		{
			name: "When a setting is set to its zero value, it should not be reported as set",
			config: &hyperv1.OperatorConfiguration{
				ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
					OVNKubernetesConfig: &hyperv1.OVNKubernetesConfig{
						IPv4: &hyperv1.OVNIPv4Config{InternalJoinSubnet: "100.66.0.0/16"},
					},
				},
			},
			expected: []string{"clusterNetworkOperator.ovnKubernetesConfig.ipv4.internalJoinSubnet"},
		},
		{
			name: "When a setting is an embedded struct from another API group, it should report its leaves",
			config: &hyperv1.OperatorConfiguration{
				IngressOperator: &hyperv1.IngressOperatorSpec{
					EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.LoadBalancerServiceStrategyType,
						LoadBalancer: &operatorv1.LoadBalancerStrategy{
							Scope: operatorv1.InternalLoadBalancer,
						},
					},
				},
			},
			expected: []string{
				"ingressOperator.endpointPublishingStrategy.loadBalancer.scope",
				"ingressOperator.endpointPublishingStrategy.type",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			paths, err := FieldPaths(tc.config)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(paths).To(Equal(tc.expected))
		})
	}
}

// TestFieldPathsIsStableAcrossBuilds asserts the property the whole mechanism rests on:
// a build that does not know a setting drops it on decode, so re-encoding what survived
// yields exactly the settings that build understands. An older control plane operator is
// simulated by decoding a spec that carries an unknown setting into a struct without it.
func TestFieldPathsIsStableAcrossBuilds(t *testing.T) {
	g := NewWithT(t)

	// What a newer HyperShift operator mirrors onto the HostedControlPlane.
	newer := &hyperv1.OperatorConfiguration{
		KubeAPIServer: hyperv1.KubeAPIServerOperatorSpec{
			ComponentLogLevelSpec: hyperv1.ComponentLogLevelSpec{LogLevel: hyperv1.Debug},
		},
	}
	configured, err := FieldPaths(newer)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(configured).To(Equal([]string{"kubeAPIServer.logLevel"}))

	// The same spec as seen by a build whose types also know the setting.
	honored, err := FieldPaths(newer.DeepCopy())
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(Unhonored(configured, honored)).To(BeEmpty(), "a build that knows every setting should honor every setting")

	// The same spec as seen by a build that drops the setting.
	g.Expect(Unhonored(configured, nil)).To(Equal([]string{"kubeAPIServer.logLevel"}))
}

func TestUnhonored(t *testing.T) {
	testCases := []struct {
		name       string
		configured []string
		honored    []string
		expected   []string
	}{
		{
			name:       "When every configured setting is honored, it should report none",
			configured: []string{"etcd.logLevel", "kubeAPIServer.logLevel"},
			honored:    []string{"etcd.logLevel", "kubeAPIServer.logLevel"},
			expected:   nil,
		},
		{
			name:       "When a configured setting is missing from the honored list, it should report it",
			configured: []string{"etcd.logLevel", "kubeAPIServer.eventTTLMinutes"},
			honored:    []string{"etcd.logLevel"},
			expected:   []string{"kubeAPIServer.eventTTLMinutes"},
		},
		{
			name:       "When nothing is honored, it should report every configured setting",
			configured: []string{"etcd.logLevel", "kubeAPIServer.eventTTLMinutes"},
			honored:    nil,
			expected:   []string{"etcd.logLevel", "kubeAPIServer.eventTTLMinutes"},
		},
		{
			name:       "When the honored list has settings that are not configured, it should ignore them",
			configured: []string{"etcd.logLevel"},
			honored:    []string{"etcd.logLevel", "kubeScheduler.logLevel"},
			expected:   nil,
		},
		{
			name:       "When nothing is configured, it should report none",
			configured: nil,
			honored:    []string{"etcd.logLevel"},
			expected:   nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(Unhonored(tc.configured, tc.honored)).To(Equal(tc.expected))
		})
	}
}

func TestReportable(t *testing.T) {
	testCases := []struct {
		name     string
		count    int
		expected bool
	}{
		{
			name:     "When no settings are configured, it should be reportable",
			count:    0,
			expected: true,
		},
		{
			name:     "When exactly the maximum number of settings are configured, it should be reportable",
			count:    MaxHonoredFields,
			expected: true,
		},
		{
			name:     "When one more than the maximum number of settings are configured, it should not be reportable",
			count:    MaxHonoredFields + 1,
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(Reportable(make([]string, tc.count))).To(Equal(tc.expected))
		})
	}
}

func TestSupportsHonoredFieldsReporting(t *testing.T) {
	testCases := []struct {
		name              string
		version           string
		expectedSupported bool
		expectedKnown     bool
	}{
		{
			name:              "When the version is empty, it should report unknown",
			version:           "",
			expectedSupported: false,
			expectedKnown:     false,
		},
		{
			name:              "When the version is not semver, it should report unknown",
			version:           "not-a-version",
			expectedSupported: false,
			expectedKnown:     false,
		},
		{
			name:              "When the version predates reporting, it should report unsupported",
			version:           "5.0.3",
			expectedSupported: false,
			expectedKnown:     true,
		},
		{
			name:              "When the version is from a much older major, it should report unsupported",
			version:           "4.19.12",
			expectedSupported: false,
			expectedKnown:     true,
		},
		{
			name:              "When the version is the first supporting version, it should report supported",
			version:           "5.1.0",
			expectedSupported: true,
			expectedKnown:     true,
		},
		{
			name:              "When the version is a prerelease of the first supporting version, it should report supported",
			version:           "5.1.0-0.nightly-2026-10-20-123456",
			expectedSupported: true,
			expectedKnown:     true,
		},
		{
			name:              "When the version is a later z-stream, it should report supported",
			version:           "5.1.7",
			expectedSupported: true,
			expectedKnown:     true,
		},
		{
			name:              "When the version is a later major, it should report supported",
			version:           "6.0.0",
			expectedSupported: true,
			expectedKnown:     true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			supported, known := SupportsHonoredFieldsReporting(tc.version)
			g.Expect(supported).To(Equal(tc.expectedSupported))
			g.Expect(known).To(Equal(tc.expectedKnown))
		})
	}
}

func TestUnhonoredBefore(t *testing.T) {
	testCases := []struct {
		name       string
		configured []string
		version    string
		expected   []string
	}{
		{
			name:       "When a setting with a recorded floor runs on an older control plane, it should report it",
			configured: []string{"kubeAPIServer.eventTTLMinutes"},
			version:    "5.0.3",
			expected:   []string{"kubeAPIServer.eventTTLMinutes"},
		},
		{
			name:       "When a setting with a recorded floor runs on a supporting control plane, it should report none",
			configured: []string{"kubeAPIServer.eventTTLMinutes"},
			version:    "5.1.0",
			expected:   nil,
		},
		{
			name:       "When a setting has no recorded floor, it should report none",
			configured: []string{"etcd.logLevel"},
			version:    "4.19.12",
			expected:   nil,
		},
		{
			name:       "When the version is unparsable, it should report none",
			configured: []string{"kubeAPIServer.eventTTLMinutes"},
			version:    "",
			expected:   nil,
		},
		{
			name:       "When only some configured settings are below their floor, it should report just those",
			configured: []string{"etcd.logLevel", "kubeAPIServer.eventTTLMinutes"},
			version:    "5.0.3",
			expected:   []string{"kubeAPIServer.eventTTLMinutes"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(UnhonoredBefore(tc.configured, tc.version)).To(Equal(tc.expected))
		})
	}
}

// TestPreReportingFieldFloorsAreClosed guards the invariant that the per-setting floors
// are a closed, shrinking set. An entry is only meaningful for a setting that shipped no
// later than honoredFields reporting itself; a setting introduced after that is reported
// generically by every control plane operator that honors it.
func TestPreReportingFieldFloorsAreClosed(t *testing.T) {
	g := NewWithT(t)
	for path, floor := range preReportingFieldFloors {
		g.Expect(meetsFloor(honoredFieldsReportingMinVersion, floor)).To(BeTrue(),
			"setting %q was introduced after honoredFields reporting, so it is reported generically and must not have a floor entry", path)
	}
}
