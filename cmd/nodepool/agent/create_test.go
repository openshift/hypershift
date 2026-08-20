package agent

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/google/go-cmp/cmp"
)

// newAgentNodePool builds the NodePool fixture shared by the UpdateNodePool test cases.
func newAgentNodePool() *hyperv1.NodePool {
	return &hyperv1.NodePool{
		Spec: hyperv1.NodePoolSpec{
			Arch: string(hyperv1.ArchitectureAMD64),
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.AgentPlatform,
			},
		},
	}
}

func TestNewCreateCommand(t *testing.T) {
	coreOpts := &core.CreateNodePoolOptions{}
	cmd := NewCreateCommand(coreOpts)

	if cmd.Use != "agent" {
		t.Errorf("expected Use to be %q, got %q", "agent", cmd.Use)
	}
	if cmd.RunE == nil {
		t.Error("expected RunE to be set")
	}
	flag := cmd.Flag("agentLabelSelector")
	if flag == nil {
		t.Fatal("expected agentLabelSelector flag to be registered")
	}
	if flag.DefValue != "" {
		t.Errorf("expected agentLabelSelector default to be empty, got %q", flag.DefValue)
	}
}

func TestUpdateNodePool(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		labelSelector    string
		expectedSelector *metav1.LabelSelector
		expectPanic      bool
	}{
		{
			name:          "When an empty selector is provided, it should produce an empty label selector",
			labelSelector: "",
			expectedSelector: &metav1.LabelSelector{
				MatchLabels:      map[string]string{},
				MatchExpressions: []metav1.LabelSelectorRequirement{},
			},
		},
		{
			name:          "When an equality-based selector is provided, it should set matchLabels",
			labelSelector: "size=large",
			expectedSelector: &metav1.LabelSelector{
				MatchLabels:      map[string]string{"size": "large"},
				MatchExpressions: []metav1.LabelSelectorRequirement{},
			},
		},
		{
			name:          "When a set-based selector is provided, it should set matchLabels and matchExpressions",
			labelSelector: "size=large,zone notin (az1,az2)",
			expectedSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"size": "large"},
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "zone",
						Operator: metav1.LabelSelectorOpNotIn,
						Values:   []string{"az1", "az2"},
					},
				},
			},
		},
		{
			name:          "When an invalid label selector is provided, it should panic",
			labelSelector: "!!!invalid!!!",
			expectPanic:   true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := t.Context()

			platformOpts := &AgentPlatformCreateOptions{
				AgentLabelSelector: testCase.labelSelector,
			}
			nodePool := newAgentNodePool()

			if testCase.expectPanic {
				defer func() {
					if r := recover(); r == nil {
						t.Fatal("expected panic for invalid label selector, got none")
					}
				}()
				_ = platformOpts.UpdateNodePool(ctx, nodePool, nil, nil)
				return
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

func TestType(t *testing.T) {
	opts := &AgentPlatformCreateOptions{}
	if opts.Type() != hyperv1.AgentPlatform {
		t.Errorf("expected Type() to return %q, got %q", hyperv1.AgentPlatform, opts.Type())
	}
}
