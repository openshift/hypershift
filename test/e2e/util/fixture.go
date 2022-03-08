package util

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/cluster/aws"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/kubevirt"
	"github.com/openshift/hypershift/cmd/cluster/none"
	"github.com/openshift/hypershift/test/e2e/util/dump"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateCluster creates a new namespace and a HostedCluster in that namespace
// using the provided options.
//
// CreateCluster will install a teardown handler into the provided test T by
// calling T.Cleanup() with a function that destroys the cluster. This function
// will block until teardown completes. No explicit cluster cleanup logic is
// expected of the caller. Note that the teardown function explicitly ignores
// interruption and tries forever to do its work, the rationale being that we
// should do everything with can to release external resources with whatever
// time we have before being forcibly terminated.
//
// This function is intended (for now) to be the preferred default way of
// creating a hosted cluster during a test.
func CreateCluster(t *testing.T, ctx context.Context, client crclient.Client, opts *core.CreateOptions, platform hyperv1.PlatformType, artifactDir string) *hyperv1.HostedCluster {
	g := NewWithT(t)
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
	err := client.Create(ctx, namespace)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create namespace")

	// Build the skeletal HostedCluster based on the provided platform.
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      SimpleNameGenerator.GenerateName("example-"),
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: platform,
			},
		},
	}

	// Build options specific to the platform.
	opts = createClusterOpts(hc, opts)

	// Try and create the cluster. If it fails, immediately try and clean up.
	t.Logf("Creating a new cluster. Options: %v", opts)
	if err := createCluster(ctx, hc, opts); err != nil {
		t.Logf("failed to create cluster, tearing down: %v", err)
		teardown(context.Background(), t, client, hc, opts, artifactDir)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create cluster")
	}

	// Assert we can retrieve the cluster that was created. If this smoke check
	// fails, immediately try and clean up.
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(hc), hc); err != nil {
		t.Logf("failed to get cluster that was created, tearing down: %v", err)
		teardown(context.Background(), t, client, hc, opts, artifactDir)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
	}

	// Everything went well, so register the async cleanup handler and allow tests
	// to proceed.
	t.Logf("Successfully created hostedcluster %s/%s in %s", hc.Namespace, hc.Name, time.Since(start).Round(time.Second))
	t.Cleanup(func() { teardown(context.Background(), t, client, hc, opts, artifactDir) })

	return hc
}

// teardown will destroy the provided HostedCluster. If an artifact directory is
// provided, teardown will dump artifacts at various interesting points to aid
// in debugging.
//
// Note that most resource dumps are considered fatal to the tests. The reason
// is that these dumps are critical to our ability to debug issues in CI, and so
// we want to treat diagnostic dump failures as high priority bugs to resolve.
func teardown(ctx context.Context, t *testing.T, client crclient.Client, hc *hyperv1.HostedCluster, opts *core.CreateOptions, artifactDir string) {
	dumpCluster := newClusterDumper(hc, opts, artifactDir)

	// First, do a dump of the cluster before tearing it down
	t.Run("PreTeardownClusterDump", func(t *testing.T) {
		err := dumpCluster(ctx, t, true)
		if err != nil {
			t.Errorf("Failed to dump cluster: %v", err)
		}
	})

	// Try repeatedly to destroy the cluster gracefully. For each failure, dump
	// the current cluster to help debug teardown lifecycle issues.
	destroyAttempt := 1
	t.Run(fmt.Sprintf("DestroyCluster_%d", destroyAttempt), func(t *testing.T) {
		t.Logf("Waiting for cluster to be destroyed. Namespace: %s, name: %s", hc.Namespace, hc.Name)
		err := wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
			err := destroyCluster(ctx, hc, opts)
			if err != nil {
				t.Logf("Failed to destroy cluster, will retry: %v", err)
				err := dumpCluster(ctx, t, false)
				if err != nil {
					t.Logf("Failed to dump cluster during destroy; this is nonfatal: %v", err)
				}
				destroyAttempt++
				return false, nil
			}
			return true, nil
		}, ctx.Done())
		if err != nil {
			t.Errorf("Failed to destroy cluster: %v", err)
		} else {
			t.Logf("Destroyed cluster. Namespace: %s, name: %s", hc.Namespace, hc.Name)
		}
	})

	// All clusters created during tests should ultimately conform to our API
	// budget. This should be checked after deletion to ensure that API operations
	// for the full lifecycle are accounted for.
	EnsureAPIBudget(t, ctx, client, hc)

	// Finally, delete the test namespace containing the HostedCluster/NodePool
	// resources.
	//
	// If the cluster was successfully destroyed and finalized, any further delay
	// in cleaning up the test namespace could be indicative of a resource
	// finalization bug. Give this namespace teardown a reasonable time to
	// complete and then dump resources to help debug.
	t.Run("DeleteTestNamespace", func(t *testing.T) {
		deleteTimeout, cancel := context.WithTimeout(ctx, 1*time.Minute)
		defer cancel()
		err := DeleteNamespace(t, deleteTimeout, client, hc.Name)
		if err != nil {
			t.Errorf("Failed to delete test namespace: %v", err)
			err := dumpCluster(ctx, t, false)
			if err != nil {
				t.Errorf("Failed to dump cluster: %v", err)
			}
		}
	})
}

