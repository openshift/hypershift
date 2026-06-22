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
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	routev1 "github.com/openshift/api/route/v1"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
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
	prowJobID    string
	sharedDir    string
	artifactDir  string
	releaseImage string
	n1Image      string

	baseDomain  string
	nodeCount   int
	namespace   string
	externalDNS string
	etcdSC      string
	pullSecret  string

	platform         lifecycle.PlatformConfig
	hypershiftBinary string
	waitTimeout      time.Duration
}

func main() {
	cfg := loadEnvConfig()

	ctx, cancel := context.WithTimeout(context.Background(), cfg.waitTimeout+10*time.Minute)
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
		prowJobID:    mustGetenv("PROW_JOB_ID"),
		sharedDir:    sharedDir,
		artifactDir:  mustGetenv("ARTIFACT_DIR"),
		releaseImage: mustGetenv("RELEASE_IMAGE_LATEST"),
		n1Image:      os.Getenv("OCP_IMAGE_N1"),

		baseDomain:  envOrDefault("HYPERSHIFT_BASE_DOMAIN", platform.DefaultBaseDomain()),
		nodeCount:   envOrDefaultInt("HYPERSHIFT_NODE_COUNT", 3),
		namespace:   envOrDefault("HYPERSHIFT_NAMESPACE", defaultNamespace),
		externalDNS: os.Getenv("HYPERSHIFT_EXTERNAL_DNS_DOMAIN"),
		etcdSC:      os.Getenv("HYPERSHIFT_ETCD_STORAGE_CLASS"),
		pullSecret:  envOrDefault("PULL_SECRET", "/etc/ci-pull-credentials/.dockerconfigjson"),

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
	specs := cfg.platform.ClusterSpecs(cfg.releaseImage, cfg.n1Image)

	// Derive cluster names and build the name map.
	named := make([]namedSpec, len(specs))
	clusterNames := make(map[string]string) // outputFile -> name
	for i, spec := range specs {
		name := lifecycle.DeriveClusterName(cfg.prowJobID, spec.Variant)
		named[i] = namedSpec{ClusterSpec: spec, name: name}
		clusterNames[spec.OutputFile] = name
	}

	// Phase 0: Platform-specific pre-create hooks (e.g., deploy OIDC providers).
	log.Println("Phase 0: Running platform pre-create hooks")
	mgmtClientPre, err := newMgmtClient()
	if err != nil {
		return fmt.Errorf("creating management cluster client for pre-create: %w", err)
	}
	if err := cfg.platform.PreCreate(ctx, mgmtClientPre, cfg.namespace); err != nil {
		return fmt.Errorf("platform pre-create hook: %w", err)
	}

	// Phase 1: Create all clusters in parallel.
	log.Printf("Phase 1: Creating %d clusters in parallel", len(named))
	createErrors := createClustersParallel(ctx, cfg, named)
	for _, ns := range named {
		if err := createErrors[ns.Variant]; err != nil {
			log.Printf("ERROR: cluster %s (%s) creation failed: %v", ns.name, ns.Variant, err)
		} else {
			log.Printf("Cluster %s (%s) creation command completed", ns.name, ns.Variant)
		}
	}
	for _, err := range createErrors {
		if err != nil {
			return fmt.Errorf("one or more cluster create commands failed")
		}
	}

	// Phase 2: Platform-specific post-create hooks.
	log.Println("Phase 2: Running platform post-create hooks")
	mgmtClient, err := newMgmtClient()
	if err != nil {
		return fmt.Errorf("creating management cluster client: %w", err)
	}
	if err := cfg.platform.PostCreate(ctx, mgmtClient, cfg.namespace, clusterNames); err != nil {
		return fmt.Errorf("platform post-create hook: %w", err)
	}

	// Phase 3: Watch for Available condition on all clusters.
	log.Println("Phase 3: Waiting for all clusters to become Available")
	availableErrors := waitForClustersAvailable(ctx, mgmtClient, cfg.namespace, named, 30*time.Minute)
	for _, ns := range named {
		if err := availableErrors[ns.Variant]; err != nil {
			log.Printf("ERROR: cluster %s (%s) did not become Available: %v", ns.name, ns.Variant, err)
		} else {
			log.Printf("Cluster %s (%s) is Available", ns.name, ns.Variant)
		}
	}
	for _, err := range availableErrors {
		if err != nil {
			return fmt.Errorf("one or more clusters did not become Available")
		}
	}

	// Phase 4: Platform-specific post-available hooks (e.g., waiting for
	// day-2 config transitions now that control plane components exist).
	log.Println("Phase 4: Running platform post-available hooks")
	if err := cfg.platform.PostAvailable(ctx, mgmtClient, cfg.namespace, clusterNames); err != nil {
		return fmt.Errorf("platform post-available hook: %w", err)
	}

	// Phase 5: Watch for version rollout completion on all clusters.
	log.Println("Phase 5: Waiting for version rollout completion on all clusters")
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

	// Phase 6: Day-2 operations that disrupt ClusterOperators (e.g., External OIDC).
	// These run after VersionState=Completed so the initial rollout isn't blocked.
	log.Println("Phase 6: Running platform post-version-rollout hooks (day-2 operations)")
	if err := cfg.platform.PostVersionRollout(ctx, mgmtClient, cfg.namespace, clusterNames); err != nil {
		return fmt.Errorf("platform post-version-rollout hook: %w", err)
	}

	// Phase 7: Write cluster names to SHARED_DIR.
	log.Println("Phase 7: Writing cluster names to SHARED_DIR")
	for _, ns := range named {
		outputPath := filepath.Join(cfg.sharedDir, ns.OutputFile)
		if err := os.WriteFile(outputPath, []byte(ns.name), 0600); err != nil {
			return fmt.Errorf("writing cluster name to %s: %w", outputPath, err)
		}
		log.Printf("Wrote cluster name %q to %s", ns.name, outputPath)
	}

	if anyRolloutFailed {
		return fmt.Errorf("one or more cluster version rollouts failed")
	}

	log.Println("All clusters are ready")
	return nil
}

