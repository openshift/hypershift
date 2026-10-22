//go:build e2ev2

package tests

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
)

func TestRegisterNativeEtcdRecoveryTests(t *testing.T) {
	t.Run("When the real recovery plan selects originating HA chaos, it should reach all five native bodies and no legacy tests", func(t *testing.T) {
		plan, err := lifecycle.ReadTestPlan(filepath.Join("..", "..", "..", "..", "contrib", "aws-e2e-v2-recovery.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		filter, err := types.ParseLabelFilter(defaultFixtureLabelFilter(plan.TestMatrix.Sequential[0].Steps[1].LabelFilter))
		if err != nil {
			t.Fatal(err)
		}
		defaultFilter, err := types.ParseLabelFilter(defaultFixtureLabelFilter(""))
		if err != nil {
			t.Fatal(err)
		}
		report := ginkgo.PreviewSpecs("native recovery registration regression")
		selected := 0
		for _, spec := range report.SpecReports {
			if !filter(spec.Labels()) {
				continue
			}
			selected++
			if defaultFilter(spec.Labels()) {
				t.Fatal("standalone default selection reached destructive native recovery")
			}
			if !strings.Contains(spec.FullText(), "Native originating HA etcd recovery") {
				t.Fatalf("native selector reached another test: %s", spec.FullText())
			}
			if spec.LeafNodeLocation.FileName == "" || !strings.HasSuffix(spec.LeafNodeLocation.FileName, "hosted_cluster_native_etcd_recovery_test.go") {
				t.Fatalf("native selector reached a legacy/non-native body: %s", spec.LeafNodeLocation.FileName)
			}
		}
		if selected != 5 {
			t.Fatalf("selected %d native HA chaos bodies, want five", selected)
		}
	})
}

func TestRegisterNativeMalformedPullSecretRecoveryTests(t *testing.T) {
	t.Run("When the real recovery plan selects malformed pull Secret recovery, it should reach exactly its native body", func(t *testing.T) {
		plan, err := lifecycle.ReadTestPlan(filepath.Join("..", "..", "..", "..", "contrib", "aws-e2e-v2-recovery.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		filter, err := types.ParseLabelFilter(defaultFixtureLabelFilter(plan.TestMatrix.Sequential[1].Steps[1].LabelFilter))
		if err != nil {
			t.Fatal(err)
		}
		defaultFilter, err := types.ParseLabelFilter(defaultFixtureLabelFilter(""))
		if err != nil {
			t.Fatal(err)
		}
		selected := 0
		for _, spec := range ginkgo.PreviewSpecs("native pull Secret registration regression").SpecReports {
			if !filter(spec.Labels()) {
				continue
			}
			selected++
			if defaultFilter(spec.Labels()) {
				t.Fatal("standalone default selection reached malformed pull Secret recovery")
			}
			if !strings.HasSuffix(spec.LeafNodeLocation.FileName, "hosted_cluster_malformed_pull_secret_recovery_test.go") {
				t.Fatalf("malformed pull Secret selector reached the wrong body: %s", spec.LeafNodeLocation.FileName)
			}
		}
		if selected != 1 {
			t.Fatalf("selected %d malformed pull Secret recovery bodies, want one", selected)
		}
	})
}
