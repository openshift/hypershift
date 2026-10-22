//go:build e2ev2

package lifecycle

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/onsi/ginkgo/v2/types"
)

type awsRootLaneExpectation struct {
	lane           string
	variant        string
	names          []string
	featureFilters []string
	private        bool
}

func TestAWSPlatformConfigTestMatrixRootSelection(t *testing.T) {
	plan := (&AWSPlatformConfig{}).DefaultTestPlan()
	for _, expected := range []awsRootLaneExpectation{
		{
			lane: "managed-dns", variant: "managed-dns",
			names:          []string{"managed-dns-configured-workers", "managed-dns-common-before", "managed-dns", "managed-dns-common-after"},
			featureFilters: []string{"hosted-cluster-managed-dns"},
		},
		{
			lane: "karpenter", variant: "karpenter",
			names:          []string{"karpenter-configured-workers", "karpenter-common-before", "karpenter", "karpenter-common-after"},
			featureFilters: []string{"karpenter"},
		},
		{
			lane: "karpenter-upgrade", variant: "karpenter-upgrade",
			names:          []string{"karpenter-upgrade-configured-workers", "karpenter-upgrade-common-before", "karpenter-upgrade", "karpenter-upgrade-common-after"},
			featureFilters: []string{"karpenter-upgrade"},
		},
		{
			lane: "upgrade-and-chaos", variant: "upgrade",
			names:          []string{"upgrade-configured-workers", "upgrade-common-before", "upgrade", "post-upgrade-health", "control-plane-tls", "etcd-chaos", "upgrade-common-after"},
			featureFilters: []string{"control-plane-upgrade", "hosted-cluster-health || control-plane-workloads", "control-plane-pki-operator", "etcd-chaos"},
		},
	} {
		t.Run("When the "+expected.lane+" root runs, it should preserve ordered features between initial and current-state checks", func(t *testing.T) {
			assertAWSRootLane(t, &plan, expected)
		})
	}
}

func TestAWSPlatformConfigTestMatrixOptInRootSelection(t *testing.T) {
	plan := readAWSRootOptInPlan(t)
	for _, expected := range []awsRootLaneExpectation{
		{
			lane: "aws-private-endpoint", variant: "private", private: true,
			names:          []string{"aws-private-configured-workers", "aws-private-common-before", "aws-private-transition", "aws-private-common-after"},
			featureFilters: []string{"aws-private-endpoint-transition"},
		},
		{
			lane: "aws-private-route-endpoint", variant: "private-route", private: true,
			names:          []string{"aws-private-route-configured-workers", "aws-private-route-common-before", "aws-private-route-transition", "aws-private-route-common-after"},
			featureFilters: []string{"aws-private-endpoint-transition"},
		},
		{
			lane: "aws-upgrade-without-pki-reconciliation", variant: "upgrade-pki-disabled",
			names:          []string{"aws-upgrade-pki-disabled-configured-workers", "aws-upgrade-pki-disabled-common-before", "aws-upgrade-pki-disabled", "aws-upgrade-pki-disabled-health", "aws-upgrade-pki-disabled-common-after"},
			featureFilters: []string{"control-plane-upgrade", "hosted-cluster-health || control-plane-workloads"},
		},
		{
			lane: "aws-autonode-upgrade-without-pki-reconciliation", variant: "karpenter-upgrade-pki-disabled",
			names:          []string{"aws-autonode-upgrade-pki-disabled-configured-workers", "aws-autonode-upgrade-pki-disabled-common-before", "aws-autonode-upgrade-pki-disabled", "aws-autonode-upgrade-pki-disabled-health", "aws-autonode-upgrade-pki-disabled-common-after"},
			featureFilters: []string{"karpenter-upgrade", "hosted-cluster-health-management || control-plane-workloads || hosted-cluster-compliance"},
		},
	} {
		t.Run("When the "+expected.lane+" opt-in runs, it should retain its fixture and applicable ordered checks", func(t *testing.T) {
			assertAWSRootLane(t, plan, expected)
		})
	}
}

