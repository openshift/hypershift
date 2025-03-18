package util

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

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/aws"
	"github.com/openshift/hypershift/cmd/cluster/azure"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/kubevirt"
	"github.com/openshift/hypershift/cmd/cluster/none"
	"github.com/openshift/hypershift/cmd/cluster/openstack"
	"github.com/openshift/hypershift/cmd/cluster/powervs"
	hcmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/metrics"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	npmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/metrics"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type PlatformAgnosticOptions struct {
	core.RawCreateOptions

	NonePlatform      none.RawCreateOptions
	AWSPlatform       aws.RawCreateOptions
	KubevirtPlatform  kubevirt.RawCreateOptions
	AzurePlatform     azure.RawCreateOptions
	PowerVSPlatform   powervs.RawCreateOptions
	OpenStackPlatform openstack.RawCreateOptions
}

type (
	hypershiftTestFunc func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster)
	hypershiftTest     struct {
		*testing.T
		ctx    context.Context
		client crclient.Client

		test hypershiftTestFunc

		hasBeenTornedDown bool
	}
)

func NewHypershiftTest(t *testing.T, ctx context.Context, test hypershiftTestFunc) *hypershiftTest {
	client, err := GetClient()
	if err != nil {
		t.Fatalf("failed to get k8s client: %v", err)
	}

	return &hypershiftTest{
		T:      t,
		ctx:    ctx,
		client: client,
		test:   test,
	}
}

func (h *hypershiftTest) Execute(opts *PlatformAgnosticOptions, platform hyperv1.PlatformType, artifactDir, name string, serviceAccountSigningKey []byte) {
	artifactDir = filepath.Join(artifactDir, artifactSubdirFor(h.T))

	// create a hypershift cluster for the test
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
		h.postTeardown(hostedCluster, opts)
	}()

	// fail safe to guarantee teardown() is always executed.
	// defer funcs will be skipped if any subtest panics
	h.Cleanup(func() { h.teardown(hostedCluster, opts, artifactDir, true) })

	// validate cluster is operational
	h.before(hostedCluster, opts, platform)

	if h.test != nil && !h.Failed() {
		h.Run("Main", func(t *testing.T) {
			h.test(t, NewWithT(t), h.client, hostedCluster)
		})
	}

	h.after(hostedCluster, platform)

	if h.Failed() {
		numNodes := opts.NodePoolReplicas * int32(len(opts.AWSPlatform.Zones))
		h.Logf("Summarizing unexpected conditions for HostedCluster %s ", hostedCluster.Name)
		validateHostedClusterConditions(h.T, h.ctx, h.client, hostedCluster, numNodes > 0, 2*time.Second)
	}
}

// runs before each test.
func (h *hypershiftTest) before(hostedCluster *hyperv1.HostedCluster, opts *PlatformAgnosticOptions, platform hyperv1.PlatformType) {
	h.Run("ValidateHostedCluster", func(t *testing.T) {
		if platform != hyperv1.NonePlatform {
			if opts.AWSPlatform.EndpointAccess == string(hyperv1.Private) {
				ValidatePrivateCluster(t, h.ctx, h.client, hostedCluster, opts)
			} else {
				ValidatePublicCluster(t, h.ctx, h.client, hostedCluster, opts)
			}
		}
	})
}

