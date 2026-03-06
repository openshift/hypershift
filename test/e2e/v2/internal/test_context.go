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

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/sqs"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/awsapi"
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
	hostedCluster         *hyperv1.HostedCluster
	hostedClusterMu       sync.RWMutex
	awsCredentialsFile    string
	awsRegion             string
	awsRegionMu           sync.RWMutex
}

// GetHostedCluster returns the HostedCluster associated with this test context.
// It fetches the HostedCluster lazily on first call if ClusterName and ClusterNamespace are set,
// caching the result for subsequent calls. Pass refresh=true to bypass the cache and fetch a
// fresh copy (e.g. to pick up status changes).
// Returns (nil, nil) if ClusterName/ClusterNamespace are not set.
func (tc *TestContext) GetHostedCluster(refresh ...bool) (*hyperv1.HostedCluster, error) {
	shouldRefresh := len(refresh) > 0 && refresh[0]

	if !shouldRefresh {
		tc.hostedClusterMu.RLock()
		if tc.hostedCluster != nil {
			hc := tc.hostedCluster
			tc.hostedClusterMu.RUnlock()
			return hc, nil
		}
		tc.hostedClusterMu.RUnlock()
	}

	if tc.ClusterName == "" || tc.ClusterNamespace == "" {
		return nil, nil
	}

	hostedCluster := &hyperv1.HostedCluster{}
	err := tc.MgmtClient.Get(context.Background(), crclient.ObjectKey{
		Namespace: tc.ClusterNamespace,
		Name:      tc.ClusterName,
	}, hostedCluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get HostedCluster %s/%s: %w", tc.ClusterNamespace, tc.ClusterName, err)
	}

	err = e2eutil.SetReleaseVersionFromHostedCluster(context.Background(), hostedCluster)
	if err != nil {
		return nil, fmt.Errorf("failed to set release version from HostedCluster: %w", err)
	}

	tc.hostedClusterMu.Lock()
	tc.hostedCluster = hostedCluster
	tc.hostedClusterMu.Unlock()

	return hostedCluster, nil
}

// GetAWSRegion returns the AWS region from the HostedCluster spec.
// Returns an error if the HostedCluster is not available or is not an AWS platform.
func (tc *TestContext) GetAWSRegion() (string, error) {
	tc.awsRegionMu.RLock()
	if tc.awsRegion != "" {
		region := tc.awsRegion
		tc.awsRegionMu.RUnlock()
		return region, nil
	}
	tc.awsRegionMu.RUnlock()

	hc, err := tc.GetHostedCluster()
	if err != nil {
		return "", fmt.Errorf("cannot resolve AWS region: %w", err)
	}
	if hc == nil {
		return "", fmt.Errorf("cannot resolve AWS region: HostedCluster is not available")
	}
	if hc.Spec.Platform.AWS == nil {
		return "", fmt.Errorf("cannot resolve AWS region: HostedCluster %s/%s is not an AWS platform", hc.Namespace, hc.Name)
	}
	region := hc.Spec.Platform.AWS.Region

	tc.awsRegionMu.Lock()
	if tc.awsRegion == "" {
		tc.awsRegion = region
	}
	region = tc.awsRegion
	tc.awsRegionMu.Unlock()
	return region, nil
}

func (tc *TestContext) requireAWSCredentials() error {
	if tc.awsCredentialsFile == "" {
		return fmt.Errorf("AWS_CREDENTIALS_FILE environment variable is not set. It is required for AWS-specific tests")
	}
	return nil
}

// GetEC2Client returns an EC2 client configured for the HostedCluster's region.
func (tc *TestContext) GetEC2Client() (*ec2.EC2, error) {
	if err := tc.requireAWSCredentials(); err != nil {
		return nil, err
	}
	region, err := tc.GetAWSRegion()
	if err != nil {
		return nil, err
	}
	return e2eutil.GetEC2Client(tc.awsCredentialsFile, region), nil
}

// GetIAMClient returns an IAM client configured for the HostedCluster's region.
func (tc *TestContext) GetIAMClient() (awsapi.IAMAPI, error) {
	if err := tc.requireAWSCredentials(); err != nil {
		return nil, err
	}
	region, err := tc.GetAWSRegion()
	if err != nil {
		return nil, err
	}
	return e2eutil.GetIAMClient(tc.Context, tc.awsCredentialsFile, region), nil
}

// GetSQSClient returns an SQS client configured for the HostedCluster's region.
func (tc *TestContext) GetSQSClient() (*sqs.SQS, error) {
	if err := tc.requireAWSCredentials(); err != nil {
		return nil, err
	}
	region, err := tc.GetAWSRegion()
	if err != nil {
		return nil, err
	}
	return e2eutil.GetSQSClient(tc.awsCredentialsFile, region), nil
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
		awsCredentialsFile:    GetEnvVarValue("AWS_CREDENTIALS_FILE"),
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

	// If both env vars are present, set up full context with cluster info
	if hostedClusterName != "" && hostedClusterNamespace != "" {
		testCtx.ClusterName = hostedClusterName
		testCtx.ClusterNamespace = hostedClusterNamespace
		testCtx.ControlPlaneNamespace = manifests.HostedControlPlaneNamespace(hostedClusterNamespace, hostedClusterName)
	}

	// AWS credentials file path from environment (optional, required only for AWS-specific tests)
	testCtx.awsCredentialsFile = GetEnvVarValue("AWS_CREDENTIALS_FILE")

	return testCtx, nil
}