func TestAWSPlatformConfigTestMatrixRemainingBeforeSelection(t *testing.T) {
	plan := (&AWSPlatformConfig{}).DefaultTestPlan()
	for _, expected := range []awsRootLaneExpectation{
		{
			lane: "public", variant: "public",
			names: []string{"public-configured-workers", "public-common-before", "public", "public-nodepool-rollouts", "public-native-lifecycle", "public-common-after"},
			featureFilters: []string{
				"hosted-cluster-aws || hosted-cluster-compliance || hosted-cluster-node-communication || " +
					"hosted-cluster-cpo || nodepool-arm64 || secret-encryption || control-plane-workloads || " +
					"hosted-cluster-security || nodepool-osimagestream || hosted-cluster-ingress || hosted-cluster-dns || " +
					"hosted-cluster-health || hosted-cluster-metrics || hosted-cluster-image-registry || hosted-cluster-api-validation || aws-control-plane-customization",
				"nodepool-vm-size-rollout || nodepool-replace-version-upgrade || nodepool-inplace-version-upgrade || " +
					"nodepool-n1-release || nodepool-n2-release || nodepool-auto-repair || nodepool-osimagestream-upgrade || " +
					"nodepool-nto-replace-rollout || nodepool-nto-inplace-rollout || nodepool-performance-profile || nodepool-mirror-config",
				"hosted-cluster-break-glass || hosted-cluster-oauth || hosted-cluster-network-configuration || " +
					"nodepool-aws-root-volume || nodepool-aws-day2-tags || hosted-cluster-metrics-lifecycle",
			},
		},
		{
			lane: "autoscaling", variant: "autoscaling",
			names:          []string{"autoscaling-configured-workers", "autoscaling-common-before", "autoscaling-nodepool-machineconfig", "autoscaling-balancing", "autoscaling-common-after"},
			featureFilters: []string{"nodepool-machineconfig-rollout", "nodepool-autoscaling-balancing || nodepool-autoscaling-scale-from-zero || nodepool-autoscaling-pause"},
		},
		{
			lane: "external-oidc", variant: "external-oidc",
			names:          []string{"external-oidc-configured-workers", "external-oidc-common-before", "external-oidc", "external-oidc-autoscaling", "external-oidc-trust-bundle", "external-oidc-common-after"},
			featureFilters: []string{"external-oidc || global-pull-secret", "nodepool-autoscaling-scale-up-down", "nodepool-trust-bundle"},
		},
	} {
		t.Run("When the "+expected.lane+" root starts, it should validate common prerequisites before any retained feature child", func(t *testing.T) {
			assertAWSRootLane(t, &plan, expected)
		})
	}
}

func TestAWSPlatformConfigTestMatrixRemainingOptInRootSelection(t *testing.T) {
	plan := readAWSRootOptInPlan(t)
	for _, expected := range []awsRootLaneExpectation{
		{
			lane: "aws-guest-olm", variant: "guest-olm",
			names:          []string{"aws-guest-olm-configured-workers", "aws-guest-olm-common-before", "aws-guest-olm-admission", "aws-guest-olm-common-after"},
			featureFilters: []string{"hosted-cluster-health || control-plane-workloads || hosted-cluster-compliance || hosted-cluster-security"},
		},
		{
			lane: "aws-proxy", variant: "proxy",
			names:          []string{"aws-proxy-configured-workers", "aws-proxy-common-before", "aws-proxy-common", "aws-proxy-common-after"},
			featureFilters: []string{"hosted-cluster-health || control-plane-workloads || hosted-cluster-compliance || hosted-cluster-security || hosted-cluster-ingress || hosted-cluster-node-communication || hosted-cluster-metrics || aws-control-plane-customization"},
		},
	} {
		t.Run("When the "+expected.lane+" opt-in starts, it should retain the original feature between applicable common checks", func(t *testing.T) {
			assertAWSRootLane(t, plan, expected)
		})
	}
}

