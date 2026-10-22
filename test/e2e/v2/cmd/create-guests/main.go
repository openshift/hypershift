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

// create-guests creates HostedClusters in parallel for v2 e2e
// lifecycle tests. The number and configuration of clusters is
// determined by the platform (HYPERSHIFT_PLATFORM env var).
// It shells out to the hypershift CLI for cluster creation, runs
// platform-specific post-create hooks, then uses controller-runtime
// watches to wait for Available condition and version rollout
// completion. Cluster names are derived deterministically from
// PROW_JOB_ID and written to SHARED_DIR for downstream CI steps.
// JUnit XML is emitted to ARTIFACT_DIR on rollout failure.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	toolswatch "k8s.io/client-go/tools/watch"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(hyperv1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(routev1.AddToScheme(scheme))
}

const defaultNamespace = "clusters"

// envConfig captures the common environment configuration.
type envConfig struct {
	testPlanPath string
	prowJobID    string
	sharedDir    string
	artifactDir  string
	releaseImage string
	n1Image      string

	baseDomain        string
	nodeCount         int
	namespace         string
	externalDNS       string
	etcdSC            string
	pullSecret        string
	sharedOIDCIssuer  string
	sharedOIDCKeyFile string
	sharedOIDCOptions lifecycle.SharedOIDCOptions

	platform         lifecycle.PlatformConfig
	hypershiftBinary string
	waitTimeout      time.Duration
}

func main() {
	cfg := loadEnvConfig()

	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalContext, cfg.waitTimeout+10*time.Minute)
	defer cancel()

	if err := run(ctx, cfg); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

func loadEnvConfig() envConfig {
	sharedDir := mustGetenv("SHARED_DIR")

	platform, err := lifecycle.NewPlatformConfig(os.Getenv("HYPERSHIFT_PLATFORM"), sharedDir)
	if err != nil {
		log.Fatalf("Failed to initialize platform config: %v", err)
	}

	cfg := envConfig{
		testPlanPath: os.Getenv("TEST_PLAN"),
		prowJobID:    mustGetenv("PROW_JOB_ID"),
		sharedDir:    sharedDir,
		artifactDir:  mustGetenv("ARTIFACT_DIR"),
		releaseImage: os.Getenv("RELEASE_IMAGE_LATEST"),
		n1Image:      os.Getenv("OCP_IMAGE_N1"),

		baseDomain:        envOrDefault("HYPERSHIFT_BASE_DOMAIN", platform.DefaultBaseDomain()),
		nodeCount:         envOrDefaultInt("HYPERSHIFT_NODE_COUNT", 3),
		namespace:         envOrDefault("HYPERSHIFT_NAMESPACE", defaultNamespace),
		externalDNS:       os.Getenv("HYPERSHIFT_EXTERNAL_DNS_DOMAIN"),
		etcdSC:            os.Getenv("HYPERSHIFT_ETCD_STORAGE_CLASS"),
		pullSecret:        envOrDefault("PULL_SECRET", "/etc/ci-pull-credentials/.dockerconfigjson"),
		sharedOIDCIssuer:  os.Getenv("E2E_AWS_SHARED_OIDC_ISSUER_URL"),
		sharedOIDCKeyFile: os.Getenv("E2E_AWS_SHARED_OIDC_SIGNING_KEY_FILE"),
		sharedOIDCOptions: lifecycle.SharedOIDCOptions{
			Bucket:             os.Getenv("AWS_OIDC_S3_BUCKET_NAME"),
			IAMCredentialsFile: os.Getenv("AWS_GUEST_INFRA_CREDENTIALS_FILE"),
			IAMRegion:          envOrDefault("HYPERSHIFT_AWS_REGION", "us-east-1"),
			S3CredentialsFile:  os.Getenv("AWS_OIDC_S3_CREDENTIALS_FILE"),
			S3Region:           os.Getenv("AWS_OIDC_S3_REGION"),
			ProwJobID:          os.Getenv("PROW_JOB_ID"),
		},

		platform:         platform,
		hypershiftBinary: envOrDefault("HYPERSHIFT_BINARY", "hypershift"),
		waitTimeout:      45 * time.Minute,
	}

	if cfg.n1Image == "" {
		cfg.n1Image = cfg.releaseImage
	}

	return cfg
}

