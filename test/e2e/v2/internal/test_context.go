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

	"github.com/blang/semver"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
	MgmtClient            crclient.Client
	ClusterName           string
	ClusterNamespace      string
	ControlPlaneNamespace string
	ArtifactDir           string
}

// getHostedCluster fetches the HostedCluster from the management cluster.
// Returns an error if no cluster name/namespace is configured or if the API
// call fails.
func (tc *TestContext) getHostedCluster() (*hyperv1.HostedCluster, error) {
	if tc.ClusterName == "" || tc.ClusterNamespace == "" {
		return nil, fmt.Errorf("no hosted cluster configured for this test run (E2E_HOSTED_CLUSTER_NAME and E2E_HOSTED_CLUSTER_NAMESPACE must be set)")
	}
	hostedCluster := &hyperv1.HostedCluster{}
	err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
		Namespace: tc.ClusterNamespace,
		Name:      tc.ClusterName,
	}, hostedCluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get HostedCluster %s/%s: %w", tc.ClusterNamespace, tc.ClusterName, err)
	}
	return hostedCluster, nil
}

func (tc *TestContext) getHostedClusterVersion() (*hyperv1.HostedCluster, semver.Version, error) {
	version := semver.Version{}
	hc, err := tc.getHostedCluster()
	if err != nil {
		return nil, version, err
	}
	if hc.Status.Version != nil && len(hc.Status.Version.History) > 0 && hc.Status.Version.History[0].Version != "" {
		releaseVersion, err := semver.Parse(hc.Status.Version.History[0].Version)
		if err != nil {
			return nil, version, fmt.Errorf("error parsing version: %w", err)
		}
		releaseVersion.Patch = 0
		releaseVersion.Pre = nil
		releaseVersion.Build = nil
		version = releaseVersion
	}
	return hc, version, nil
}

func (tc *TestContext) getHostedClusterRESTConfig(hc *hyperv1.HostedCluster) (*rest.Config, error) {
	var kubeconfigSecret corev1.Secret
	err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
		Namespace: hc.Namespace,
		Name:      hc.Status.KubeConfig.Name,
	}, &kubeconfigSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig secret %s/%s: %w", hc.Namespace, hc.Status.KubeConfig.Name, err)
	}

	kubeconfigData, ok := kubeconfigSecret.Data["kubeconfig"]
	if !ok || len(kubeconfigData) == 0 {
		return nil, fmt.Errorf("kubeconfig key not found or empty in secret %s/%s", hc.Namespace, hc.Status.KubeConfig.Name)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config from kubeconfig: %w", err)
	}
	restConfig.QPS = 200
	restConfig.Burst = 300

	return restConfig, nil
}

func (tc *TestContext) getHostedClusterClient(hc *hyperv1.HostedCluster) (crclient.Client, error) {
	restConfig, err := tc.getHostedClusterRESTConfig(hc)
	if err != nil {
		return nil, err
	}
	if restConfig == nil {
		return nil, fmt.Errorf("expected a REST config for hostedcluster")
	}
	client, err := crclient.New(restConfig, crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create hosted cluster client: %w", err)
	}
	return client, nil
}

// MustGetHostedCluster fetches the HostedCluster, failing the test via Expect
// on error. Do not call inside Eventually or other retry closures.
func (tc *TestContext) MustGetHostedCluster() *hyperv1.HostedCluster {
	GinkgoHelper()
	hc, err := tc.getHostedCluster()
	Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster %s/%s", tc.ClusterNamespace, tc.ClusterName)
	return hc
}

// VersionAtLeast returns true if the hosted cluster version is at least v.
// Fails the test if the version cannot be determined.
func (tc *TestContext) VersionAtLeast(v semver.Version) bool {
	GinkgoHelper()
	_, version, err := tc.getHostedClusterVersion()
	Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster version for %s/%s", tc.ClusterNamespace, tc.ClusterName)
	return !version.LT(v)
}

// MustGetHostedClusterClient returns a controller-runtime client for the hosted
// cluster, failing the test on error. Do not call inside retry closures.
func (tc *TestContext) MustGetHostedClusterClient() crclient.Client {
	GinkgoHelper()
	hc := tc.MustGetHostedCluster()
	client, err := tc.getHostedClusterClient(hc)
	Expect(err).NotTo(HaveOccurred(), "failed to get hosted cluster client for %s/%s", tc.ClusterNamespace, tc.ClusterName)
	return client
}

// MustGetHostedClusterRESTConfig returns the REST config for the hosted cluster,
// failing the test on error. Do not call inside retry closures.
func (tc *TestContext) MustGetHostedClusterRESTConfig() *rest.Config {
	GinkgoHelper()
	hc := tc.MustGetHostedCluster()
	cfg, err := tc.getHostedClusterRESTConfig(hc)
	Expect(err).NotTo(HaveOccurred(), "failed to get hosted cluster REST config for %s/%s", tc.ClusterNamespace, tc.ClusterName)
	return cfg
}

// SkipIfVersionBelow skips the test if the hosted cluster version is below
// minVersion. Returns the detected version on success.
func (tc *TestContext) SkipIfVersionBelow(minVersion semver.Version) semver.Version {
	GinkgoHelper()
	_, version, err := tc.getHostedClusterVersion()
	Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster version for %s/%s", tc.ClusterNamespace, tc.ClusterName)
	if version.LT(minVersion) {
		Skip(fmt.Sprintf("Only tested in %s and later", minVersion))
	}
	return version
}

// SkipIfNotPlatform skips the test unless the hosted cluster matches one of the
// given platforms.
func (tc *TestContext) SkipIfNotPlatform(platforms ...hyperv1.PlatformType) {
	GinkgoHelper()
	hc := tc.MustGetHostedCluster()
	for _, p := range platforms {
		if hc.Spec.Platform.Type == p {
			return
		}
	}
	Skip(fmt.Sprintf("test only applies to platforms %v, got %s", platforms, hc.Spec.Platform.Type))
}

// SkipIfPlatform skips the test if the hosted cluster matches any of the given
// platforms.
func (tc *TestContext) SkipIfPlatform(platforms ...hyperv1.PlatformType) {
	GinkgoHelper()
	hc := tc.MustGetHostedCluster()
	for _, p := range platforms {
		if hc.Spec.Platform.Type == p {
			Skip(fmt.Sprintf("test does not apply to platform %s", p))
		}
	}
}

var testCtx *TestContext

func GetTestContext() *TestContext {
	return testCtx
}

func SetTestContext(ctx *TestContext) {
	testCtx = ctx
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
	}
	testCtx.ArtifactDir = artifactDir

	return testCtx, nil
}