func TestAWSPlatformConfigTestMatrixRestrictedOptInRootSelection(t *testing.T) {
	plan := readAWSRootOptInPlan(t)
	ciliumCommon := "hosted-cluster-health || control-plane-workloads || hosted-cluster-compliance || hosted-cluster-security || hosted-cluster-node-communication || aws-control-plane-customization"
	customCommon := "hosted-cluster-health || control-plane-workloads || hosted-cluster-compliance || hosted-cluster-security || hosted-cluster-node-communication || hosted-cluster-metrics || aws-control-plane-customization"
	managementCommon := "hosted-cluster-health-management || control-plane-workloads || routes || hosted-cluster-network-policies || aws-control-plane-customization"
	nodePoolCommon := "hosted-cluster-health-management || control-plane-workloads || hosted-cluster-webhook-validation || hosted-cluster-authentication-status"
	for _, expected := range []struct {
		lane    string
		variant string
		names   []string
		filters []string
	}{
		{
			lane: "aws-cilium", variant: "cilium",
			names: []string{"aws-cilium-configured-workers", "aws-cilium-common-before", "aws-cilium-common", "aws-cilium-common-after"},
			filters: []string{
				"hosted-cluster-health-configured-workers",
				"(" + ciliumCommon + ") && !hosted-cluster-health-configured-workers && !hosted-cluster-ingress",
				ciliumCommon,
				"(" + ciliumCommon + " || hosted-cluster-operator-metrics || hosted-cluster-node-tuning-metrics) && !hosted-cluster-health-configured-workers",
			},
		},
		{
			lane: "aws-custom-config", variant: "custom-config",
			names: []string{"aws-custom-config-configured-workers", "aws-custom-config-common-before", "aws-custom-config-features", "aws-custom-config-common-after"},
			filters: []string{
				"hosted-cluster-health-configured-workers",
				"(" + customCommon + ") && !hosted-cluster-health-configured-workers && !hosted-cluster-ingress",
				"hosted-cluster-capabilities || secret-encryption || hosted-cluster-image-registry || hosted-cluster-custom-config || hosted-cluster-network-configuration || hosted-cluster-oauth || hosted-cluster-ingress",
				customCommon,
			},
		},
		{
			lane: "aws-break-glass-ha", variant: "break-glass-ha",
			names:   []string{"aws-break-glass-ha-configured-workers", "aws-break-glass-ha-common-before", "aws-break-glass-ha-authentication", "aws-break-glass-ha-common-after"},
			filters: []string{"hosted-cluster-health-configured-workers", managementCommon, "hosted-cluster-break-glass-ha", managementCommon + " || hosted-cluster-operator-metrics || hosted-cluster-node-tuning-metrics || hosted-cluster-admission-policies"},
		},
		{
			lane: "aws-nodepool-dispatch", variant: "nodepool-dispatch",
			names:   []string{"aws-nodepool-dispatch-configured-workers", "aws-nodepool-dispatch-before", "aws-nodepool-dispatch-rollouts", "aws-nodepool-dispatch-after"},
			filters: []string{"hosted-cluster-health-configured-workers", nodePoolCommon, "nodepool-machineconfig-rollout || nodepool-nto-replace-rollout || nodepool-nto-inplace-rollout || nodepool-mirror-config", nodePoolCommon + " || hosted-cluster-operator-metrics || hosted-cluster-network-policies || hosted-cluster-node-tuning-metrics || routes || hosted-cluster-admission-policies"},
		},
		{
			lane: "aws-dedicated-trust-bundle", variant: "trust-bundle",
			names:   []string{"aws-trust-bundle-configured-workers", "aws-trust-bundle-before", "aws-trust-bundle-mutation", "aws-trust-bundle-after"},
			filters: []string{"hosted-cluster-health-configured-workers", nodePoolCommon, "nodepool-trust-bundle", nodePoolCommon + " || hosted-cluster-operator-metrics || hosted-cluster-network-policies || hosted-cluster-node-tuning-metrics || routes || hosted-cluster-admission-policies"},
		},
	} {
		t.Run("When the "+expected.lane+" restricted root runs, it should keep initial intent separate from applicable common and original feature checks", func(t *testing.T) {
			laneIndex := slices.IndexFunc(plan.TestMatrix.Sequential, func(lane SequentialGroup) bool { return lane.Name == expected.lane })
			if laneIndex < 0 {
				t.Fatalf("missing sequential lane %s", expected.lane)
			}
			steps := plan.TestMatrix.Sequential[laneIndex].Steps
			if len(steps) != len(expected.names) {
				t.Fatalf("%s has %d children, expected %d", expected.lane, len(steps), len(expected.names))
			}
			for index, step := range steps {
				if step.Name != expected.names[index] || step.Variant != expected.variant || step.LabelFilter != expected.filters[index] || step.Skip != "" {
					t.Fatalf("%s changed child %d identity, applicability or selection: %#v", expected.lane, index, step)
				}
			}
			assertAWSRestrictedCommonSelection(t, steps[1], expected.variant)
			assertAWSRestrictedCommonSelection(t, steps[len(steps)-1], expected.variant)
		})
	}
}

