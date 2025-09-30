//go:build e2e
// +build e2e

package framework

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/metrics"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	npmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/metrics"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type PlatformAgnosticOptions = e2eutil.PlatformAgnosticOptions

type hypershiftTestFunc func(t GinkgoTInterface, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster)
type hypershiftTest struct {
	GinkgoTInterface
	T      *testing.T // for compatibility with e2eutil functions
	ctx    context.Context
	client crclient.Client

	test hypershiftTestFunc

	hasBeenTornedDown bool
}

func NewHypershiftTest(t GinkgoTInterface, ctx context.Context, test hypershiftTestFunc) *hypershiftTest {
	client, err := e2eutil.GetClient()
	if err != nil {
		Fail(fmt.Sprintf("failed to get k8s client: %v", err))
	}

	return &hypershiftTest{
		GinkgoTInterface: t,
		ctx:              ctx,
		client:           client,
		test:             test,
	}
}

func (h *hypershiftTest) Execute(opts *PlatformAgnosticOptions, platform hyperv1.PlatformType, artifactDir, name string, serviceAccountSigningKey []byte) {
	artifactDir = filepath.Join(artifactDir, e2eutil.ArtifactSubdirFor(h.T))

	// create a hypershift cluster for the test
	By("creating hosted cluster")
	hostedCluster := h.createHostedCluster(opts, platform, serviceAccountSigningKey, name, artifactDir)

	// if cluster creation failed, immediately try and clean up.
	if h.Failed() {
		h.teardown(hostedCluster, opts, artifactDir, false)
		return
	}

	defer func() {
		if err := recover(); err != nil {
			// on a panic, print error and mark test as failed so postTeardown() is skipped
			// panics from subtests can't be caught by this.
			h.Errorf("%s", string(debug.Stack()))
		}

		h.teardown(hostedCluster, opts, artifactDir, false)
		h.postTeardown(hostedCluster, opts, platform)
	}()

	// fail safe to guarantee teardown() is always executed.
	// defer funcs will be skipped if any subtest panics
	DeferCleanup(func() { h.teardown(hostedCluster, opts, artifactDir, true) })

	// validate cluster is operational
	h.before(hostedCluster, opts, platform)

	if h.test != nil && !h.Failed() {
		By("running main test function")
		h.test(GinkgoT(), NewWithT(h.T), h.client, hostedCluster)
	}

	h.after(hostedCluster, platform)

	if h.Failed() {
		numNodes := opts.NodePoolReplicas * int32(len(opts.AWSPlatform.Zones))
		h.Logf("Summarizing unexpected conditions for HostedCluster %s ", hostedCluster.Name)
		e2eutil.ValidateHostedClusterConditions(h.T, h.ctx, h.client, hostedCluster, numNodes > 0, 2*time.Second)
	}
}

// runs before each test.
func (h *hypershiftTest) before(hostedCluster *hyperv1.HostedCluster, opts *PlatformAgnosticOptions, platform hyperv1.PlatformType) {
	By("validating hosted cluster")
	if platform != hyperv1.NonePlatform {
		if opts.AWSPlatform.EndpointAccess == string(hyperv1.Private) {
			By("validating private cluster configuration")
			e2eutil.ValidatePrivateCluster(h.T, h.ctx, h.client, hostedCluster, opts)
		} else {
			By("validating public cluster configuration")
			e2eutil.ValidatePublicCluster(h.T, h.ctx, h.client, hostedCluster, opts)
		}
	}

	if opts.ExtOIDCConfig != nil && opts.ExtOIDCConfig.ExternalOIDCProvider == e2eutil.ProviderKeycloak {
		By("validating authentication spec")
		e2eutil.ValidateAuthenticationSpec(h.T, h.ctx, h.client, hostedCluster, opts.ExtOIDCConfig)
	}
}