func run(ctx context.Context, cfg envConfig) error {
	return runWithManagementClient(ctx, cfg, newMgmtClient)
}

func runWithManagementClient(ctx context.Context, cfg envConfig, newClient func() (crclient.WithWatch, error)) error {
	if err := validateCreateState(cfg); err != nil {
		return err
	}
	cfg, specs, cpoOverrides, err := prepareCreatePlan(ctx, cfg, newClient)
	if err != nil {
		return err
	}

	// Phase 0: The manifest must exist before any infra is provisioned so
	// destroy-guests can always clean up, even if create-guests fails
	// before the HostedCluster CR is applied.
	log.Println("Phase 0: Computing cluster identities and writing manifest")
	named := make([]namedSpec, len(specs))
	clusterNames := make(map[string]string) // variant -> name
	entries := make([]lifecycle.ClusterEntry, len(specs))
	for i, spec := range specs {
		name := lifecycle.DeriveClusterName(cfg.prowJobID, spec.Variant)
		named[i] = namedSpec{ClusterSpec: spec, name: name}
		clusterNames[spec.Variant] = name
		entries[i] = lifecycle.ClusterEntry{
			Variant:     spec.Variant,
			Name:        name,
			InfraID:     name,
			Namespace:   cfg.namespace,
			DestroyArgs: spec.DestroyArgs,
		}
		log.Printf("Cluster %s: name=%s", spec.Variant, name)
	}

	releaseImages := lifecycle.ReleaseImagesFromEnvironment(cfg.releaseImage)
	if releaseImages["E2E_PREVIOUS_RELEASE_IMAGE"] == "" {
		releaseImages["E2E_PREVIOUS_RELEASE_IMAGE"] = cfg.n1Image
	}
	manifest := &lifecycle.ClusterManifest{
		Clusters:      entries,
		ReleaseImages: releaseImages,
	}
	if err := lifecycle.WriteManifest(cfg.sharedDir, manifest); err != nil {
		return fmt.Errorf("writing cluster manifest: %w", err)
	}
	log.Printf("Wrote cluster manifest to %s/%s", cfg.sharedDir, lifecycle.ManifestFileName)
	configureExpectedNodePoolRecorders(named, manifest, cfg.sharedDir)

	// Phase 1: Some platforms need prerequisites deployed before clusters
	// exist (e.g., OIDC providers that the HC spec references).
	log.Println("Phase 1: Running platform pre-create hooks")
	mgmtClientPre, err := newClient()
	if err != nil {
		return fmt.Errorf("creating management cluster client for pre-create: %w", err)
	}
	if err := preflightSelectedCPOOverrides(cpoOverrides, func() error {
		return preflightCPOOverrides(ctx, mgmtClientPre, specs, cfg.pullSecret, os.Getenv("E2E_HYPERSHIFT_OPERATOR_NAMESPACE"))
	}); err != nil {
		return fmt.Errorf("CPO override preflight: %w", err)
	}
	ownedSharedOIDC := cfg.platform.Name() == "aws" && cfg.sharedOIDCIssuer == "" && cfg.sharedOIDCKeyFile == ""
	clustersStarted := false
	if ownedSharedOIDC {
		cfg, specs, err = prepareAutomaticSharedOIDC(ctx, cfg, specs, mgmtClientPre, lifecycle.SetupSharedOIDC)
		if err != nil {
			return err
		}
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
			defer cancel()
			var cleanupErr error
			if clustersStarted {
				cleanupErr = lifecycle.CleanupSharedOIDCIfUnused(cleanupCtx, cfg.sharedDir, mgmtClientPre)
			} else {
				cleanupErr = lifecycle.CleanupSharedOIDC(cleanupCtx, cfg.sharedDir)
			}
			if cleanupErr != nil {
				log.Printf("WARNING: shared OIDC cleanup incomplete; destroy-guests can retry: %v", cleanupErr)
			}
		}()
		for index := range named {
			named[index].ClusterSpec = specs[index]
		}
	}
	if cfg.artifactDir != "" {
		namespaces := make([]string, 0, len(named))
		for _, spec := range named {
			namespaces = append(namespaces, cfg.namespace+"-"+spec.name)
		}
		stopObserver, err := v2util.StartPodTimingObserver(ctx, mgmtClientPre, namespaces, filepath.Join(cfg.artifactDir, "e2e-create-metrics-raw.prometheus"))
		if err != nil {
			log.Printf("WARNING: failed to start creation pod timing diagnostics: %v", err)
		} else {
			defer func() {
				if err := stopObserver(); err != nil {
					log.Printf("WARNING: failed to write creation test metrics: %v", err)
				}
			}()
		}
	}
	if !cpoOverrides {
		if err := cfg.platform.PreCreate(ctx, mgmtClientPre, cfg.namespace); err != nil {
			return fmt.Errorf("platform pre-create hook: %w", err)
		}
	}

	// Phase 2: Clusters are independent; creating them in parallel cuts
	// wall-clock time proportionally.
	log.Printf("Phase 2: Creating %d clusters in parallel", len(named))
	clustersStarted = true
	createErrors := createClustersParallel(ctx, cfg, named)
	if err := reportClusterCreateResults(named, createErrors); err != nil {
		return err
	}

	// Phase 3: Some platforms need configuration applied after the HC CR
	// exists but before it becomes Available (e.g., patching OperatorConfiguration).
	log.Println("Phase 3: Running platform post-create hooks")
	mgmtClient, err := newClient()
	if err != nil {
		return fmt.Errorf("creating management cluster client: %w", err)
	}
	if err := cfg.platform.PostCreate(ctx, mgmtClient, cfg.namespace, clusterNames); err != nil {
		return fmt.Errorf("platform post-create hook: %w", err)
	}

	// Phase 4: Control plane components must exist before post-available
	// hooks can run; Available guarantees that.
	log.Println("Phase 4: Waiting for all clusters to become Available")
	availableErrors := waitForClustersAvailable(ctx, mgmtClient, cfg.namespace, named, cfg.waitTimeout)
	if err := reportClusterAvailabilityResults(named, availableErrors); err != nil {
		return err
	}

	// Phase 5: Some platforms need to wait for day-2 config transitions
	// that depend on control plane components existing.
	log.Println("Phase 5: Running platform post-available hooks")
	if err := cfg.platform.PostAvailable(ctx, mgmtClient, cfg.namespace, clusterNames); err != nil {
		return fmt.Errorf("platform post-available hook: %w", err)
	}

	// Phase 6: Worker-bearing fixtures require completed version rollout;
	// explicit zero-worker fixtures require a ready control plane instead.
	log.Println("Phase 6: Waiting for version rollout completion on all clusters")
	rolloutErrors := waitForVersionRollout(ctx, mgmtClient, cfg, named)
	anyRolloutFailed := false
	for _, ns := range named {
		if err := rolloutErrors[ns.Variant]; err != nil {
			log.Printf("ERROR: version rollout failed for %s (%s): %v", ns.name, ns.Variant, err)
			emitJUnitFailure(ctx, mgmtClient, cfg, ns.name, ns.Variant)
			anyRolloutFailed = true
		} else {
			log.Printf("Version rollout completed for %s (%s)", ns.name, ns.Variant)
			emitJUnitSuccess(cfg, ns.name, ns.Variant)
		}
	}

	// Phase 7: Day-2 operations that disrupt ClusterOperators (e.g., External OIDC)
	// run after rollout so they don't block the initial version completion.
	log.Println("Phase 7: Running platform post-version-rollout hooks (day-2 operations)")
	if err := cfg.platform.PostVersionRollout(ctx, mgmtClient, cfg.namespace, clusterNames); err != nil {
		return fmt.Errorf("platform post-version-rollout hook: %w", err)
	}

	if anyRolloutFailed {
		return fmt.Errorf("one or more cluster version rollouts failed")
	}

	log.Println("All clusters are ready")
	return nil
}

