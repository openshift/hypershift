package util

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	hcmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/metrics"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	npmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/metrics"
	conditionsUtil "github.com/openshift/hypershift/support/conditions"
	support "github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/gomega"
)

type hypershiftTestFunc func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster)
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
	artifactDir = filepath.Join(artifactDir, artifactSubdirFor(h.T))

	// create a hypershift cluster for the test
	hostedCluster := h.createHostedCluster(opts, platform, serviceAccountSigningKey, artifactDir)

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
		h.summarizeHCConditions(hostedCluster, opts)
	}
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
func (h *hypershiftTest) after(hostedCluster *hyperv1.HostedCluster, platform hyperv1.PlatformType) {
	if h.Failed() {
		// skip if Main failed
		return
	}
	h.Run("EnsureHostedCluster", func(t *testing.T) {
		hcpNs := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		EnsurePodsWithEmptyDirPVsHaveSafeToEvictAnnotations(t, context.Background(), h.client, hcpNs)
		EnsureAllContainersHavePullPolicyIfNotPresent(t, context.Background(), h.client, hostedCluster)
		EnsureHCPContainersHaveResourceRequests(t, context.Background(), h.client, hostedCluster)
		EnsureNoPodsWithTooHighPriority(t, context.Background(), h.client, hostedCluster)
		NoticePreemptionOrFailedScheduling(t, context.Background(), h.client, hostedCluster)
		EnsureAllRoutesUseHCPRouter(t, context.Background(), h.client, hostedCluster)
		EnsureNetworkPolicies(t, context.Background(), h.client, hostedCluster)
		if platform == hyperv1.AWSPlatform {
			EnsureHCPPodsAffinitiesAndTolerations(t, context.Background(), h.client, hostedCluster)
		}
		EnsureSATokenNotMountedUnlessNecessary(t, context.Background(), h.client, hostedCluster)
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
		teardownHostedCluster(h.T, context.Background(), hostedCluster, h.client, opts, artifactDir, cleanupPhase)
		return
	}

	h.Run("Teardown", func(t *testing.T) {
		teardownHostedCluster(t, h.ctx, hostedCluster, h.client, opts, artifactDir, cleanupPhase)
	})
}

func (h *hypershiftTest) postTeardown(hostedCluster *hyperv1.HostedCluster, opts *core.CreateOptions) {
	// don't run if test has already failed
	if h.Failed() {
		h.Logf("skipping postTeardown()")
		return
	}

	h.Run("PostTeardown", func(t *testing.T) {
		// All clusters created during tests should ultimately conform to our API
		// budget. This should be checked after deletion to ensure that API operations
		// for the full lifecycle are accounted for.
		if !opts.SkipAPIBudgetVerification {
			EnsureAPIBudget(t, h.ctx, h.client, hostedCluster)
		}

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

func (h *hypershiftTest) createHostedCluster(opts *core.CreateOptions, platform hyperv1.PlatformType, serviceAccountSigningKey []byte, artifactDir string) *hyperv1.HostedCluster {
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

	// Dump the output from rendering the cluster objects for posterity
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		h.Errorf("failed to create dump directory: %v", err)
	}

	// Try and create the cluster. If it fails, mark test as failed and return.
	opts.Render = false
	opts.RenderInto = ""
	h.Logf("Creating a new cluster. Options: %v", opts)
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

// NOTE: teardownHostedCluster shouldn't start any subtests with t.Run() when cleanupPhase=True, this is not a supported operation and will fail immediately
func teardownHostedCluster(t *testing.T, ctx context.Context, hc *hyperv1.HostedCluster, client crclient.Client, opts *core.CreateOptions, artifactDir string, cleanupPhase bool) {
	// TODO (Mulham): dumpCluster() uses testName to construc dumpDir, since we removed sub tests from this function
	// we should pass dumpDir to the dumpCluster() as <artifactDir>/<testName>_<suffix>
	dumpCluster := newClusterDumper(hc, opts, artifactDir)

	// First, do a dump of the cluster before tearing it down
	// Save off any error so that we can continue with the teardown
	dumpErr := dumpCluster(ctx, t, true)

	if !cleanupPhase && !t.Failed() {
		ValidateMetrics(t, ctx, hc, []string{
			hcmetrics.SilenceAlertsMetricName,
			hcmetrics.LimitedSupportEnabledMetricName,
			hcmetrics.ProxyMetricName,
			hcmetrics.InvalidAwsCredsMetricName,
			HypershiftOperatorInfoName,
			npmetrics.SizeMetricName,
			npmetrics.AvailableReplicasMetricName,
		}, true)
	}

	// Try repeatedly to destroy the cluster gracefully. For each failure, dump
	// the current cluster to help debug teardown lifecycle issues.
	destroyAttempt := 1
	t.Logf("Waiting for cluster to be destroyed. Namespace: %s, name: %s", hc.Namespace, hc.Name)
	err := wait.PollUntilContextCancel(ctx, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		err := destroyCluster(ctx, t, hc, opts)
		if err != nil {
			if strings.Contains(err.Error(), "required inputs are missing") {
				return false, err
			}
			if strings.Contains(err.Error(), "NoCredentialProviders") {
				return false, err
			}
			t.Logf("Failed to destroy cluster, will retry: %v", err)
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
}

func (h *hypershiftTest) summarizeHCConditions(hostedCluster *hyperv1.HostedCluster, opts *core.CreateOptions) {
	conditions := hostedCluster.Status.Conditions
	expectedConditions := conditionsUtil.ExpectedHCConditions()

	if hostedCluster.Spec.SecretEncryption == nil || hostedCluster.Spec.SecretEncryption.KMS == nil || hostedCluster.Spec.SecretEncryption.KMS.AWS == nil {
		// AWS KMS is not configured
		expectedConditions[hyperv1.ValidAWSKMSConfig] = metav1.ConditionUnknown
	} else {
		expectedConditions[hyperv1.ValidAWSKMSConfig] = metav1.ConditionTrue
	}

	kasExternalHostname := support.ServiceExternalDNSHostnameByHC(hostedCluster, hyperv1.APIServer)
	if kasExternalHostname == "" {
		// ExternalDNS is not configured
		expectedConditions[hyperv1.ExternalDNSReachable] = metav1.ConditionUnknown
	} else {
		expectedConditions[hyperv1.ExternalDNSReachable] = metav1.ConditionTrue
	}
	if opts.NodePoolReplicas*int32(len(opts.AWSPlatform.Zones)) < 1 {
		expectedConditions[hyperv1.ClusterVersionAvailable] = metav1.ConditionFalse
		expectedConditions[hyperv1.ClusterVersionSucceeding] = metav1.ConditionFalse
		expectedConditions[hyperv1.ClusterVersionProgressing] = metav1.ConditionTrue
	}

	if hostedCluster.Spec.Platform.Type == hyperv1.KubevirtPlatform &&
		hostedCluster.Spec.Networking.NetworkType == hyperv1.OVNKubernetes {
		expectedConditions[hyperv1.ValidKubeVirtInfraNetworkMTU] = metav1.ConditionTrue
	}

	if conditions != nil {
		h.Logf("Summarizing unexpected conditions for HostedCluster %s ", hostedCluster.Name)
		for _, condition := range conditions {
			expectedStatus, known := expectedConditions[hyperv1.ConditionType(condition.Type)]
			if !known {
				h.Logf("Unknown condition %s", condition.Type)
				continue
			}

			if condition.Status != expectedStatus {
				msg := fmt.Sprintf("%s, Reason: %s", condition.Type, condition.Reason)
				if condition.Message != "" {
					msg += fmt.Sprintf(", Message: %s", condition.Message)
				}
				h.Logf(msg)
			}
		}
	} else {
		h.Logf("HostedCluster %s has no conditions", hostedCluster.Name)
	}
}
