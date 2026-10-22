//go:build e2ev2

package internal

import (
	"path/filepath"
	"testing"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
)

func TestSetupTestContextFromEnv_InvalidExpectedNodePools(t *testing.T) {
	t.Run("When configured cardinality is invalid, it should reject it before management client initialization", func(t *testing.T) {
		t.Setenv(lifecycle.ExpectedNodePoolsEnv, `{"fixture":-1}`)
		t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "missing-kubeconfig"))
		testContext, err := SetupTestContextFromEnv(t.Context())
		if err == nil || err.Error() != `invalid configured NodePool "fixture" replicas -1` || testContext != nil {
			t.Fatalf("expected cardinality rejection before management lookup, got context %v, error %v", testContext, err)
		}
	})
}