// runs after each test.
func (h *hypershiftTest) after(hostedCluster *hyperv1.HostedCluster, platform hyperv1.PlatformType) {
	if h.Failed() {
		// skip if Main failed
		return
	}

	hcpNs := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

	By("ensuring payload arch set correctly")
	e2eutil.EnsurePayloadArchSetCorrectly(h.T, context.Background(), h.client, hostedCluster)

	By("ensuring pods with emptyDir have safe-to-evict annotations")
	e2eutil.EnsurePodsWithEmptyDirPVsHaveSafeToEvictAnnotations(h.T, context.Background(), h.client, hcpNs)

	By("ensuring readonly root filesystem")
	e2eutil.EnsureReadOnlyRootFilesystem(h.T, context.Background(), h.client, hcpNs)

	By("ensuring all containers have PullPolicy IfNotPresent")
	e2eutil.EnsureAllContainersHavePullPolicyIfNotPresent(h.T, context.Background(), h.client, hostedCluster)

	By("ensuring all containers have termination message policy fallback to logs on error")
	e2eutil.EnsureAllContainersHaveTerminationMessagePolicyFallbackToLogsOnError(h.T, context.Background(), h.client, hostedCluster)

	By("ensuring HCP containers have resource requests")
	e2eutil.EnsureHCPContainersHaveResourceRequests(h.T, context.Background(), h.client, hostedCluster)

	By("ensuring no pods with too high priority")
	e2eutil.EnsureNoPodsWithTooHighPriority(h.T, context.Background(), h.client, hostedCluster)

	By("ensuring no rapid deployment rollouts")
	e2eutil.EnsureNoRapidDeploymentRollouts(h.T, context.Background(), h.client, hostedCluster)

	By("noticing preemption or failed scheduling")
	e2eutil.NoticePreemptionOrFailedScheduling(h.T, context.Background(), h.client, hostedCluster)

	By("ensuring all routes use HCP router")
	e2eutil.EnsureAllRoutesUseHCPRouter(h.T, context.Background(), h.client, hostedCluster)

	By("ensuring network policies")
	e2eutil.EnsureNetworkPolicies(h.T, context.Background(), h.client, hostedCluster)

	if platform == hyperv1.AWSPlatform {
		By("ensuring HCP pods affinities and tolerations")
		e2eutil.EnsureHCPPodsAffinitiesAndTolerations(h.T, context.Background(), h.client, hostedCluster)
	}

	By("ensuring SA token not mounted unless necessary")
	e2eutil.EnsureSATokenNotMountedUnlessNecessary(h.T, context.Background(), h.client, hostedCluster)

	// HCCO installs the admission policies, however, NonePlatform clusters can be ready before
	// the HCCO is fully up and reconciling, resulting in a potential race and flaky test assertions.
	if platform != hyperv1.NonePlatform {
		By("ensuring admission policies")
		e2eutil.EnsureAdmissionPolicies(h.T, context.Background(), h.client, hostedCluster)
	}

	if platform == hyperv1.AzurePlatform && azureutil.IsAroHCP() && !e2eutil.IsLessThan(e2eutil.Version420) {
		By("ensuring security context UID")
		e2eutil.EnsureSecurityContextUID(h.T, context.Background(), h.client, hostedCluster)
	}

	metricsToValidate := []string{hcmetrics.SilenceAlertsMetricName, // common metrics
		hcmetrics.LimitedSupportEnabledMetricName,
		hcmetrics.ProxyMetricName,
		e2eutil.HypershiftOperatorInfoName,
		npmetrics.SizeMetricName,
		npmetrics.AvailableReplicasMetricName,
	}
	AWSMetrics := []string{hcmetrics.InvalidAwsCredsMetricName}

	AzureMetrics := []string{
		hcmetrics.HostedClusterManagedAzureInfoMetricName,
		/* only Managed Azure ARO at the moment
		//hcmetrics.HostedClusterAzureInfoMetricName,
		*/
	}

	switch platform {
	case hyperv1.AWSPlatform:
		metricsToValidate = append(metricsToValidate, AWSMetrics...)
	case hyperv1.AzurePlatform:
		metricsToValidate = append(metricsToValidate, AzureMetrics...)
	}

	By("validating metrics")
	e2eutil.ValidateMetrics(h.T, context.Background(), h.client, hostedCluster, metricsToValidate, true)

	// TestHAEtcdChaos runs as NonePlatform and it's broken.
	// so skipping until we fix it.
	// TODO(alberto): consider drop this gate when we fix OCPBUGS-61291.
	if hostedCluster.Spec.Platform.Type != hyperv1.NonePlatform {
		// Private clusters may won't be reachable from the test runner; assume workers exist.
		hasWorkerNodes := true
		if !util.IsPrivateHC(hostedCluster) {
			By("waiting for guest client to list nodes")
			guestClient := e2eutil.WaitForGuestClient(h.T, h.T.Context(), h.client, hostedCluster)
			var nodeList corev1.NodeList
			if err := guestClient.List(h.T.Context(), &nodeList); err != nil {
				h.T.Errorf("failed to list nodes in guest cluster: %v", err)
			}
			hasWorkerNodes = len(nodeList.Items) > 0
		}
		By("validating hosted cluster conditions")
		e2eutil.ValidateHostedClusterConditions(h.T, h.T.Context(), h.client, hostedCluster, hasWorkerNodes, 10*time.Minute)
	}
}