func TestAWSPlatformConfigTestMatrixDispatcherNetworkPolicySelection(t *testing.T) {
	plan := readAWSRootOptInPlan(t)
	laneIndex := slices.IndexFunc(plan.TestMatrix.Sequential, func(lane SequentialGroup) bool { return lane.Name == "aws-nodepool-dispatch" })
	if laneIndex < 0 {
		t.Fatal("missing NodePool dispatcher lane")
	}
	steps := plan.TestMatrix.Sequential[laneIndex].Steps
	for index, step := range steps {
		t.Run("When the dispatcher runs "+step.Name+", it should select management network policy checks only after its feature", func(t *testing.T) {
			filter, err := types.ParseLabelFilter(step.LabelFilter)
			if err != nil {
				t.Fatal(err)
			}
			if filter([]string{"hosted-cluster-security", "hosted-cluster-network-policies"}) != (index == len(steps)-1) {
				t.Fatalf("%s has incorrect inherited network-policy selection", step.Name)
			}
			for _, labels := range [][]string{
				{"hosted-cluster-security"},
				{"hosted-cluster-security", "Informing"},
				{"hosted-cluster-health", "hosted-cluster-ingress"},
			} {
				if filter(labels) {
					t.Fatalf("%s broadens dispatcher to unrelated guest group %v", step.Name, labels)
				}
			}
		})
	}
}

func TestAWSPlatformConfigTestMatrixWorkerDependentSelection(t *testing.T) {
	plan := readAWSRootOptInPlan(t)
	for _, lane := range plan.TestMatrix.Sequential {
		if len(lane.Steps) == 0 || (lane.Steps[0].Variant != "nodepool-dispatch" && lane.Steps[0].Variant != "trust-bundle") {
			continue
		}
		for _, step := range lane.Steps {
			t.Run("When zero-worker root runs "+step.Name+", it should not select OSImageStream leaves that require existing workers", func(t *testing.T) {
				filter, err := types.ParseLabelFilter(step.LabelFilter)
				if err != nil {
					t.Fatal(err)
				}
				for _, labels := range [][]string{{"nodepool-osimagestream"}, {"nodepool-osimagestream", "Informing"}, {"lifecycle", "nodepool-osimagestream"}, {"lifecycle", "nodepool-osimagestream", "nodepool-osimagestream-upgrade"}} {
					if filter(labels) {
						t.Fatalf("zero-worker step %s selected positive-worker OSImageStream leaf %v", step.Name, labels)
					}
				}
			})
		}
	}
	t.Run("When the default public root runs, it should preserve existing positive-worker OSImageStream coverage", func(t *testing.T) {
		plan := (&AWSPlatformConfig{}).DefaultTestPlan()
		selected := map[string]bool{}
		for _, lane := range plan.TestMatrix.Sequential {
			if lane.Name != "public" {
				continue
			}
			for _, step := range lane.Steps {
				filter, err := types.ParseLabelFilter(step.LabelFilter)
				if err != nil {
					t.Fatal(err)
				}
				selected[step.Name] = filter([]string{"nodepool-osimagestream"})
				if step.Name == "public-nodepool-rollouts" && !filter([]string{"lifecycle", "nodepool-osimagestream", "nodepool-osimagestream-upgrade"}) {
					t.Fatal("public OSImageStream rollout coverage lost")
				}
			}
		}
		if !selected["public"] {
			t.Fatal("public OSImageStream status/node OS/no-rollout leaves are not selected")
		}
	})
}

