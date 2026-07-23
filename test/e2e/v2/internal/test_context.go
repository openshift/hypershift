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

	. "github.com/onsi/ginkgo/v2"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	hyperapi "github.com/openshift/hypershift/support/api"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type TestContextGetter func() *TestContext

type TestContext struct {
	context.Context
	MgmtClient                  crclient.Client
	ClusterName                 string
	ClusterNamespace            string
	ControlPlaneNamespace       string
	ArtifactDir                 string
	HostedClusterConfigured     bool
	hostedCluster               *hyperv1.HostedCluster
	hostedClusterOnce           sync.Once
	hostedClusterClient         crclient.Client
	hostedClusterClientOnce     sync.Once
	hostedClusterRESTConfig     *rest.Config
	hostedClusterRESTConfigOnce sync.Once
}

// GetHostedCluster returns the HostedCluster associated with this test context.
// The result is cached by sync.Once — callers must ensure the cluster is ready before the first call.
// Returns nil if ClusterName or ClusterNamespace are not set.
// Panics if the HostedCluster fetch or release version extraction fails.
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

// getHostedClusterRESTConfig fetches the kubeconfig secret and returns a REST config.
// Returns nil if the HostedCluster is not available or its KubeConfig status is not set.
// Panics on any other failure.
func (tc *TestContext) getHostedClusterRESTConfig() *rest.Config {
	hc := tc.GetHostedCluster()
	if hc == nil || hc.Status.KubeConfig == nil {
		return nil
	}

	var kubeconfigSecret corev1.Secret
	err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
		Namespace: hc.Namespace,
		Name:      hc.Status.KubeConfig.Name,
	}, &kubeconfigSecret)
	if err != nil {
		panic(fmt.Sprintf("failed to get kubeconfig secret %s/%s: %v", hc.Namespace, hc.Status.KubeConfig.Name, err))
	}

	kubeconfigData, ok := kubeconfigSecret.Data["kubeconfig"]
	if !ok || len(kubeconfigData) == 0 {
		panic(fmt.Sprintf("kubeconfig key not found or empty in secret %s/%s", hc.Namespace, hc.Status.KubeConfig.Name))
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
	if err != nil {
		panic(fmt.Sprintf("failed to create REST config from kubeconfig: %v", err))
	}
	restConfig.QPS = 200
	restConfig.Burst = 300

	return restConfig
}

// GetHostedClusterClient returns a controller-runtime client for the hosted cluster.
// The result is cached by sync.Once — callers must ensure the cluster is ready before the first call.
// Returns nil if the HostedCluster is not available or its KubeConfig status is not set.
// Panics on any other initialization failure.
func (tc *TestContext) GetHostedClusterClient() crclient.Client {
	tc.hostedClusterClientOnce.Do(func() {
		restConfig := tc.GetHostedClusterRESTConfig()
		if restConfig == nil {
			return
		}
		client, err := crclient.New(restConfig, crclient.Options{Scheme: hyperapi.Scheme})
		if err != nil {
			panic(fmt.Sprintf("failed to create hosted cluster client: %v", err))
		}
		tc.hostedClusterClient = client
	})
	return tc.hostedClusterClient
}

// GetHostedClusterRESTConfig returns the raw REST config for the hosted cluster.
// The result is cached by sync.Once — callers must ensure the cluster is ready before the first call.
// Returns nil if the HostedCluster is not available or its KubeConfig status is not set.
// Panics on any other initialization failure.
func (tc *TestContext) GetHostedClusterRESTConfig() *rest.Config {
	tc.hostedClusterRESTConfigOnce.Do(func() {
		tc.hostedClusterRESTConfig = tc.getHostedClusterRESTConfig()
	})
	return tc.hostedClusterRESTConfig
}

var (
	testCtx     *TestContext
	testCtxInit sync.Once
)

// GetTestContext returns the global test context. On first call, if no context
// was set via SetTestContext (e.g. in OTE mode where BeforeSuite is stripped),
// it lazy-initializes from environment variables.
func GetTestContext() *TestContext {
	testCtxInit.Do(func() {
		if testCtx != nil {
			return
		}
		tc, err := SetupTestContextFromEnv(context.Background())
		if err != nil {
			panic(fmt.Sprintf("lazy-init test context from env: %v", err))
		}
		testCtx = tc
	})
	return testCtx
}

func SetTestContext(ctx *TestContext) {
	testCtx = ctx
}

func SetupTestContext(ctx context.Context, hostedClusterName, hostedClusterNamespace string) (*TestContext, error) {
	mgmtClient, err := e2eutil.GetClient()
	if err != nil {
		return nil, fmt.Errorf("failed to get management client: %w", err)
	}

	testCtx := &TestContext{
		Context:                 ctx,
		MgmtClient:              mgmtClient,
		ClusterName:             hostedClusterName,
		ClusterNamespace:        hostedClusterNamespace,
		ControlPlaneNamespace:   manifests.HostedControlPlaneNamespace(hostedClusterNamespace, hostedClusterName),
		HostedClusterConfigured: hostedClusterName != "" && hostedClusterNamespace != "",
	}

	return testCtx, nil
}

// SetupTestContextFromEnv initializes the test context from environment variables.
// It reads E2E_HOSTED_CLUSTER_NAME and E2E_HOSTED_CLUSTER_NAMESPACE from the environment.
// If these are not set, it creates a basic context with only the management client.
func SetupTestContextFromEnv(ctx context.Context) (*TestContext, error) {
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

	if hostedClusterName != "" && hostedClusterNamespace != "" {
		testCtx.ClusterName = hostedClusterName
		testCtx.ClusterNamespace = hostedClusterNamespace
		testCtx.ControlPlaneNamespace = manifests.HostedControlPlaneNamespace(hostedClusterNamespace, hostedClusterName)
		testCtx.HostedClusterConfigured = true
	}
	testCtx.ArtifactDir = artifactDir

	return testCtx, nil
}

// ValidateHostedCluster skips the test if no hosted cluster was configured for this run.
// Panics if a hosted cluster was configured but cannot be fetched.
func (tc *TestContext) ValidateHostedCluster() {
	if !tc.HostedClusterConfigured {
		Skip("no hosted cluster configured for this test run")
	}
	tc.GetHostedCluster()
}

// ValidateHostedClusterClient skips the test if no hosted cluster was configured.
// Panics if the hosted cluster client cannot be initialized.
func (tc *TestContext) ValidateHostedClusterClient() {
	tc.ValidateHostedCluster()
	if tc.GetHostedClusterClient() == nil {
		panic("hosted cluster client not available — kubeconfig may not be ready")
	}
}