func reportClusterCreateResults(named []namedSpec, createErrors map[string]error) error {
	for _, spec := range named {
		if err := createErrors[spec.Variant]; err != nil {
			log.Printf("ERROR: cluster %s (%s) creation failed: %v", spec.name, spec.Variant, err)
		} else {
			log.Printf("Cluster %s (%s) creation command completed", spec.name, spec.Variant)
		}
	}
	for _, err := range createErrors {
		if err != nil {
			return fmt.Errorf("one or more cluster create commands failed")
		}
	}
	return nil
}

func reportClusterAvailabilityResults(named []namedSpec, availableErrors map[string]error) error {
	for _, spec := range named {
		if err := availableErrors[spec.Variant]; err != nil {
			log.Printf("ERROR: cluster %s (%s) did not become Available: %v", spec.name, spec.Variant, err)
		} else {
			log.Printf("Cluster %s (%s) is Available", spec.name, spec.Variant)
		}
	}
	for _, err := range availableErrors {
		if err != nil {
			return fmt.Errorf("one or more clusters did not become Available")
		}
	}
	return nil
}

// buildCreateArgs returns CLI arguments for creating a cluster.
func buildCreateArgs(cfg envConfig, ns namedSpec) []string {
	releaseImage := cfg.releaseImage
	if ns.ReleaseImage != "" {
		releaseImage = ns.ReleaseImage
	}
	nodeCount := initialNodeCount(cfg, ns)

	args := []string{
		"create", "cluster", cfg.platform.Name(),
		"--name=" + ns.name,
		"--namespace=" + cfg.namespace,
		"--infra-id=" + ns.name,
		"--node-pool-replicas=" + strconv.Itoa(nodeCount),
		"--base-domain=" + cfg.baseDomain,
		"--pull-secret=" + cfg.pullSecret,
		"--release-image=" + releaseImage,
		"--generate-ssh",
	}

	if cfg.externalDNS != "" {
		args = append(args, "--external-dns-domain="+cfg.externalDNS)
	}
	if cfg.etcdSC != "" {
		args = append(args, "--etcd-storage-class="+cfg.etcdSC)
	}

	args = append(args, ns.CreateArgs...)

	return args
}