func TestAWSPlatformConfigTestMatrixRestrictedOperatorMetricsSelection(t *testing.T) {
	plan := readAWSRootOptInPlan(t)
	for _, variant := range []string{"private", "private-route", "break-glass-ha", "nodepool-dispatch", "trust-bundle", "cilium"} {
		t.Run("When the "+variant+" restricted root succeeds, it should select management operator and NTO metrics after its feature", func(t *testing.T) {
			matched := false
			for _, lane := range plan.TestMatrix.Sequential {
				if len(lane.Steps) == 0 || lane.Steps[0].Variant != variant {
					continue
				}
				matched = true
				for index, step := range lane.Steps {
					filter, err := types.ParseLabelFilter(step.LabelFilter)
					if err != nil {
						t.Fatal(err)
					}
					selected := filter([]string{"hosted-cluster-metrics", "hosted-cluster-operator-metrics"})
					if selected != (index == len(lane.Steps)-1) {
						t.Fatalf("%s must select operator metrics only in its final child", step.Name)
					}
					if filter([]string{"hosted-cluster-metrics", "hosted-cluster-node-tuning-metrics"}) != (index == len(lane.Steps)-1) {
						t.Fatalf("%s must select the inherited NTO metrics leaf only in its final child", step.Name)
					}
					for _, labels := range [][]string{{"hosted-cluster-metrics"}, {"hosted-cluster-metrics", "Informing"}} {
						if filter(labels) {
							t.Fatalf("%s selects guest-dependent metrics group %v", step.Name, labels)
						}
					}
				}
			}
			if !matched {
				t.Fatalf("missing restricted root for %s", variant)
			}
		})
	}
}

func TestAWSPlatformConfigTestMatrixRestrictedAdmissionSelection(t *testing.T) {
	plan := readAWSRootOptInPlan(t)
	for _, variant := range []string{"break-glass-ha", "nodepool-dispatch", "trust-bundle", "private", "private-route"} {
		t.Run("When the "+variant+" restricted lane runs, it should retain exact public admission and Route applicability", func(t *testing.T) {
			matched := false
			for _, lane := range plan.TestMatrix.Sequential {
				if len(lane.Steps) == 0 || lane.Steps[0].Variant != variant {
					continue
				}
				matched = true
				for index, step := range lane.Steps {
					filter, err := types.ParseLabelFilter(step.LabelFilter)
					if err != nil {
						t.Fatal(err)
					}
					public := variant == "break-glass-ha" || variant == "nodepool-dispatch" || variant == "trust-bundle"
					if filter([]string{"hosted-cluster-security", "hosted-cluster-admission-policies"}) != (public && index == len(lane.Steps)-1) {
						t.Fatalf("%s has incorrect inherited admission selection", step.Name)
					}
					if (variant == "nodepool-dispatch" || variant == "trust-bundle") && filter([]string{"hosted-cluster-compliance", "routes"}) != (index == len(lane.Steps)-1) {
						t.Fatalf("%s must select the management Route leaf only after its feature", step.Name)
					}
					for _, labels := range [][]string{{"hosted-cluster-security"}, {"hosted-cluster-security", "Informing"}, {"hosted-cluster-compliance"}, {"hosted-cluster-compliance", "Informing"}} {
						if filter(labels) {
							t.Fatalf("%s broadens restricted selection to unrelated guest checks %v", step.Name, labels)
						}
					}
				}
			}
			if !matched {
				t.Fatalf("missing restricted variant %s", variant)
			}
		})
	}
}

