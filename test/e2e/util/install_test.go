package util

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestInstallHyperShiftOperator(t *testing.T) {
	err := InstallHyperShiftOperator(t.Context(), HyperShiftOperatorInstallOptions{
		DryRun:          true,
		DryRunDir:       t.TempDir(),
		PrivatePlatform: string(hyperv1.NonePlatform),
	})
	if err != nil {
		t.Fatalf("expected dry-run installation to render locally: %v", err)
	}
}