func initialNodeCount(cfg envConfig, spec namedSpec) int {
	if spec.InitialNodePoolReplicas != nil {
		return *spec.InitialNodePoolReplicas
	}
	return cfg.nodeCount
}

func validateScenarioInputs(cfg envConfig, specs []lifecycle.ClusterSpec) error {
	if cfg.platform.Name() != "aws" {
		return nil
	}
	for _, spec := range specs {
		if _, err := awsFixtureRegion(namedSpec{ClusterSpec: spec}); err != nil {
			return err
		}
		switch spec.Variant {
		case "private-route":
			if cfg.externalDNS == "" {
				return fmt.Errorf("AWS private-route requires HYPERSHIFT_EXTERNAL_DNS_DOMAIN and an external-dns installation")
			}
		case "custom-config":
			if os.Getenv("E2E_AWS_KMS_KEY_ARN") == "" && os.Getenv("E2E_AWS_KMS_KEY_ALIAS") == "" {
				return fmt.Errorf("AWS custom-config requires E2E_AWS_KMS_KEY_ARN or E2E_AWS_KMS_KEY_ALIAS for etcd encryption")
			}
		}
	}
	return nil
}

type namedSpec struct {
	lifecycle.ClusterSpec
	name                    string
	expectedNodePools       map[string]int32
	recordExpectedNodePools func(map[string]int32) error
}