func TestAWSPlatformConfigTestMatrixCloudRootSelection(t *testing.T) {
	for _, testCase := range []struct {
		file     string
		expected awsRootLaneExpectation
		features []string
	}{
		{
			file: "aws-e2e-v2-opt-in.yaml",
			expected: awsRootLaneExpectation{
				lane: "aws-cloud-opt-ins", variant: "public",
				names:          []string{"aws-cloud-configured-workers", "aws-cloud-common-before", "aws-cloud-and-version", "aws-cloud-common-after"},
				featureFilters: []string{"nodepool-aws-cloud || nodepool-n3-release || nodepool-n4-release"},
			},
			features: []string{"nodepool-aws-cloud", "nodepool-n3-release", "nodepool-n4-release"},
		},
		{
			file: "aws-e2e-v2-cross-major.yaml",
			expected: awsRootLaneExpectation{
				lane: "aws-nodepool-cross-major", variant: "public",
				names:          []string{"aws-nodepool-cross-major-configured-workers", "aws-nodepool-cross-major-common-before", "aws-nodepool-major-version-upgrade", "aws-nodepool-major-version-upgrade-health", "aws-nodepool-cross-major-common-after"},
				featureFilters: []string{"nodepool-major-version-upgrade", "hosted-cluster-health || control-plane-workloads || aws-control-plane-customization"},
			},
			features: []string{"nodepool-major-version-upgrade"},
		},
	} {
		t.Run("When the "+testCase.expected.lane+" plan runs, it should surround the original required-input features with public checks", func(t *testing.T) {
			plan := readAWSRootPlan(t, testCase.file)
			if err := plan.Validate((&AWSPlatformConfig{}).ClusterSpecs("latest", "previous")); err != nil {
				t.Fatal(err)
			}
			assertAWSRootLane(t, plan, testCase.expected)
			for _, lane := range plan.TestMatrix.Sequential {
				if lane.Name != testCase.expected.lane {
					continue
				}
				if lane.Steps[1].LabelFilter != awsRootBeforeLabels || lane.Steps[len(lane.Steps)-1].LabelFilter != awsRootAfterSuccessLabels {
					t.Fatal("explicit public plan must use the default public before/after semantics")
				}
				featureFilter, err := types.ParseLabelFilter(lane.Steps[2].LabelFilter)
				if err != nil {
					t.Fatal(err)
				}
				for _, feature := range testCase.features {
					if !featureFilter([]string{"lifecycle", "nodepool-lifecycle", feature}) {
						t.Fatalf("%s omits the actual %s feature body", lane.Steps[2].Name, feature)
					}
				}
			}
		})
	}
}