func (h *hypershiftTest) teardown(hostedCluster *hyperv1.HostedCluster, opts *PlatformAgnosticOptions, artifactDir string, cleanupPhase bool) {
	if h.hasBeenTornedDown {
		// teardown was already called
		h.Logf("skipping teardown, already called")
		return
	}
	h.hasBeenTornedDown = true

	// t.Run() is not supported in cleanup phase
	if cleanupPhase {
		teardownHostedCluster(h.T, context.Background(), hostedCluster, h.client, opts, artifactDir)
		return
	}

	By("tearing down hosted cluster")
	teardownHostedCluster(h.T, h.ctx, hostedCluster, h.client, opts, artifactDir)
}

func (h *hypershiftTest) postTeardown(hostedCluster *hyperv1.HostedCluster, opts *PlatformAgnosticOptions, platform hyperv1.PlatformType) {
	// don't run if test has already failed
	if h.Failed() {
		h.Logf("skipping postTeardown()")
		return
	}

	By("validating post-teardown metrics")
	e2eutil.ValidateMetrics(h.T, h.ctx, h.client, hostedCluster, []string{
		hcmetrics.WaitingInitialAvailabilityDurationMetricName,
		hcmetrics.InitialRollingOutDurationMetricName,
		hcmetrics.UpgradingDurationMetricName,
		hcmetrics.SilenceAlertsMetricName,
		hcmetrics.LimitedSupportEnabledMetricName,
		hcmetrics.ProxyMetricName,
		hcmetrics.InvalidAwsCredsMetricName,
		hcmetrics.DeletingDurationMetricName,
		hcmetrics.GuestCloudResourcesDeletingDurationMetricName,
		npmetrics.InitialRollingOutDurationMetricName,
		npmetrics.SizeMetricName,
		npmetrics.AvailableReplicasMetricName,
		npmetrics.DeletingDurationMetricName,
	}, false)
}