// buildCreateArgs returns CLI arguments for creating a cluster.
func buildCreateArgs(cfg envConfig, name string, spec lifecycle.ClusterSpec) []string {
	releaseImage := cfg.releaseImage
	if spec.ReleaseImage != "" {
		releaseImage = spec.ReleaseImage
	}

	args := []string{
		"create", "cluster", cfg.platform.Name(),
		"--name=" + name,
		"--node-pool-replicas=" + strconv.Itoa(cfg.nodeCount),
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

	args = append(args, cfg.platform.CreateArgs()...)
	args = append(args, spec.ExtraArgs...)

	return args
}

type namedSpec struct {
	lifecycle.ClusterSpec
	name string
}

func createClustersParallel(ctx context.Context, cfg envConfig, specs []namedSpec) map[string]error {
	results := make(map[string]error)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, ns := range specs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			args := buildCreateArgs(cfg, ns.name, ns.ClusterSpec)
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

			err := watchForCondition(watchCtx, cl, cfg.namespace, ns.name, func(hc *hyperv1.HostedCluster) bool {
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

			mu.Lock()
			results[ns.Variant] = err
			mu.Unlock()
		}()
	}
	wg.Wait()
	return results
}

func watchForCondition(ctx context.Context, cl crclient.WithWatch, namespace, name string, predicate func(*hyperv1.HostedCluster) bool) error {
	key := crclient.ObjectKey{Namespace: namespace, Name: name}
	hc := &hyperv1.HostedCluster{}

	for {
		if err := cl.Get(ctx, key, hc); err == nil {
			if predicate(hc) {
				return nil
			}
		}

		hcList := &hyperv1.HostedClusterList{}
		watcher, err := cl.Watch(ctx, hcList,
			crclient.InNamespace(namespace),
			crclient.MatchingFields{"metadata.name": name},
		)
		if err != nil {
			return fmt.Errorf("starting watch for %s/%s: %w", namespace, name, err)
		}

		closed := false
		for !closed {
			select {
			case <-ctx.Done():
				watcher.Stop()
				return fmt.Errorf("timed out waiting for %s/%s: %w", namespace, name, ctx.Err())
			case event, ok := <-watcher.ResultChan():
				if !ok {
					closed = true
					break
				}
				if event.Type == watch.Error {
					closed = true
					break
				}
				if event.Type != watch.Added && event.Type != watch.Modified {
					continue
				}
				watchedHC, ok := event.Object.(*hyperv1.HostedCluster)
				if !ok {
					continue
				}
				logClusterProgress(watchedHC)
				if predicate(watchedHC) {
					watcher.Stop()
					return nil
				}
			}
		}
		watcher.Stop()
	}
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