// runs after each test.
func (h *hypershiftTest) after(hostedCluster *hyperv1.HostedCluster, platform hyperv1.PlatformType) {
	if h.Failed() {
		// skip if Main failed
		return
	}
	h.Run("EnsureHostedCluster", func(t *testing.T) {
		hcpNs := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		EnsurePayloadArchSetCorrectly(t, context.Background(), h.client, hostedCluster)
		EnsurePodsWithEmptyDirPVsHaveSafeToEvictAnnotations(t, context.Background(), h.client, hcpNs)
		EnsureAllContainersHavePullPolicyIfNotPresent(t, context.Background(), h.client, hostedCluster)
		EnsureHCPContainersHaveResourceRequests(t, context.Background(), h.client, hostedCluster)
		EnsureNoPodsWithTooHighPriority(t, context.Background(), h.client, hostedCluster)
		EnsureNoRapidDeploymentRollouts(t, context.Background(), h.client, hostedCluster)
		NoticePreemptionOrFailedScheduling(t, context.Background(), h.client, hostedCluster)
		EnsureAllRoutesUseHCPRouter(t, context.Background(), h.client, hostedCluster)
		EnsureNetworkPolicies(t, context.Background(), h.client, hostedCluster)
		if platform == hyperv1.AWSPlatform {
			EnsureHCPPodsAffinitiesAndTolerations(t, context.Background(), h.client, hostedCluster)
		}
		EnsureSATokenNotMountedUnlessNecessary(t, context.Background(), h.client, hostedCluster)
		// HCCO installs the admission policies, however, NonePlatform clusters can be ready before
		// the HCCO is fully up and reconciling, resulting in a potential race and flaky test assertions.
		if platform != hyperv1.NonePlatform {
			EnsureAdmissionPolicies(t, context.Background(), h.client, hostedCluster)
		}
		ValidateMetrics(t, context.Background(), hostedCluster, []string{
			hcmetrics.SilenceAlertsMetricName,
			hcmetrics.LimitedSupportEnabledMetricName,
			hcmetrics.ProxyMetricName,
			hcmetrics.InvalidAwsCredsMetricName,
			HypershiftOperatorInfoName,
			npmetrics.SizeMetricName,
			npmetrics.AvailableReplicasMetricName,
		}, true)
	})
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

	h.Run("Teardown", func(t *testing.T) {
		teardownHostedCluster(t, h.ctx, hostedCluster, h.client, opts, artifactDir)
	})
}

func (h *hypershiftTest) postTeardown(hostedCluster *hyperv1.HostedCluster, opts *PlatformAgnosticOptions) {
	// don't run if test has already failed
	if h.Failed() {
		h.Logf("skipping postTeardown()")
		return
	}

	h.Run("PostTeardown", func(t *testing.T) {
		ValidateMetrics(t, h.ctx, hostedCluster, []string{
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
	})
}

func (h *hypershiftTest) createHostedCluster(opts *PlatformAgnosticOptions, platform hyperv1.PlatformType, serviceAccountSigningKey []byte, name, artifactDir string) *hyperv1.HostedCluster {
	g := NewWithT(h.T)
	start := time.Now()

	// Set up a namespace to contain the hostedcluster.
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: SimpleNameGenerator.GenerateName("e2e-clusters-"),
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
			}
		}
	}

    tempHc := &hyperv1.HostedCluster{}
    opts.BeforeApply(tempHc)
    if tempHc.Spec.Configuration != nil && tempHc.Spec.Configuration.Authentication != nil && tempHc.Spec.Configuration.Authentication.Type == configv1.AuthenticationTypeOIDC {
        // do oidc setup
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-provider-ca",
				Namespace: namespace.Name,
			},
			Data: map[string]string{
				"ca-bundle.crt": "ca-bundle contents",
			},
		}

		err := h.client.Create(h.ctx, cm)
        g.Expect(err).NotTo(HaveOccurred(), "failed to create OIDC provider-ca ConfigMap")
    }


	// Build the skeletal HostedCluster based on the provided platform.
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      SimpleNameGenerator.GenerateName(fmt.Sprintf("%s-", name)),
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: platform,
			},
		},
	}

	// Build options specific to the platform.
	opts, err = createClusterOpts(h.ctx, h.client, hc, opts)
	g.Expect(err).NotTo(HaveOccurred(), "failed to generate platform specific cluster options")

	// Dump the output from rendering the cluster objects for posterity
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		h.Errorf("failed to create dump directory: %v", err)
	}

	// Try and create the cluster. If it fails, mark test as failed and return.
	opts.Render = false
	opts.RenderInto = ""
	if err := createCluster(h.ctx, hc, opts, artifactDir); err != nil {
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
	dumpCluster := newClusterDumper(hc, opts, artifactDir)

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
		err := destroyCluster(ctx, t, hc, opts, artifactDir)
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
	err = DeleteNamespace(t, deleteTimeout, client, hc.Namespace)
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
		deleteIngressRoute53Records(t, ctx, hc, opts)
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
