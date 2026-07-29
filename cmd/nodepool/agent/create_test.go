package agent

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/testutil"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/google/go-cmp/cmp"
)

func TestNewCreateCommand_When_flags_are_parsed_it_should_generate_correct_nodepool(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
	}{
		{
			name: "When no label selector flag is provided, it should generate nodepool with empty agent label selector",
			args: []string{},
		},
		{
			name: "When single equality label selector is provided, it should set matchLabels on agent platform",
			args: []string{
				"--agentLabelSelector=size=large",
			},
		},
		{
			name: "When multi label selector with set-based expression is provided, it should set matchLabels and matchExpressions",
			args: []string{
				"--agentLabelSelector=size=large,zone notin (az1,az2)",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := t.Context()

			cmd, platformOpts := newCreateCommandWithOpts()
			if len(testCase.args) > 0 {
				if err := cmd.Flags().Parse(testCase.args); err != nil {
					t.Fatalf("failed to parse flags: %v", err)
				}
			}

			nodePool := &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: string(hyperv1.ArchitectureAMD64),
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AgentPlatform,
					},
				},
			}

			if err := platformOpts.UpdateNodePool(ctx, nodePool, nil, nil); err != nil {
				t.Fatalf("failed to update nodepool: %v", err)
			}

			testutil.CompareWithFixture(t, nodePool.Spec.Platform.Agent)
		})
	}
}

func TestUpdateNodePool_When_label_selector_is_parsed_it_should_set_agent_platform(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		labelSelector    string
		expectedSelector *metav1.LabelSelector
	}{
		{
			name:          "When empty selector is provided, it should produce empty label selector",
			labelSelector: "",
			expectedSelector: &metav1.LabelSelector{
				MatchLabels:      map[string]string{},
				MatchExpressions: []metav1.LabelSelectorRequirement{},
			},
		},
		{
			name:          "When equality-based selector is provided, it should set matchLabels",
			labelSelector: "size=large",
			expectedSelector: &metav1.LabelSelector{
				MatchLabels:      map[string]string{"size": "large"},
				MatchExpressions: []metav1.LabelSelectorRequirement{},
			},
		},
		{
			name:          "When set-based selector is provided, it should set matchExpressions",
			labelSelector: "zone notin (az1,az2)",
			expectedSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{},
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "zone",
						Operator: metav1.LabelSelectorOpNotIn,
						Values:   []string{"az1", "az2"},
					},
				},
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := t.Context()

			platformOpts := &AgentPlatformCreateOptions{
				AgentLabelSelector: testCase.labelSelector,
			}

			nodePool := &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: string(hyperv1.ArchitectureAMD64),
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AgentPlatform,
					},
				},
			}

			if err := platformOpts.UpdateNodePool(ctx, nodePool, nil, nil); err != nil {
				t.Fatalf("failed to update nodepool: %v", err)
			}

			if diff := cmp.Diff(testCase.expectedSelector, nodePool.Spec.Platform.Agent.AgentLabelSelector); diff != "" {
				t.Errorf("unexpected label selector (-want +got):\n%s", diff)
			}
		})
	}
}

func TestUpdateNodePool_When_invalid_label_selector_is_provided_it_should_panic(t *testing.T) {
	var returnedErr error
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic for invalid label selector, got error: %v", returnedErr)
		}
	}()

	platformOpts := &AgentPlatformCreateOptions{
		AgentLabelSelector: "!!!invalid!!!",
	}

	nodePool := &hyperv1.NodePool{
		Spec: hyperv1.NodePoolSpec{
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.AgentPlatform,
			},
		},
	}

	returnedErr = platformOpts.UpdateNodePool(t.Context(), nodePool, nil, nil)
}

func TestType_When_called_it_should_return_AgentPlatform(t *testing.T) {
	opts := &AgentPlatformCreateOptions{}
	if opts.Type() != hyperv1.AgentPlatform {
		t.Errorf("expected Type() to return %q, got %q", hyperv1.AgentPlatform, opts.Type())
	}
}