func assertAWSRestrictedCommonSelection(t *testing.T, step TestGroup, variant string) {
	t.Helper()
	filter, err := types.ParseLabelFilter(step.LabelFilter)
	if err != nil {
		t.Fatal(err)
	}
	for _, labels := range [][]string{
		{"hosted-cluster-health-configured-workers"},
		{"hosted-cluster-ingress"}, {"hosted-cluster-ingress", "Informing"},
		{"hosted-cluster-image-registry"}, {"hosted-cluster-capabilities"},
	} {
		if filter(labels) {
			t.Fatalf("%s selects incompatible initial/feature labels %v", step.Name, labels)
		}
	}
	for _, labels := range [][]string{{"hosted-cluster-health", "hosted-cluster-health-management"}, {"control-plane-workloads"}} {
		if !filter(labels) {
			t.Fatalf("%s omits applicable management checks %v", step.Name, labels)
		}
	}
	if variant == "cilium" && filter([]string{"hosted-cluster-metrics"}) {
		t.Fatalf("%s broadens Other-network selection to metrics", step.Name)
	}
	if variant == "nodepool-dispatch" || variant == "trust-bundle" {
		for _, labels := range [][]string{
			{"hosted-cluster-security", "hosted-cluster-webhook-validation"},
			{"hosted-cluster-health", "hosted-cluster-authentication-status"},
		} {
			if !filter(labels) {
				t.Fatalf("%s omits baseline zero-worker public leaf %v", step.Name, labels)
			}
		}
	}
	if variant == "break-glass-ha" || variant == "nodepool-dispatch" || variant == "trust-bundle" {
		for _, label := range []string{"hosted-cluster-health", "hosted-cluster-security", "hosted-cluster-compliance", "hosted-cluster-metrics", "hosted-cluster-node-communication"} {
			if filter([]string{label}) {
				t.Fatalf("%s selects guest-dependent group %s on a zero-worker fixture", step.Name, label)
			}
		}
	}
}

func assertAWSRootLane(t *testing.T, plan *TestPlan, expected awsRootLaneExpectation) {
	t.Helper()
	for _, lane := range plan.TestMatrix.Sequential {
		if lane.Name != expected.lane {
			continue
		}
		names := make([]string, 0, len(lane.Steps))
		for _, step := range lane.Steps {
			names = append(names, step.Name)
			if step.Variant != expected.variant || step.Skip != "" {
				t.Fatalf("%s changes fixture or skips its assertions: %#v", step.Name, step)
			}
		}
		if !slices.Equal(names, expected.names) {
			t.Fatalf("%s steps = %v, expected %v", lane.Name, names, expected.names)
		}
		if lane.Steps[0].LabelFilter != "hosted-cluster-health-configured-workers" {
			t.Fatal("initial independent worker validation must be the distinct first child")
		}
		for index, filter := range expected.featureFilters {
			if lane.Steps[index+2].LabelFilter != filter {
				t.Fatalf("%s changed feature selection %q", lane.Steps[index+2].Name, filter)
			}
		}
		assertAWSRootCommonSelection(t, lane.Steps[1], expected.private, true)
		assertAWSRootCommonSelection(t, lane.Steps[len(lane.Steps)-1], expected.private, false)
		return
	}
	t.Fatalf("missing sequential lane %s", expected.lane)
}

func assertAWSRootCommonSelection(t *testing.T, step TestGroup, private, before bool) {
	t.Helper()
	filter, err := types.ParseLabelFilter(step.LabelFilter)
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"control-plane-workloads", "aws-control-plane-customization"} {
		if !filter([]string{label}) {
			t.Fatalf("%s omits %s", step.Name, label)
		}
	}
	if !filter([]string{"hosted-cluster-health", "hosted-cluster-health-management"}) {
		t.Fatalf("%s omits current desired/NLB management health checks", step.Name)
	}
	if !filter([]string{"hosted-cluster-compliance", "routes"}) || !filter([]string{"hosted-cluster-security", "hosted-cluster-network-policies"}) {
		t.Fatalf("%s omits management route/network-policy checks", step.Name)
	}
	for _, label := range []string{"hosted-cluster-health", "hosted-cluster-compliance", "hosted-cluster-security", "hosted-cluster-node-communication", "hosted-cluster-metrics"} {
		if filter([]string{label}) == private {
			t.Fatalf("%s has incorrect public/private applicability for %s", step.Name, label)
		}
	}
	for _, labels := range [][]string{
		{"hosted-cluster-ingress"}, {"hosted-cluster-ingress", "Informing"},
		{"hosted-cluster-health", "hosted-cluster-ingress"},
		{"hosted-cluster-health", "hosted-cluster-ingress", "Informing"},
	} {
		if filter(labels) != (!private && !before) {
			t.Fatalf("%s must exclude inherited ingress mutation labels before features and retain public after coverage: %v", step.Name, labels)
		}
	}
	for _, labels := range [][]string{
		{"hosted-cluster-health-configured-workers"},
		{"hosted-cluster-health", "hosted-cluster-health-configured-workers"},
		{"hosted-cluster-health", "hosted-cluster-health-configured-workers", "Informing"},
		{"lifecycle", "nodepool-aws-cloud", "nodepool-aws-root-volume"},
		{"lifecycle", "nodepool-lifecycle", "nodepool-n3-release"},
		{"lifecycle", "nodepool-lifecycle", "nodepool-n4-release"},
		{"lifecycle", "nodepool-lifecycle", "nodepool-major-version-upgrade"},
		{"lifecycle", "karpenter"}, {"lifecycle", "karpenter-upgrade"},
		{"control-plane-upgrade"}, {"control-plane-pki-operator"}, {"etcd-chaos"},
		{"hosted-cluster-managed-dns"}, {"aws-private-endpoint-transition"},
	} {
		if filter(labels) {
			t.Fatalf("%s reselects initial/feature assertions with labels %v", step.Name, labels)
		}
	}
}