func createClustersParallel(ctx context.Context, cfg envConfig, specs []namedSpec) map[string]error {
	results := make(map[string]error)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, ns := range specs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if cfg.platform.Name() == "aws" {
				err := createAWSFixture(ctx, cfg, ns)
				mu.Lock()
				results[ns.Variant] = err
				mu.Unlock()
				return
			}
			args := buildCreateArgs(cfg, ns)
			log.Printf("Creating %s cluster %s", ns.Variant, ns.name)
			log.Printf("Running: %s %v", cfg.hypershiftBinary, args)

			cmd := exec.CommandContext(ctx, cfg.hypershiftBinary, args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			err := cmd.Run()

			mu.Lock()
			results[ns.Variant] = err
			mu.Unlock()
		}()
	}
	wg.Wait()
	return results
}

func newMgmtClient() (crclient.WithWatch, error) {
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("getting management cluster kubeconfig: %w", err)
	}
	restConfig.Dial = (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	return crclient.NewWithWatch(restConfig, crclient.Options{Scheme: scheme})
}

func waitForClustersAvailable(ctx context.Context, cl crclient.WithWatch, namespace string, specs []namedSpec, timeout time.Duration) map[string]error {
	results := make(map[string]error)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, ns := range specs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			watchCtx, watchCancel := context.WithTimeout(ctx, timeout)
			defer watchCancel()

			err := watchForCondition(watchCtx, cl, namespace, ns.name, func(hc *hyperv1.HostedCluster) bool {
				for _, cond := range hc.Status.Conditions {
					if cond.Type == string(hyperv1.HostedClusterAvailable) && cond.Status == metav1.ConditionTrue {
						return true
					}
				}
				return false
			})

			mu.Lock()
			results[ns.Variant] = err
			mu.Unlock()
		}()
	}
	wg.Wait()
	return results
}

func waitForVersionRollout(ctx context.Context, cl crclient.WithWatch, cfg envConfig, specs []namedSpec) map[string]error {
	results := make(map[string]error)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, ns := range specs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			watchCtx, watchCancel := context.WithTimeout(ctx, cfg.waitTimeout)
			defer watchCancel()

			var err error
			if !initialWorkersConfigured(cfg, ns) {
				err = waitForZeroWorkerControlPlane(watchCtx, cl, cfg.namespace, ns.name)
			} else {
				err = watchForCondition(watchCtx, cl, cfg.namespace, ns.name, func(hc *hyperv1.HostedCluster) bool {
					if hc.Status.Version == nil || len(hc.Status.Version.History) == 0 {
						return false
					}
					for _, entry := range hc.Status.Version.History {
						if entry.State != "" && entry.State != configv1.CompletedUpdate {
							return false
						}
						if entry.State == "" {
							return false
						}
					}
					return true
				})
			}

			mu.Lock()
			results[ns.Variant] = err
			mu.Unlock()
		}()
	}
	wg.Wait()
	return results
}

func waitForZeroWorkerControlPlane(ctx context.Context, cl crclient.Client, namespace, name string) error {
	return pollZeroWorkerControlPlane(ctx, cl, namespace, name, 5*time.Second)
}

