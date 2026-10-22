//go:build e2ev2

package lifecycle

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/onsi/ginkgo/v2/types"
)

func TestAWSPlatformConfigNativeRecoverySelection(t *testing.T) {
	plan, err := ReadTestPlan(filepath.Join("..", "..", "..", "..", "contrib", "aws-e2e-v2-recovery.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	config := &AWSPlatformConfig{region: "us-east-1", zones: []string{"us-east-1a"}}
	t.Setenv("EXTRA_ARGS", "--node-pool-replicas=2 --control-plane-availability-policy=SingleReplica")
	specs := config.ClusterSpecs("latest", "previous")
	if err := plan.Validate(specs); err != nil {
		t.Fatal(err)
	}
	if plan.Platform != "aws" || len(plan.TestMatrix.Sequential) != 2 || len(plan.TestMatrix.Parallel) != 0 {
		t.Fatal("native recovery requires exactly two isolated AWS fixture owners")
	}
	selected := plan.FilterClusterSpecs(specs)
	if len(selected) != 2 {
		t.Fatal("recovery plan must select exactly two dedicated fixtures")
	}
	for _, spec := range selected {
		t.Run("When "+spec.Variant+" is selected, it should enforce originating HA and zero workers without an upgrade", func(t *testing.T) {
			assertNativeRecoveryClusterSpec(t, config, spec)
		})
	}
	for _, lane := range plan.TestMatrix.Sequential {
		if len(lane.Steps) != 3 {
			t.Fatal("native recovery must retain common assertions before and after its mutation")
		}
		group := lane.Steps[1]
		for _, step := range lane.Steps {
			if step.Variant != group.Variant {
				t.Fatal("recovery lane must have exactly one dedicated fixture owner")
			}
		}
		for _, step := range []TestGroup{lane.Steps[0], lane.Steps[2]} {
			filter, err := types.ParseLabelFilter(step.LabelFilter)
			if err != nil {
				t.Fatal(err)
			}
			for _, label := range []string{"control-plane-workloads", "hosted-cluster-health-management", "hosted-cluster-metrics", "aws-control-plane-customization", "routes", "hosted-cluster-network-policies"} {
				if !filter([]string{label}) {
					t.Fatalf("common step %s omitted %s", step.Name, label)
				}
			}
			for _, labels := range [][]string{{"hosted-cluster-native-recovery", "native-etcd-recovery"}, {"hosted-cluster-native-recovery", "malformed-pull-secret-recovery"}, {"lifecycle", "etcd-chaos"}, {"hosted-cluster-ingress"}, {"control-plane-upgrade"}} {
				if filter(labels) {
					t.Fatalf("common step %s reselected a mutation or worker-only feature: %v", step.Name, labels)
				}
			}
		}
		t.Run("When "+group.Name+" is parsed, it should select only its native feature and no legacy chaos", func(t *testing.T) {
			filter, err := types.ParseLabelFilter(group.LabelFilter)
			if err != nil {
				t.Fatal(err)
			}
			feature := group.Variant
			if feature != "malformed-pull-secret-recovery" && feature != "native-etcd-recovery" {
				t.Fatal("unexpected fixture in recovery plan")
			}
			if !filter([]string{"hosted-cluster-native-recovery", feature}) {
				t.Fatal("native feature is unreachable")
			}
			other := "native-etcd-recovery"
			if feature == other {
				other = "malformed-pull-secret-recovery"
			}
			for _, labels := range [][]string{{"etcd-chaos", "lifecycle"}, {"hosted-cluster-native-recovery", other}, {"hosted-cluster-health"}, {"hosted-cluster-native-recovery"}} {
				if filter(labels) {
					t.Fatalf("fixture selected unrelated feature labels %v", labels)
				}
			}
		})
	}
}

func assertNativeRecoveryClusterSpec(t *testing.T, config *AWSPlatformConfig, spec ClusterSpec) {
	t.Helper()
	if spec.InitialNodePoolReplicas == nil || *spec.InitialNodePoolReplicas != 0 || spec.ReleaseImage != "" {
		t.Fatal("native recovery must originate at the requested current release with zero workers")
	}
	for _, argument := range []string{"--public-only", "--shared-role=false", "--annotations=hypershift-e2e-native-recovery=" + spec.Variant} {
		if !slices.Contains(spec.CreateArgs, argument) {
			t.Fatalf("fixture %s missing %s", spec.Variant, argument)
		}
	}
	for _, flag := range []struct{ prefix, expected string }{
		{"--node-pool-replicas=", "--node-pool-replicas=0"},
		{"--control-plane-availability-policy=", "--control-plane-availability-policy=HighlyAvailable"},
	} {
		last := ""
		for _, argument := range spec.CreateArgs {
			if len(argument) >= len(flag.prefix) && argument[:len(flag.prefix)] == flag.prefix {
				last = argument
			}
		}
		if last != flag.expected {
			t.Fatalf("fixture invariant overridden: %s", last)
		}
	}
	if slices.Contains(config.DefaultTestPlan().TestMatrix.Variants(), spec.Variant) {
		t.Fatal("native recovery fixture must remain default-off")
	}
}