func TestAWSPlatformConfigCPOOverrideRootSelection(t *testing.T) {
	plan := readAWSRootPlan(t, "aws-e2e-v2-cpo-overrides.yaml")
	if len(plan.TestMatrix.Parallel) != 0 || len(plan.TestMatrix.Sequential) != 2 {
		t.Fatal("CPO root coverage must use exactly two independent sequential lanes")
	}
	for _, variant := range []string{"cpo-override-latest", "cpo-override-previous"} {
		t.Run("When "+variant+" is selected, it should retain public hooks around its original required assertion", func(t *testing.T) {
			assertAWSRootLane(t, plan, awsRootLaneExpectation{
				lane: variant, variant: variant,
				names:          []string{variant + "-configured-workers", variant + "-common-before", variant, variant + "-common-after"},
				featureFilters: []string{"hosted-cluster-cpo-required"},
			})
		})
	}
	if err := plan.Validate((&AWSPlatformConfig{}).ClusterSpecs("unrelated-latest", "unrelated-previous")); err != nil {
		t.Fatal(err)
	}
}

func TestAWSPlatformConfigTestMatrixRootVariantOwnership(t *testing.T) {
	defaultPlan := (&AWSPlatformConfig{}).DefaultTestPlan()
	for _, plan := range []*TestPlan{&defaultPlan, readAWSRootOptInPlan(t), readAWSRootPlan(t, "aws-e2e-v2-cross-major.yaml")} {
		t.Run("When "+plan.Name+" is resolved, it should validate unique variant ownership and initial-only selection", func(t *testing.T) {
			if err := plan.Validate((&AWSPlatformConfig{}).ClusterSpecs("latest", "previous")); err != nil {
				t.Fatal(err)
			}
			for _, group := range plan.TestMatrix.Parallel {
				if slices.Contains([]string{"managed-dns", "karpenter", "karpenter-upgrade"}, group.Variant) {
					t.Fatalf("variant %s retained an overlapping parallel owner", group.Variant)
				}
			}
			for _, lane := range plan.TestMatrix.Sequential {
				for index, step := range lane.Steps {
					filter, err := types.ParseLabelFilter(step.LabelFilter)
					if err != nil {
						t.Fatal(err)
					}
					if index > 0 && filter([]string{"hosted-cluster-health-configured-workers"}) {
						t.Fatalf("%s rechecks initial replicas after mutations", step.Name)
					}
				}
			}
		})
	}
}

func readAWSRootOptInPlan(t *testing.T) *TestPlan {
	t.Helper()
	return readAWSRootPlan(t, "aws-e2e-v2-opt-in.yaml")
}

func readAWSRootPlan(t *testing.T, file string) *TestPlan {
	t.Helper()
	plan, err := ReadTestPlan(filepath.Join("..", "..", "..", "..", "contrib", file))
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