func (h *hypershiftTest) createHostedCluster(opts *PlatformAgnosticOptions, platform hyperv1.PlatformType, serviceAccountSigningKey []byte, name, artifactDir string) *hyperv1.HostedCluster {
	g := NewWithT(h.T)
	start := time.Now()

	// Set up a namespace to contain the hostedcluster.
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: e2eutil.SimpleNameGenerator.GenerateName("e2e-clusters-"),
			Labels: map[string]string{
				"hypershift-e2e-component": "hostedclusters-namespace",
			},
		},
	}
	err := h.client.Create(h.ctx, namespace)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create namespace")

	// create serviceAccount signing key secret
	if len(serviceAccountSigningKey) > 0 {
		serviceAccountSigningKeySecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-sa-signing-key",
				Namespace: namespace.Name,
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				hyperv1.ServiceAccountSigningKeySecretKey: serviceAccountSigningKey,
			},
		}
		err = h.client.Create(h.ctx, serviceAccountSigningKeySecret)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create serviceAccountSigningKeySecret")

		// create external oidc secret and configmap
		if opts.ExtOIDCConfig != nil {
			consoleClientSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      opts.ExtOIDCConfig.ConsoleClientSecretName,
					Namespace: namespace.Name,
				},
				Type: corev1.SecretTypeOpaque,
				StringData: map[string]string{
					"clientSecret": opts.ExtOIDCConfig.ConsoleClientSecretValue,
				},
			}
			err := h.client.Create(h.ctx, consoleClientSecret)
			g.Expect(err).NotTo(HaveOccurred(), "failed to create external oidc secret")

			caData, err := os.ReadFile(opts.ExtOIDCConfig.IssuerCABundleFile)
			g.Expect(err).NotTo(HaveOccurred(), "failed to read external oidc issuer ca bundle file")

			oidcCAConfigmap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      opts.ExtOIDCConfig.IssuerCAConfigmapName,
					Namespace: namespace.Name,
				},
				Data: map[string]string{
					"ca-bundle.crt": string(caData),
				},
			}
			err = h.client.Create(h.ctx, oidcCAConfigmap)
			g.Expect(err).NotTo(HaveOccurred(), "failed to create external oidc issuer ca configmap")
		}

		originalBeforeApply := opts.BeforeApply
		opts.BeforeApply = func(o crclient.Object) {
			if originalBeforeApply != nil {
				originalBeforeApply(o)
			}

			switch v := o.(type) {
			case *hyperv1.HostedCluster:
				v.Spec.ServiceAccountSigningKey = &corev1.LocalObjectReference{
					Name: serviceAccountSigningKeySecret.Name,
				}
				if platform == hyperv1.AWSPlatform {
					if v.Spec.Configuration == nil {
						v.Spec.Configuration = &hyperv1.ClusterConfiguration{}
					}
					v.Spec.Configuration.Ingress = &configv1.IngressSpec{
						LoadBalancer: configv1.LoadBalancer{
							Platform: configv1.IngressPlatformSpec{
								Type: configv1.AWSPlatformType,
								AWS: &configv1.AWSIngressSpec{
									Type: configv1.NLB,
								},
							},
						},
					}
				}

				if opts.ExtOIDCConfig != nil {
					if v.Spec.Configuration == nil {
						v.Spec.Configuration = &hyperv1.ClusterConfiguration{}
					}
					v.Spec.Configuration.Authentication = opts.ExtOIDCConfig.GetAuthenticationConfig()
				}
			}
		}
	}

	// Build the skeletal HostedCluster based on the provided platform.
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      e2eutil.SimpleNameGenerator.GenerateName(fmt.Sprintf("%s-", name)),
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: platform,
			},
		},
	}

	// Build options specific to the platform.
	opts, err = e2eutil.CreateClusterOpts(h.ctx, h.client, hc, opts)
	g.Expect(err).NotTo(HaveOccurred(), "failed to generate platform specific cluster options")

	// Dump the output from rendering the cluster objects for posterity
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		h.Errorf("failed to create dump directory: %v", err)
	}

	// Try and create the cluster. If it fails, mark test as failed and return.
	opts.Render = false
	opts.RenderInto = ""
	if err := e2eutil.CreateCluster(h.ctx, hc, opts, artifactDir); err != nil {
		h.Errorf("failed to create cluster, tearing down: %v", err)
		return hc
	}

	// Assert we can retrieve the cluster that was created. If this smoke check
	// fails, mark test as failed and return.
	if err := h.client.Get(h.ctx, crclient.ObjectKeyFromObject(hc), hc); err != nil {
		h.Errorf("failed to get cluster that was created, tearing down: %v", err)
		return hc
	}

	// Everything went well
	h.Logf("Successfully created hostedcluster %s/%s in %s", hc.Namespace, hc.Name, time.Since(start).Round(time.Second))
	return hc
}