func pollZeroWorkerControlPlane(ctx context.Context, cl crclient.Client, namespace, name string, interval time.Duration) error {
	var lastReadError error
	err := wait.PollUntilContextCancel(ctx, interval, true, func(ctx context.Context) (bool, error) {
		cluster := &hyperv1.HostedCluster{}
		deployment := &appsv1.Deployment{}
		lastReadError = nil
		for _, resource := range []struct {
			kind   string
			key    crclient.ObjectKey
			object crclient.Object
		}{
			{kind: "HostedCluster", key: crclient.ObjectKey{Namespace: namespace, Name: name}, object: cluster},
			{kind: "Deployment", key: crclient.ObjectKey{Namespace: namespace + "-" + name, Name: "kube-apiserver"}, object: deployment},
		} {
			if err := cl.Get(ctx, resource.key, resource.object); err != nil {
				lastReadError = fmt.Errorf("reading %s %s: %w", resource.kind, resource.key, err)
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				if !retryableZeroWorkerReadError(err) {
					return false, lastReadError
				}
				log.Printf("Retrying zero-worker readiness: %v", lastReadError)
				return false, nil
			}
		}
		return zeroWorkerControlPlaneReady(cluster, deployment), nil
	})
	if err != nil {
		return errors.Join(err, lastReadError)
	}
	return nil
}

func retryableZeroWorkerReadError(err error) bool {
	var networkError net.Error
	return apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsInternalError(err) ||
		apierrors.IsTooManyRequests(err) || apierrors.IsServiceUnavailable(err) ||
		utilnet.IsConnectionReset(err) || utilnet.IsConnectionRefused(err) || utilnet.IsProbableEOF(err) ||
		(errors.As(err, &networkError) && networkError.Timeout())
}

func zeroWorkerControlPlaneReady(cluster *hyperv1.HostedCluster, deployment *appsv1.Deployment) bool {
	if !meta.IsStatusConditionTrue(cluster.Status.Conditions, string(hyperv1.HostedClusterAvailable)) ||
		cluster.Status.KubeConfig == nil || cluster.Status.KubeConfig.Name == "" || deployment.Spec.Replicas == nil {
		return false
	}
	expected := int32(1)
	if cluster.Spec.ControllerAvailabilityPolicy == hyperv1.HighlyAvailable {
		expected = 3
	}
	return *deployment.Spec.Replicas == expected && deployment.Status.ObservedGeneration >= deployment.Generation &&
		deployment.Status.Replicas == expected && deployment.Status.UpdatedReplicas == expected &&
		deployment.Status.ReadyReplicas == expected && deployment.Status.AvailableReplicas == expected && deployment.Status.UnavailableReplicas == 0
}

func watchForCondition(ctx context.Context, cl crclient.WithWatch, namespace, name string, predicate func(*hyperv1.HostedCluster) bool) error {
	fieldSelector := fields.OneTermEqualSelector("metadata.name", name).String()
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
			opts.FieldSelector = fieldSelector
			list := &hyperv1.HostedClusterList{}
			err := cl.List(ctx, list, &crclient.ListOptions{Raw: &opts, Namespace: namespace})
			return list, err
		},
		WatchFuncWithContext: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
			opts.FieldSelector = fieldSelector
			list := &hyperv1.HostedClusterList{}
			return cl.Watch(ctx, list, &crclient.ListOptions{Raw: &opts, Namespace: namespace})
		},
	}

	_, err := toolswatch.UntilWithSync(ctx, cache.ToListWatcherWithWatchListSemantics(lw, cl), &hyperv1.HostedCluster{}, nil,
		func(event watch.Event) (bool, error) {
			hc, ok := event.Object.(*hyperv1.HostedCluster)
			if !ok || hc == nil {
				return false, nil
			}
			logClusterProgress(hc)
			return predicate(hc), nil
		},
	)
	if err != nil {
		return fmt.Errorf("waiting for %s/%s: %w", namespace, name, err)
	}
	return nil
}

func logClusterProgress(hc *hyperv1.HostedCluster) {
	available := "Unknown"
	for _, cond := range hc.Status.Conditions {
		if cond.Type == string(hyperv1.HostedClusterAvailable) {
			available = string(cond.Status)
			break
		}
	}

	versionState := "<none>"
	if hc.Status.Version != nil && len(hc.Status.Version.History) > 0 {
		versionState = string(hc.Status.Version.History[0].State)
	}

	log.Printf("Cluster %s/%s: Available=%s, VersionState=%s",
		hc.Namespace, hc.Name, available, versionState)
}

