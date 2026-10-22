//go:build e2ev2

package main

import (
	"path/filepath"
	"testing"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
)

func TestHealthWorkerPhase(t *testing.T) {
	plans := []lifecycle.TestPlan{(&lifecycle.AWSPlatformConfig{}).DefaultTestPlan()}
	for _, name := range []string{"aws-e2e-v2-opt-in.yaml", "aws-e2e-v2-cpo-overrides.yaml", "aws-e2e-v2-cross-major.yaml"} {
		plan, err := lifecycle.ReadTestPlan(filepath.Join("..", "..", "..", "..", "..", "contrib", name))
		if err != nil {
			t.Fatal(err)
		}
		plans = append(plans, *plan)
	}
	afterCount := 0
	for _, plan := range plans {
		for _, lane := range plan.TestMatrix.Sequential {
			for index, step := range lane.Steps {
				t.Run("When the actual "+step.Name+" child runs, it should use its source-proven worker phase", func(t *testing.T) {
					want := ""
					if index == len(lane.Steps)-1 || step.Name == "post-upgrade-health" || step.Name == "aws-upgrade-pki-disabled-health" || step.Name == "aws-autonode-upgrade-pki-disabled-health" || step.Name == "aws-nodepool-major-version-upgrade-health" {
						want = "after"
						afterCount++
					}
					if got := healthWorkerPhase(step.Variant, step.Name); got != want {
						t.Fatalf("%s/%s phase=%q, expected %q", step.Variant, step.Name, got, want)
					}
				})
			}
		}
	}
	if afterCount != 26 {
		t.Fatalf("actual default/opt-in/CPO/cross-major after children=%d, expected26", afterCount)
	}
	for _, testCase := range []struct{ name, variant, child string }{
		{name: "When an arbitrary child contains after, it should not acquire successful-after semantics", variant: "public", child: "arbitrary-common-after"},
		{name: "When a known after name belongs to a different fixture, it should not transfer phase", variant: "private", child: "public-common-after"},
		{name: "When a known after name is used outside AWS variants, it should remain standalone", variant: "azure", child: "public-common-after"},
		{name: "When a feature selection is unnamed, it should not infer phase from history or prior success", variant: "upgrade"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := healthWorkerPhase(testCase.variant, testCase.child); got != "" {
				t.Fatalf("unproven caller inherited %q", got)
			}
		})
	}
}
