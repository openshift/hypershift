package aws

import (
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestConfig(t *testing.T) {
	hcp := newTestHCP(map[string]string{
		hyperv1.SharedLoadBalancerHealthProbePathAnnotation: "/healthz",
		hyperv1.SharedLoadBalancerHealthProbePortAnnotation: "10256",
	})
	hcp.Namespace = "HCP_NAMESPACE"

	cm := &corev1.ConfigMap{}
	_, _, err := assets.LoadManifestInto(ComponentName, "config.yaml", cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cpContext := component.WorkloadContext{
		HCP: hcp,
	}
	err = adaptConfig(cpContext, cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	yaml, err := util.SerializeResource(cm, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, yaml)
}

// newTestHCP creates a HostedControlPlane with default AWS configuration for testing.
// Custom annotations can be provided to override defaults.
func newTestHCP(annotations map[string]string) *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "test-namespace",
			Annotations: annotations,
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
						VPC:    "my-vpc",
						Subnet: &hyperv1.AWSResourceReference{ID: ptr.To("my-subnet-ID")},
						Zone:   "my-zone",
					},
				},
			},
			InfraID: "my-infra-ID",
		},
	}
}

func TestConfigWithTechPreviewNoUpgrade(t *testing.T) {
	hcp := newTestHCP(map[string]string{
		hyperv1.SharedLoadBalancerHealthProbePathAnnotation: "/healthz",
		hyperv1.SharedLoadBalancerHealthProbePortAnnotation: "10256",
	})
	hcp.Namespace = "HCP_NAMESPACE"
	hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
		FeatureGate: &configv1.FeatureGateSpec{
			FeatureGateSelection: configv1.FeatureGateSelection{
				FeatureSet: configv1.CustomNoUpgrade,
				CustomNoUpgrade: &configv1.CustomFeatureGates{
					Enabled: []configv1.FeatureGateName{
						"AWSServiceLBNetworkSecurityGroup",
					},
				},
			},
		},
	}

	cm := &corev1.ConfigMap{}
	_, _, err := assets.LoadManifestInto(ComponentName, "config.yaml", cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cpContext := component.WorkloadContext{
		HCP: hcp,
	}
	err = adaptConfig(cpContext, cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify NLBSecurityGroupMode is present in the config
	config := cm.Data[configKey]
	if !strings.Contains(config, "NLBSecurityGroupMode = Managed") {
		t.Errorf("expected config to contain 'NLBSecurityGroupMode = Managed', got: %s", config)
	}

	yaml, err := util.SerializeResource(cm, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, yaml)
}

func TestConfigWithTechPreviewNoUpgradeNoDuplicates(t *testing.T) {
	hcp := newTestHCP(map[string]string{})
	hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
		FeatureGate: &configv1.FeatureGateSpec{
			FeatureGateSelection: configv1.FeatureGateSelection{
				FeatureSet: configv1.CustomNoUpgrade,
				CustomNoUpgrade: &configv1.CustomFeatureGates{
					Enabled: []configv1.FeatureGateName{
						"AWSServiceLBNetworkSecurityGroup",
					},
				},
			},
		},
	}

	cm := &corev1.ConfigMap{}
	_, _, err := assets.LoadManifestInto(ComponentName, "config.yaml", cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Manually add NLBSecurityGroupMode to the template to simulate duplicate scenario
	cm.Data[configKey] += "\nNLBSecurityGroupMode = Managed"

	cpContext := component.WorkloadContext{
		HCP: hcp,
	}
	err = adaptConfig(cpContext, cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify NLBSecurityGroupMode appears only once in the config
	config := cm.Data[configKey]
	count := strings.Count(config, "NLBSecurityGroupMode")
	if count != 1 {
		t.Errorf("expected NLBSecurityGroupMode to appear exactly once, but found %d occurrences in config:\n%s", count, config)
	}
}

func TestConfigErrorStates(t *testing.T) {
	tests := []struct {
		name        string
		hcp         *hyperv1.HostedControlPlane
		expectedErr string
	}{
		{
			name: "invalid load balancer health probe mode",
			hcp: newTestHCP(map[string]string{
				hyperv1.AWSLoadBalancerHealthProbeModeAnnotation: "invalid-mode",
			}),
			expectedErr: "invalid value for annotation hypershift.openshift.io/aws-load-balancer-health-probe-mode: invalid-mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := &corev1.ConfigMap{}
			_, _, err := assets.LoadManifestInto(ComponentName, "config.yaml", cm)
			if err != nil {
				t.Fatalf("failed to load manifest: %v", err)
			}
			cpContext := component.WorkloadContext{
				HCP: tt.hcp,
			}
			err = adaptConfig(cpContext, cm)
			if err == nil {
				t.Fatalf("expected error but got none")
			}
			if tt.expectedErr != "" && !strings.Contains(err.Error(), tt.expectedErr) {
				t.Fatalf("expected error to contain %q, but got: %v", tt.expectedErr, err)
			}
		})
	}
}

func TestSetNlbSecurityGroupMode(t *testing.T) {
	tests := []struct {
		name           string
		hcp            *hyperv1.HostedControlPlane
		baseConfig     *string
		expectedConfig string
		expectError    bool
		errorContains  string
	}{
		{
			name:          "nil baseConfig should return error",
			hcp:           newTestHCP(map[string]string{}),
			baseConfig:    nil,
			expectError:   true,
			errorContains: "baseConfig is required",
		},
		{
			name:           "no feature gate configuration should not add NLBSecurityGroupMode",
			hcp:            newTestHCP(map[string]string{}),
			baseConfig:     ptr.To("Zone = my-zone"),
			expectedConfig: "Zone = my-zone",
			expectError:    false,
		},
		{
			name: "non-AWS platform should not add NLBSecurityGroupMode even with feature gate enabled",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := newTestHCP(map[string]string{})
				hcp.Spec.Platform.Type = hyperv1.GCPPlatform
				hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
					FeatureGate: &configv1.FeatureGateSpec{
						FeatureGateSelection: configv1.FeatureGateSelection{
							FeatureSet: configv1.TechPreviewNoUpgrade,
						},
					},
				}
				return hcp
			}(),
			baseConfig:     ptr.To("Zone = my-zone"),
			expectedConfig: "Zone = my-zone",
			expectError:    false,
		},
		{
			name: "TechPreviewNoUpgrade feature set should add NLBSecurityGroupMode",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := newTestHCP(map[string]string{})
				hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
					FeatureGate: &configv1.FeatureGateSpec{
						FeatureGateSelection: configv1.FeatureGateSelection{
							FeatureSet: configv1.TechPreviewNoUpgrade,
						},
					},
				}
				return hcp
			}(),
			baseConfig:     ptr.To("Zone = my-zone"),
			expectedConfig: "Zone = my-zone\nNLBSecurityGroupMode = Managed",
			expectError:    false,
		},
		{
			name: "Default feature set should not add NLBSecurityGroupMode",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := newTestHCP(map[string]string{})
				hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
					FeatureGate: &configv1.FeatureGateSpec{
						FeatureGateSelection: configv1.FeatureGateSelection{
							FeatureSet: configv1.Default,
						},
					},
				}
				return hcp
			}(),
			baseConfig:     ptr.To("Zone = my-zone"),
			expectedConfig: "Zone = my-zone",
			expectError:    false,
		},
		{
			name: "CustomNoUpgrade with explicit gate enabled should add NLBSecurityGroupMode",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := newTestHCP(map[string]string{})
				hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
					FeatureGate: &configv1.FeatureGateSpec{
						FeatureGateSelection: configv1.FeatureGateSelection{
							FeatureSet: configv1.CustomNoUpgrade,
							CustomNoUpgrade: &configv1.CustomFeatureGates{
								Enabled: []configv1.FeatureGateName{
									"AWSServiceLBNetworkSecurityGroup",
								},
							},
						},
					},
				}
				return hcp
			}(),
			baseConfig:     ptr.To("Zone = my-zone"),
			expectedConfig: "Zone = my-zone\nNLBSecurityGroupMode = Managed",
			expectError:    false,
		},
		{
			name: "CustomNoUpgrade with explicit gate disabled should not add NLBSecurityGroupMode",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := newTestHCP(map[string]string{})
				hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
					FeatureGate: &configv1.FeatureGateSpec{
						FeatureGateSelection: configv1.FeatureGateSelection{
							FeatureSet: configv1.CustomNoUpgrade,
							CustomNoUpgrade: &configv1.CustomFeatureGates{
								Disabled: []configv1.FeatureGateName{
									"AWSServiceLBNetworkSecurityGroup",
								},
							},
						},
					},
				}
				return hcp
			}(),
			baseConfig:     ptr.To("Zone = my-zone"),
			expectedConfig: "Zone = my-zone",
			expectError:    false,
		},
		{
			name: "CustomNoUpgrade with gate in neither enabled nor disabled should not add NLBSecurityGroupMode",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := newTestHCP(map[string]string{})
				hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
					FeatureGate: &configv1.FeatureGateSpec{
						FeatureGateSelection: configv1.FeatureGateSelection{
							FeatureSet: configv1.CustomNoUpgrade,
							CustomNoUpgrade: &configv1.CustomFeatureGates{
								Enabled: []configv1.FeatureGateName{
									"SomeOtherFeature",
								},
							},
						},
					},
				}
				return hcp
			}(),
			baseConfig:     ptr.To("Zone = my-zone"),
			expectedConfig: "Zone = my-zone",
			expectError:    false,
		},
		{
			name: "already contains NLBSecurityGroupMode should not duplicate",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := newTestHCP(map[string]string{})
				hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
					FeatureGate: &configv1.FeatureGateSpec{
						FeatureGateSelection: configv1.FeatureGateSelection{
							FeatureSet: configv1.TechPreviewNoUpgrade,
						},
					},
				}
				return hcp
			}(),
			baseConfig:     ptr.To("Zone = my-zone\nNLBSecurityGroupMode = Managed"),
			expectedConfig: "Zone = my-zone\nNLBSecurityGroupMode = Managed",
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpContext := component.WorkloadContext{
				HCP: tt.hcp,
			}

			err := setNlbSecurityGroupMode(cpContext, tt.baseConfig)

			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error but got none")
				}
				if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Fatalf("expected error to contain %q, but got: %v", tt.errorContains, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.baseConfig != nil && *tt.baseConfig != tt.expectedConfig {
				t.Errorf("expected baseConfig to be %q, got %q", tt.expectedConfig, *tt.baseConfig)
			}
		})
	}
}

