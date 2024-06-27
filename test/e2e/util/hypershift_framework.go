package util

import (
	"context"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/gomega"
)

type hypershiftTestFunc func(t *testing.T, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster)
type hypershiftTest struct {
	*testing.T
	ctx    context.Context
	client crclient.Client

	test hypershiftTestFunc

	hasBeenTornedDown bool
}

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

func (h *hypershiftTest) Execute(opts *core.CreateOptions, platform hyperv1.PlatformType, artifactDir string, serviceAccountSigningKey []byte) {
	// create a hypershift cluster for the test
	hostedCluster := h.createHostedCluster(opts, platform, artifactDir, serviceAccountSigningKey)
	// if cluster creation failed, immediately try and clean up.
	if h.Failed() {
		h.teardown(hostedCluster, opts, artifactDir, false)
		return
	}

	defer func() {
		if err := recover(); err != nil {
			// on a panic, print error and mark test as failed so postTeardown() is skipped
			// panics from subtests can't be caught by this.
			h.Errorf(string(debug.Stack()))
		}

		h.teardown(hostedCluster, opts, artifactDir, false)
		h.postTeardown(hostedCluster, opts)
	}()

	// fail safe to gurantee teardown() is always executed.
	// defer funcs will be skipped if any subtest panics
	h.Cleanup(func() { h.teardown(hostedCluster, opts, artifactDir, true) })

	// validate cluster is operational
	h.before(hostedCluster, opts, platform)

	if h.test != nil && !h.Failed() {
		h.Run("Main", func(t *testing.T) {
			h.test(t, h.client, hostedCluster)
		})
	}

	h.after(hostedCluster, opts, platform)
}

// runs before each test.
func (h *hypershiftTest) before(hostedCluster *hyperv1.HostedCluster, opts *core.CreateOptions, platform hyperv1.PlatformType) {
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
func (h *hypershiftTest) after(hostedCluster *hyperv1.HostedCluster, opts *core.CreateOptions, platform hyperv1.PlatformType) {
	if h.Failed() {
		// skip if Main failed
		return
	}
	h.Run("EnsureHostedCluster", func(t *testing.T) {
		hcpNs := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name

		EnsurePodsWithEmptyDirPVsHaveSafeToEvictAnnotations(t, context.Background(), h.client, hcpNs)
		EnsureAllContainersHavePullPolicyIfNotPresent(t, context.Background(), h.client, hostedCluster)
		EnsureHCPContainersHaveResourceRequests(t, context.Background(), h.client, hostedCluster)
		EnsureNoPodsWithTooHighPriority(t, context.Background(), h.client, hostedCluster)
		NoticePreemptionOrFailedScheduling(t, context.Background(), h.client, hostedCluster)
		EnsureAllRoutesUseHCPRouter(t, context.Background(), h.client, hostedCluster)
		EnsureNetworkPolicies(t, context.Background(), h.client, hostedCluster)
	})
}

func (h *hypershiftTest) teardown(hostedCluster *hyperv1.HostedCluster, opts *core.CreateOptions, artifactDir string, cleanupPhase bool) {
	if h.hasBeenTornedDown {
		// teardown was already called
		h.Logf("skipping teardown, already called")
		return
	}
	h.hasBeenTornedDown = true

	// t.Run() is not supported in cleanup phase
	if cleanupPhase {
		h.teardownHostedCluster(context.Background(), hostedCluster, opts, artifactDir)
		return
	}

	h.Run("Teardown", func(t *testing.T) {
		h.teardownHostedCluster(context.Background(), hostedCluster, opts, artifactDir)
	})
}

func (h *hypershiftTest) postTeardown(hostedCluster *hyperv1.HostedCluster, opts *core.CreateOptions) {
	// don't run if test has already failed
	if h.Failed() {
		h.Logf("skipping postTeardown()")
		return
	}

	h.Run("PostTeardown", func(t *testing.T) {
		ValidateMetrics(t, h.ctx, hostedCluster)
	})
}

func (h *hypershiftTest) createHostedCluster(opts *core.CreateOptions, platform hyperv1.PlatformType, artifactDir string, serviceAccountSigningKey []byte) *hyperv1.HostedCluster {
	h.Logf("createHostedCluster()")

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
			}
		}
	}

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
	opts, err = createClusterOpts(h.ctx, h.client, hc, opts)
	g.Expect(err).NotTo(HaveOccurred(), "failed to generate platform specific cluster options")

	// Try and create the cluster. If it fails, mark test as failed and return.
	h.Logf("Creating a new cluster. Options: %v", opts)
	if err := createCluster(h.ctx, hc, opts); err != nil {
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

// NOTE: teardownHostedCluster shouldn't include any subtests
// as it is called during the cleanup phase where t.Run() is not supported.
func (h *hypershiftTest) teardownHostedCluster(ctx context.Context, hc *hyperv1.HostedCluster, opts *core.CreateOptions, artifactDir string) {
	// TODO (Mulham): dumpCluster() uses testName to construc dumpDir, since we removed sub tests from this function
	// we should pass dumpDir to the dumpCluster() as <artifactDir>/<testName>_<suffix>
	dumpCluster := newClusterDumper(hc, opts, artifactDir)

	// First, do a dump of the cluster before tearing it down
	err := dumpCluster(ctx, h.T, true)
	if err != nil {
		h.Errorf("Failed to dump cluster: %v", err)
	}

	// Try repeatedly to destroy the cluster gracefully. For each failure, dump
	// the current cluster to help debug teardown lifecycle issues.
	destroyAttempt := 1
	h.Logf("Waiting for cluster to be destroyed. Namespace: %s, name: %s", hc.Namespace, hc.Name)
	err = wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
		err := destroyCluster(ctx, h.T, hc, opts)
		if err != nil {
			if strings.Contains(err.Error(), "required inputs are missing") {
				return false, err
			}
			if strings.Contains(err.Error(), "NoCredentialProviders") {
				return false, err
			}
			h.Logf("Failed to destroy cluster, will retry: %v", err)
			err := dumpCluster(ctx, h.T, false)
			if err != nil {
				h.Logf("Failed to dump cluster during destroy; this is nonfatal: %v", err)
			}
			destroyAttempt++
			return false, nil
		}
		return true, nil
	}, ctx.Done())
	if err != nil {
		h.Errorf("Failed to destroy cluster: %v", err)
	} else {
		h.Logf("Destroyed cluster. Namespace: %s, name: %s", hc.Namespace, hc.Name)
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
	err = DeleteNamespace(h.T, deleteTimeout, h.client, hc.Namespace)
	if err != nil {
		h.Errorf("Failed to delete test namespace: %v", err)
		err := dumpCluster(ctx, h.T, false)
		if err != nil {
			h.Errorf("Failed to dump cluster: %v", err)
		}
	}
}