func emitJUnitFailure(ctx context.Context, cl crclient.WithWatch, cfg envConfig, name, variant string) {
	hc := &hyperv1.HostedCluster{}
	_ = cl.Get(ctx, crclient.ObjectKey{Namespace: cfg.namespace, Name: name}, hc)

	degradedMsg := conditionMessage(hc, "Degraded")
	cvSucceedingMsg := conditionMessage(hc, string(hyperv1.ClusterVersionSucceeding))
	diagnostics := collectDiagnostics(ctx, cl, cfg.namespace, name, hc)

	junitXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="hypershift install %s" tests="1" failures="1">
  <testcase name="hosted cluster version rollout succeeds">
    <failure message="hosted cluster version rollout never completed">
      <![CDATA[
error: hosted cluster version rollout never completed for %s (%s)
Degraded: %s
ClusterVersionSucceeding: %s
%s
      ]]>
    </failure>
  </testcase>
</testsuite>
`, name, name, variant, degradedMsg, cvSucceedingMsg, diagnostics)

	junitPath := filepath.Join(cfg.artifactDir, fmt.Sprintf("junit_hosted_cluster_%s.xml", name))
	if err := os.WriteFile(junitPath, []byte(junitXML), 0600); err != nil {
		log.Printf("WARNING: failed to write JUnit XML to %s: %v", junitPath, err)
	} else {
		log.Printf("Wrote JUnit failure XML to %s", junitPath)
	}
}

func emitJUnitSuccess(cfg envConfig, name, variant string) {
	junitXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="hypershift install %s" tests="1" failures="0">
  <testcase name="hosted cluster version rollout succeeds">
    <system-out>
      <![CDATA[
info: hosted cluster version rollout completed successfully for %s (%s)
      ]]>
    </system-out>
  </testcase>
</testsuite>
`, name, name, variant)

	junitPath := filepath.Join(cfg.artifactDir, fmt.Sprintf("junit_hosted_cluster_%s.xml", name))
	if err := os.WriteFile(junitPath, []byte(junitXML), 0600); err != nil {
		log.Printf("WARNING: failed to write JUnit XML to %s: %v", junitPath, err)
	}
}

func conditionMessage(hc *hyperv1.HostedCluster, condType string) string {
	if hc == nil {
		return "<unknown>"
	}
	for _, cond := range hc.Status.Conditions {
		if cond.Type == condType {
			return cond.Message
		}
	}
	return "<unknown>"
}

func collectDiagnostics(ctx context.Context, cl crclient.WithWatch, namespace, name string, hc *hyperv1.HostedCluster) string {
	var sb strings.Builder

	if hc != nil && len(hc.Status.Conditions) > 0 {
		sb.WriteString("HostedCluster conditions:\n")
		for _, cond := range hc.Status.Conditions {
			fmt.Fprintf(&sb, "  %s\t%s\t%s\t%s\n", cond.Type, cond.Status, cond.Reason, cond.Message)
		}
	}

	np := &hyperv1.NodePool{}
	if err := cl.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: name}, np); err == nil {
		sb.WriteString("NodePool conditions:\n")
		for _, cond := range np.Status.Conditions {
			fmt.Fprintf(&sb, "  %s\t%s\t%s\t%s\n", cond.Type, cond.Status, cond.Reason, cond.Message)
		}
	}

	return sb.String()
}

func mustGetenv(key string) string {
	val := os.Getenv(key)
	if val == "" {
		log.Fatalf("%s environment variable is required", key)
	}
	return val
}

func envOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func envOrDefaultInt(key string, defaultVal int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		log.Printf("WARNING: invalid integer for %s=%q, using default %d", key, val, defaultVal)
		return defaultVal
	}
	return n
}
