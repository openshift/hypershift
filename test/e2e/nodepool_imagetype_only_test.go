//go:build e2e

package e2e

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestNodePoolImageTypeOnly(t *testing.T) {
	t.Parallel()

	nodePoolTestCasesPerHostedCluster := []HostedClusterNodePoolTestCases{
		{
			build: func(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hostedClusterClient crclient.Client, clusterOpts e2eutil.PlatformAgnosticOptions) []NodePoolTestCase {
				return []NodePoolTestCase{
					{
						name: "TestImageTypes",
						test: NewNodePoolImageTypeTest(ctx, mgtClient, hostedCluster, hostedClusterClient, clusterOpts),
					},
				}
			},
		},
	}

	executeNodePoolTests(t, nodePoolTestCasesPerHostedCluster)
}