// NOTE: Do not use t.Run() here as this function can be called in the Cleanup context and will fail immediately
func teardownHostedCluster(t *testing.T, ctx context.Context, hc *hyperv1.HostedCluster, client crclient.Client, opts *PlatformAgnosticOptions, artifactDir string) {
	// TODO (Mulham): dumpCluster() uses testName to construct dumpDir, since we removed sub tests from this function
	// we should pass dumpDir to the dumpCluster() as <artifactDir>/<testName>_<suffix>
	dumpCluster := e2eutil.NewClusterDumper(hc, opts, artifactDir)

	defer func() {
		hostedClusterDir := filepath.Join(artifactDir, "hostedcluster-"+hc.Name)
		if _, err := os.Stat(hostedClusterDir); os.IsNotExist(err) {
			return
		}
		archiveFile := filepath.Join(artifactDir, "hostedcluster.tar.gz")
		t.Logf("archiving %s to %s", hostedClusterDir, archiveFile)
		if err := archive(t, hostedClusterDir, archiveFile); err != nil {
			t.Errorf("failed to archive hosted cluster content: %v", err)
		}
		if err := os.RemoveAll(hostedClusterDir); err != nil {
			t.Errorf("failed to remove hosted cluster directory: %v", err)
		}
	}()

	// First, do a dump of the cluster before tearing it down
	// Save off any error so that we can continue with the teardown
	dumpErr := dumpCluster(ctx, t, true)

	// Try repeatedly to destroy the cluster gracefully. For each failure, dump
	// the current cluster to help debug teardown lifecycle issues.
	destroyAttempt := 1
	if os.Getenv("EVENTUALLY_VERBOSE") != "false" {
		t.Logf("Waiting for HostedCluster %s/%s to be destroyed", hc.Namespace, hc.Name)
	}
	var previousError string
	err := wait.PollUntilContextCancel(ctx, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		err := e2eutil.DestroyCluster(ctx, t, hc, opts, artifactDir)
		if err != nil {
			if strings.Contains(err.Error(), "required inputs are missing") {
				return false, err
			}
			if strings.Contains(err.Error(), "NoCredentialProviders") {
				return false, err
			}
			if previousError != err.Error() {
				t.Logf("Failed to destroy cluster, will retry: %v", err)
				previousError = err.Error()
			}
			err := dumpCluster(ctx, t, false)
			if err != nil {
				t.Logf("Failed to dump cluster during destroy; this is nonfatal: %v", err)
			}
			destroyAttempt++
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Errorf("Failed to destroy cluster: %v", err)
	} else {
		t.Logf("Destroyed cluster. Namespace: %s, name: %s", hc.Namespace, hc.Name)
	}

	// Finally, delete the test namespace containing the HostedCluster/NodePool
	// resources.
	//
	// If the cluster was successfully destroyed and finalized, any further delay
	// in cleaning up the test namespace could be indicative of a resource
	// finalization bug. Give this namespace teardown a reasonable time to
	// complete and then dump resources to help debug.
	deleteTimeout, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()
	err = e2eutil.DeleteNamespace(t, deleteTimeout, client, hc.Namespace)
	if err != nil {
		t.Errorf("Failed to delete test namespace: %v", err)
		err := dumpCluster(ctx, t, false)
		if err != nil {
			t.Errorf("Failed to dump cluster: %v", err)
		}
	}

	if dumpErr != nil {
		t.Errorf("Failed to dump cluster: %v", dumpErr)
	}

	if hc.Spec.Platform.Type == hyperv1.OpenStackPlatform && opts.AWSPlatform.Credentials.AWSCredentialsFile != "" {
		e2eutil.DeleteIngressRoute53Records(t, ctx, hc, opts)
	}
}

// archive re-packs the dir into the destination
func archive(t *testing.T, srcDir, destArchive string) error {
	// we want the temporary file we use for output to be in the same directory as the real destination, so
	// we can be certain that our final os.Rename() call will not have to operate across a device boundary
	output, err := os.CreateTemp(filepath.Dir(destArchive), "tmp-archive")
	if err != nil {
		return fmt.Errorf("failed to create temporary file for archive: %w", err)
	}

	zipWriter := gzip.NewWriter(output)
	tarWriter := tar.NewWriter(zipWriter)
	defer func() {
		if err := tarWriter.Close(); err != nil {
			t.Logf("Could not close tar writer after archiving: %v.", err)
		}
		if err := zipWriter.Close(); err != nil {
			t.Logf("Could not close zip writer after archiving: %v.", err)
		}
		if err := output.Close(); err != nil {
			t.Logf("Could not close output file after archiving: %v.", err)
		}
	}()

	if err := filepath.Walk(srcDir, func(absPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Handle symlinks. See https://stackoverflow.com/a/40003617.
		var link string
		if info.Mode()&os.ModeSymlink == os.ModeSymlink {
			if link, err = os.Readlink(absPath); err != nil {
				return err
			}
		}

		// "link" is only used by FileInfoHeader if "info" here is a symlink.
		// See https://pkg.go.dev/archive/tar#FileInfoHeader.
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return fmt.Errorf("could not create tar header: %w", err)
		}
		// the header won't get nested paths right
		relpath, shouldNotErr := filepath.Rel(srcDir, absPath)
		if shouldNotErr != nil {
			t.Logf("filepath.Rel returned an error, but we assumed there must be a relative path between %s and %s: %v", srcDir, absPath, shouldNotErr)
		}
		header.Name = relpath
		if err := tarWriter.WriteHeader(header); err != nil {
			return fmt.Errorf("could not write tar header: %w", err)
		}
		if info.IsDir() {
			return nil
		}

		// Nothing more to do for non-regular files (symlinks).
		if !info.Mode().IsRegular() {
			return nil
		}

		file, err := os.Open(absPath)
		if err != nil {
			return fmt.Errorf("could not open source file: %w", err)
		}
		n, err := io.Copy(tarWriter, file)
		if err != nil {
			return fmt.Errorf("could not tar file: %w", err)
		}
		if n != info.Size() {
			return fmt.Errorf("only wrote %d bytes from %s; expected %d", n, absPath, info.Size())
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("could not close source file: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("could not walk source files to archive them: %w", err)
	}

	if err := os.Rename(output.Name(), destArchive); err != nil {
		return fmt.Errorf("could not overwrite archive: %w", err)
	}

	return nil
}
