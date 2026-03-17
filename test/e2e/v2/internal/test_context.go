//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package internal

import (
	"context"
	"fmt"
	"sync"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestContextGetter is a function type that returns a TestContext.
// It is used to lazily access the test context in test functions.
type TestContextGetter func() *TestContext

// TestContext holds the test context including clients and hosted cluster reference
type TestContext struct {
	context.Context
	MgmtClient            crclient.Client
	ClusterName           string
	ClusterNamespace      string
	ControlPlaneNamespace string
	ArtifactDir           string
	hostedCluster         *hyperv1.HostedCluster
	hostedClusterOnce     sync.Once
}

// GetHostedCluster returns the HostedCluster associated with this test context.
// It fetches the HostedCluster lazily on first call if ClusterName and ClusterNamespace are set.
// Returns nil if the HostedCluster cannot be fetched or if ClusterName/ClusterNamespace are not set.
func (tc *TestContext) GetHostedCluster() *hyperv1.HostedCluster {
	tc.hostedClusterOnce.Do(func() {
		if tc.ClusterName == "" || tc.ClusterNamespace == "" {
			return
		}

		hostedCluster := &hyperv1.HostedCluster{}
		err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
			Namespace: tc.ClusterNamespace,
			Name:      tc.ClusterName,
		}, hostedCluster)
		if err != nil {
			// In test code, panicking is acceptable and will fail the test appropriately
			panic(fmt.Sprintf("failed to get HostedCluster %s/%s: %v", tc.ClusterNamespace, tc.ClusterName, err))
		}

		err = e2eutil.SetReleaseVersionFromHostedCluster(tc.Context, hostedCluster)
		if err != nil {
			panic(fmt.Sprintf("failed to set release version from HostedCluster: %v", err))
		}

		tc.hostedCluster = hostedCluster
	})
	return tc.hostedCluster
}

var (
	// Global test context - set in BeforeSuite
	testCtx *TestContext
)

// GetTestContext returns the global test context
func GetTestContext() *TestContext {
	return testCtx
}

// SetTestContext sets the global test context
func SetTestContext(ctx *TestContext) {
	testCtx = ctx
}

// SetupTestContext initializes the test context from a HostedCluster
func SetupTestContext(ctx context.Context, hostedClusterName, hostedClusterNamespace string) (*TestContext, error) {
	// Get management client
	mgmtClient, err := e2eutil.GetClient()
	if err != nil {
		return nil, fmt.Errorf("failed to get management client: %w", err)
	}

	testCtx := &TestContext{
		Context:               ctx,
		MgmtClient:            mgmtClient,
		ClusterName:           hostedClusterName,
		ClusterNamespace:      hostedClusterNamespace,
		ControlPlaneNamespace: manifests.HostedControlPlaneNamespace(hostedClusterNamespace, hostedClusterName),
	}

	return testCtx, nil
}

// SetupTestContextFromEnv initializes the test context from environment variables.
// It reads E2E_HOSTED_CLUSTER_NAME and E2E_HOSTED_CLUSTER_NAMESPACE from the environment.
// If these are not set, it creates a basic context with only the management client.
func SetupTestContextFromEnv(ctx context.Context) (*TestContext, error) {
	// Get management client
	mgmtClient, err := e2eutil.GetClient()
	if err != nil {
		return nil, fmt.Errorf("failed to get management client: %w", err)
	}

	testCtx := &TestContext{
		Context:    ctx,
		MgmtClient: mgmtClient,
	}

	hostedClusterName := GetEnvVarValue("E2E_HOSTED_CLUSTER_NAME")
	hostedClusterNamespace := GetEnvVarValue("E2E_HOSTED_CLUSTER_NAMESPACE")
	artifactDir := GetEnvVarValue("ARTIFACT_DIR")

	// If both env vars are present, set up full context with cluster info
	if hostedClusterName != "" && hostedClusterNamespace != "" {
		testCtx.ClusterName = hostedClusterName
		testCtx.ClusterNamespace = hostedClusterNamespace
		testCtx.ControlPlaneNamespace = manifests.HostedControlPlaneNamespace(hostedClusterNamespace, hostedClusterName)
	}
	testCtx.ArtifactDir = artifactDir

	return testCtx, nil
}

// ValidateControlPlaneNamespace checks if the ControlPlaneNamespace is set in the test context.
// Returns an error with a helpful message if not set.
func (tc *TestContext) ValidateControlPlaneNamespace() error {
	if tc.ControlPlaneNamespace == "" {
		return fmt.Errorf("ControlPlaneNamespace is required but not set. Please set the following environment variables:\n" +
			"  E2E_HOSTED_CLUSTER_NAME - Name of the HostedCluster to test\n" +
			"  E2E_HOSTED_CLUSTER_NAMESPACE - Namespace of the HostedCluster to test")
	}
	return nil
}