// createClusterOpts mutates the cluster creation options according to the
// cluster's platform as necessary to deal with options the test caller doesn't
// know or care about in advance.
//
// TODO: Mutates the input, instead should use a copy of the input options
func createClusterOpts(hc *hyperv1.HostedCluster, opts *core.CreateOptions) *core.CreateOptions {
	opts.Namespace = hc.Namespace
	opts.Name = hc.Name

	switch hc.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		opts.InfraID = hc.Name
	}

	return opts
}

// createCluster calls the correct cluster create CLI function based on the
// cluster platform.
func createCluster(ctx context.Context, hc *hyperv1.HostedCluster, opts *core.CreateOptions) error {
	switch hc.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		return aws.CreateCluster(ctx, opts)
	case hyperv1.NonePlatform:
		return none.CreateCluster(ctx, opts)
	case hyperv1.KubevirtPlatform:
		return kubevirt.CreateCluster(ctx, opts)
	default:
		return fmt.Errorf("unsupported platform")
	}
}

// destroyCluster calls the correct cluster destroy CLI function based on the
// cluster platform and the options used to create the cluster.
func destroyCluster(ctx context.Context, hc *hyperv1.HostedCluster, createOpts *core.CreateOptions) error {
	switch hc.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		opts := &core.DestroyOptions{
			Namespace: hc.Namespace,
			Name:      hc.Name,
			InfraID:   createOpts.InfraID,
			AWSPlatform: core.AWSPlatformDestroyOptions{
				BaseDomain:         createOpts.BaseDomain,
				AWSCredentialsFile: createOpts.AWSPlatform.AWSCredentialsFile,
				PreserveIAM:        false,
				Region:             createOpts.AWSPlatform.Region,
			},
			ClusterGracePeriod: 15 * time.Minute,
		}
		return aws.DestroyCluster(ctx, opts)
	case hyperv1.NonePlatform, hyperv1.KubevirtPlatform:
		opts := &core.DestroyOptions{
			Namespace:          hc.Namespace,
			Name:               hc.Name,
			ClusterGracePeriod: 15 * time.Minute,
		}
		return none.DestroyCluster(ctx, opts)
	default:
		return fmt.Errorf("unsupported cluster platform")
	}
}

// newClusterDumper returns a function that dumps important diagnostic data for
// a cluster based on the cluster's platform. The output directory will be named
// according to the test name. So, the returned dump function should be called
// at most once per unique test name.
func newClusterDumper(hc *hyperv1.HostedCluster, opts *core.CreateOptions, artifactDir string) func(ctx context.Context, t *testing.T, dumpGuestCluster bool) error {
	return func(ctx context.Context, t *testing.T, dumpGuestCluster bool) error {
		if len(artifactDir) == 0 {
			t.Logf("Skipping cluster dump because no artifact directory was provided")
			return nil
		}
		dumpDir := filepath.Join(artifactDir, strings.ReplaceAll(t.Name(), "/", "_"))

		switch hc.Spec.Platform.Type {
		case hyperv1.AWSPlatform:
			var dumpErrors []error
			err := dump.DumpMachineConsoleLogs(ctx, hc, opts.AWSPlatform.AWSCredentialsFile, dumpDir)
			if err != nil {
				t.Logf("Failed saving machine console logs; this is nonfatal: %v", err)
			}
			err = dump.DumpHostedCluster(ctx, hc, dumpGuestCluster, dumpDir)
			if err != nil {
				dumpErrors = append(dumpErrors, fmt.Errorf("failed to dump hosted cluster: %w", err))
			}
			err = dump.DumpJournals(t, ctx, hc, dumpDir, opts.AWSPlatform.AWSCredentialsFile)
			if err != nil {
				t.Logf("Failed to dump machine journals; this is nonfatal: %v", err)
			}
			return utilerrors.NewAggregate(dumpErrors)
		case hyperv1.NonePlatform, hyperv1.KubevirtPlatform:
			err := dump.DumpHostedCluster(ctx, hc, dumpGuestCluster, dumpDir)
			if err != nil {
				return fmt.Errorf("failed to dump hosted cluster: %w", err)
			}
			return nil
		default:
			return fmt.Errorf("unsupported cluster platform")
		}
	}
}

func NodePoolName(hcName, zone string) string {
	return fmt.Sprintf("%s-%s", hcName, zone)
}
