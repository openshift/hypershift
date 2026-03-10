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
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/sqs"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/awsapi"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/tools/clientcmd"

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
	hostedClusterMu       sync.RWMutex
	guestClient           crclient.Client
	guestClientOnce       sync.Once
	awsCredentialsFile    string
	awsRegion             string
	awsRegionMu           sync.RWMutex
}

// GetNodePools returns all NodePools associated with this HostedCluster.
// It performs a fresh list on each call (NodePools are mutable during test execution).
// Filters by Spec.ClusterName to ensure only NodePools for this HostedCluster are returned.
func (tc *TestContext) GetNodePools() ([]*hyperv1.NodePool, error) {
	nodePools := &hyperv1.NodePoolList{}
	if err := tc.MgmtClient.List(tc.Context, nodePools, crclient.InNamespace(tc.ClusterNamespace)); err != nil {
		return nil, fmt.Errorf("failed to list NodePools in namespace %s: %w", tc.ClusterNamespace, err)
	}

	var result []*hyperv1.NodePool
	for i := range nodePools.Items {
		if nodePools.Items[i].Spec.ClusterName == tc.ClusterName {
			result = append(result, &nodePools.Items[i])
		}
	}
	return result, nil
}

// GetNodePool returns a specific NodePool by name.
// Performs a direct Get rather than listing.
func (tc *TestContext) GetNodePool(name string) (*hyperv1.NodePool, error) {
	np := &hyperv1.NodePool{}
	if err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
		Namespace: tc.ClusterNamespace,
		Name:      name,
	}, np); err != nil {
		return nil, fmt.Errorf("failed to get NodePool %s/%s: %w", tc.ClusterNamespace, name, err)
	}
	if np.Spec.ClusterName != tc.ClusterName {
		return nil, fmt.Errorf(
			"NodePool %s/%s belongs to cluster %q, not %q",
			tc.ClusterNamespace, name, np.Spec.ClusterName, tc.ClusterName,
		)
	}
	return np, nil
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
	err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
		Namespace: tc.ClusterNamespace,
		Name:      tc.ClusterName,
	}, hostedCluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get HostedCluster %s/%s: %w", tc.ClusterNamespace, tc.ClusterName, err)
	}

	err = e2eutil.SetReleaseVersionFromHostedCluster(tc.Context, hostedCluster)
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

// GetGuestClient returns a controller-runtime client for the guest (hosted) cluster.
// It fetches the admin kubeconfig from the HostedCluster status lazily on first call via sync.Once.
// Retries handle transient DNS propagation failures when connecting to the guest API server.
// Panics if the guest client cannot be created, as tests cannot proceed without it.
func (tc *TestContext) GetGuestClient() crclient.Client {
	tc.guestClientOnce.Do(func() {
		hc, err := tc.GetHostedCluster()
		if err != nil {
			panic(fmt.Sprintf("failed to get HostedCluster: %v", err))
		}
		if hc == nil || hc.Status.KubeConfig == nil {
			panic("HostedCluster has no kubeconfig in status")
		}

		var secret corev1.Secret
		err = tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
			Namespace: hc.Namespace,
			Name:      hc.Status.KubeConfig.Name,
		}, &secret)
		if err != nil {
			panic(fmt.Sprintf("failed to get kubeconfig secret: %v", err))
		}

		kubeconfigData, ok := secret.Data["kubeconfig"]
		if !ok {
			panic("kubeconfig secret does not contain 'kubeconfig' key")
		}

		restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
		if err != nil {
			panic(fmt.Sprintf("failed to parse guest kubeconfig: %v", err))
		}
		restConfig.QPS = -1
		restConfig.Burst = -1

		var guestClient crclient.Client
		var lastErr error
		err = wait.PollUntilContextTimeout(tc.Context, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
			guestClient, err = e2eutil.GetClientFromConfig(restConfig)
			if err != nil {
				lastErr = fmt.Errorf("build guest client: %w", err)
				return false, nil
			}
			_, apiErr := discovery.NewDiscoveryClientForConfigOrDie(restConfig).ServerVersion()
			if apiErr != nil {
				lastErr = fmt.Errorf("discover guest API server version: %w", apiErr)
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			if lastErr != nil {
				panic(fmt.Sprintf("failed to connect to guest cluster: %v (last error: %v)", err, lastErr))
			}
			panic(fmt.Sprintf("failed to connect to guest cluster: %v", err))
		}
		tc.guestClient = guestClient
	})
	return tc.guestClient
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
	artifactDir := GetEnvVarValue("ARTIFACT_DIR")

	// If both env vars are present, set up full context with cluster info
	if hostedClusterName != "" && hostedClusterNamespace != "" {
		testCtx.ClusterName = hostedClusterName
		testCtx.ClusterNamespace = hostedClusterNamespace
		testCtx.ControlPlaneNamespace = manifests.HostedControlPlaneNamespace(hostedClusterNamespace, hostedClusterName)
	}
	testCtx.ArtifactDir = artifactDir

	// AWS credentials file path from environment (optional, required only for AWS-specific tests)
	testCtx.awsCredentialsFile = GetEnvVarValue("AWS_CREDENTIALS_FILE")

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
