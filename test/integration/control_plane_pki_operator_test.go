//go:build integration

package integration

import (
	"testing"

	"github.com/openshift/hypershift/test/integration/framework"
)

func TestControlPlanePKIOperatorBreakGlassCredentials(t *testing.T) {
	framework.RunHostedClusterTest(testContext, log, globalOpts, t, func(t *testing.T, testCtx *framework.TestContext) {
		RunTestControlPlanePKIOperatorBreakGlassCredentials(t, testContext, testCtx.HostedCluster, testCtx.MgmtCluster, testCtx.GuestCluster)
	})
}