func TestIsFeatureGateEnabled(t *testing.T) {
	tests := []struct {
		name     string
		spec     *configv1.FeatureGateSpec
		expected bool
		wantErr  bool
	}{
		{
			name:     "nil spec should return false",
			spec:     nil,
			expected: false,
			wantErr:  false,
		},
		{
			name: "TechPreviewNoUpgrade should return true",
			spec: &configv1.FeatureGateSpec{
				FeatureGateSelection: configv1.FeatureGateSelection{
					FeatureSet: configv1.TechPreviewNoUpgrade,
				},
			},
			expected: true,
			wantErr:  false,
		},
		{
			name: "Default should return false",
			spec: &configv1.FeatureGateSpec{
				FeatureGateSelection: configv1.FeatureGateSelection{
					FeatureSet: configv1.Default,
				},
			},
			expected: false,
			wantErr:  false,
		},
		{
			name: "CustomNoUpgrade with nil CustomNoUpgrade should return false",
			spec: &configv1.FeatureGateSpec{
				FeatureGateSelection: configv1.FeatureGateSelection{
					FeatureSet:      configv1.CustomNoUpgrade,
					CustomNoUpgrade: nil,
				},
			},
			expected: false,
			wantErr:  false,
		},
		{
			name: "CustomNoUpgrade with gate explicitly enabled should return true",
			spec: &configv1.FeatureGateSpec{
				FeatureGateSelection: configv1.FeatureGateSelection{
					FeatureSet: configv1.CustomNoUpgrade,
					CustomNoUpgrade: &configv1.CustomFeatureGates{
						Enabled: []configv1.FeatureGateName{
							"AWSServiceLBNetworkSecurityGroup",
						},
					},
				},
			},
			expected: true,
			wantErr:  false,
		},
		{
			name: "CustomNoUpgrade with gate explicitly disabled should return false",
			spec: &configv1.FeatureGateSpec{
				FeatureGateSelection: configv1.FeatureGateSelection{
					FeatureSet: configv1.CustomNoUpgrade,
					CustomNoUpgrade: &configv1.CustomFeatureGates{
						Disabled: []configv1.FeatureGateName{
							"AWSServiceLBNetworkSecurityGroup",
						},
					},
				},
			},
			expected: false,
			wantErr:  false,
		},
		{
			name: "CustomNoUpgrade with gate in neither list should return false",
			spec: &configv1.FeatureGateSpec{
				FeatureGateSelection: configv1.FeatureGateSelection{
					FeatureSet: configv1.CustomNoUpgrade,
					CustomNoUpgrade: &configv1.CustomFeatureGates{
						Enabled: []configv1.FeatureGateName{
							"SomeOtherFeature",
						},
					},
				},
			},
			expected: false,
			wantErr:  false,
		},
		{
			name: "CustomNoUpgrade with both enabled and disabled should prefer disabled",
			spec: &configv1.FeatureGateSpec{
				FeatureGateSelection: configv1.FeatureGateSelection{
					FeatureSet: configv1.CustomNoUpgrade,
					CustomNoUpgrade: &configv1.CustomFeatureGates{
						Enabled: []configv1.FeatureGateName{
							"AWSServiceLBNetworkSecurityGroup",
						},
						Disabled: []configv1.FeatureGateName{
							"AWSServiceLBNetworkSecurityGroup",
						},
					},
				},
			},
			expected: false,
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := isFeatureGateEnabled(tt.spec)
			if (err != nil) != tt.wantErr {
				t.Errorf("isFeatureGateEnabled() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.expected {
				t.Errorf("isFeatureGateEnabled() = %v, expected %v", got, tt.expected)
			}
		})
	}
}
