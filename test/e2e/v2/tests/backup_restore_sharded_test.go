//go:build e2ev2 && backuprestore

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

package tests

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// shardedE2EShardName routes configmaps (a core resource with easily
	// distinguishable data) to a named persistent shard.
	shardedE2EShardName = "configmaps"
	// shardedE2ESecretName / shardedE2EConfigMapName hold distinguishable
	// data in the default and the named shard respectively.
	shardedE2ESecretName     = "sharded-e2e-secret"
	shardedE2EConfigMapName  = "sharded-e2e-configmap"
	shardedE2ESecretValue    = "default-shard-data-before-backup"
	shardedE2EConfigMapValue = "named-shard-data-before-backup"
)

type shardedE2E struct {
	testCtx      *internal.TestContext
	cliPath      string
	region       string
	zones        string
	baseDomain   string
	releaseImage string
	pullSecret   string // path to a pull secret file
	awsCreds     string // path to the AWS credentials file

	sourceCluster string
	targetCluster string
}

// runCLI executes the hypershift CLI with the given args, streaming output to
// a log file in the artifact dir.
func (s *shardedE2E) runCLI(ctx context.Context, logName string, args ...string) error {
	cmd := exec.CommandContext(ctx, s.cliPath, args...)
	logPath := filepath.Join(s.testCtx.ArtifactDir, logName)
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("failed to create log file %s: %w", logPath, err)
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	GinkgoWriter.Printf("Running: %s %s\n", s.cliPath, strings.Join(args, " "))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hypershift %s failed (see %s): %w", strings.Join(args, " "), logPath, err)
	}
	return nil
}

func (s *shardedE2E) commonCreateArgs(name string) []string {
	return []string{
		"create", "cluster", "aws",
		"--name=" + name,
		"--namespace=clusters",
		"--infra-id=" + name,
		"--node-pool-replicas=0",
		"--base-domain=" + s.baseDomain,
		"--pull-secret=" + s.pullSecret,
		"--release-image=" + s.releaseImage,
		"--generate-ssh",
		"--region=" + s.region,
		"--zones=" + s.zones,
		"--aws-creds=" + s.awsCreds,
		"--public-only",
		"--infra-availability-policy=SingleReplica",
		"--feature-set=TechPreviewNoUpgrade",
		"--annotations=hypershift.openshift.io/cleanup-cloud-resources=true",
		"--annotations=hypershift.openshift.io/skip-release-image-validation=true",
		// Upstream --etcd-shard format: resources are <apiGroup>/<resource>
		// with a leading slash for the core group; replicas=1 keeps the
		// node-less test cluster cheap.
		"--etcd-shard=name=" + shardedE2EShardName + ",resources=/" + shardedE2EShardName + ",replicas=1",
	}
}

func (s *shardedE2E) getHostedCluster(name string) (*hyperv1.HostedCluster, error) {
	hc := &hyperv1.HostedCluster{}
	err := s.testCtx.MgmtClient.Get(s.testCtx.Context, types.NamespacedName{Namespace: "clusters", Name: name}, hc)
	return hc, err
}

// waitForGuestAPI waits until the hosted cluster's admin kubeconfig is usable.
// On timeout, the error carries the cluster's observed state to make lane
// failures diagnosable from the test output.
func (s *shardedE2E) waitForGuestAPI(name string, timeout time.Duration) (*hyperv1.HostedCluster, error) {
	var hc *hyperv1.HostedCluster
	var lastDiagnosis string
	err := eventuallyWithContext(s.testCtx.Context, timeout, func() (bool, error) {
		got, err := s.getHostedCluster(name)
		if err != nil {
			lastDiagnosis = fmt.Sprintf("HostedCluster not found: %v", err)
			return false, nil //nolint:nilerr // transient: keep polling until the timeout
		}
		hc = got
		if hc.Status.KubeConfig == nil {
			lastDiagnosis = "kubeconfig not available yet; HC conditions: " + describeConditions(hc.Status.Conditions)
			return false, nil //nolint:nilerr // transient: keep polling until the timeout
		}
		restCfg, err := s.testCtx.GetHostedClusterRESTConfig(hc)
		if err != nil {
			lastDiagnosis = fmt.Sprintf("kubeconfig not usable: %v; HC conditions: %s", err, describeConditions(hc.Status.Conditions))
			return false, nil //nolint:nilerr // transient: keep polling until the timeout
		}
		discoveryClient, err := discovery.NewDiscoveryClientForConfig(restCfg)
		if err != nil {
			lastDiagnosis = fmt.Sprintf("discovery client: %v", err)
			return false, nil //nolint:nilerr // transient: keep polling until the timeout
		}
		if _, err := discoveryClient.ServerVersion(); err != nil {
			lastDiagnosis = fmt.Sprintf("API not reachable: %v; HC conditions: %s", err, describeConditions(hc.Status.Conditions))
			return false, nil //nolint:nilerr // transient: keep polling until the timeout
		}
		return true, nil
	})
	if err != nil {
		// Include the HCP's conditions too, when it exists.
		hcpDiagnosis := ""
		hcp := &hyperv1.HostedControlPlane{}
		if hcpErr := s.testCtx.MgmtClient.Get(s.testCtx.Context, types.NamespacedName{Name: name, Namespace: "clusters-" + name}, hcp); hcpErr == nil {
			hcpDiagnosis = "; HCP conditions: " + describeConditions(hcp.Status.Conditions)
		}
		// Dump the control plane namespace's workloads and the logs of any
		// non-ready pods to the artifact dir, before the deferred cleanup
		// destroys the cluster — the lane's post-dump never sees it.
		s.dumpControlPlaneState(name)
		return nil, fmt.Errorf("guest API for %s not usable after %s: %s%s", name, timeout, lastDiagnosis, hcpDiagnosis)
	}
	return hc, nil
}

// dumpControlPlaneState writes the deployments, pods, and non-ready pod logs
// of the given cluster's control plane namespace to the artifact dir.
func (s *shardedE2E) dumpControlPlaneState(name string) {
	ns := "clusters-" + name
	clientset, err := kubernetes.NewForConfig(s.testCtx.MgmtRESTConfig)
	if err != nil {
		GinkgoWriter.Printf("failed to build clientset for diagnostics: %v\n", err)
		return
	}
	var out strings.Builder
	deployments, err := clientset.AppsV1().Deployments(ns).List(s.testCtx.Context, metav1.ListOptions{})
	if err == nil {
		fmt.Fprintf(&out, "=== deployments in %s ===\n", ns)
		for _, d := range deployments.Items {
			fmt.Fprintf(&out, "%s: ready=%d unavailable=%d (desired=%d)\n", d.Name, d.Status.ReadyReplicas, d.Status.UnavailableReplicas, ptr.Deref(d.Spec.Replicas, -1))
		}
	}
	pods, err := clientset.CoreV1().Pods(ns).List(s.testCtx.Context, metav1.ListOptions{})
	if err == nil {
		fmt.Fprintf(&out, "\n=== pods in %s ===\n", ns)
		for _, p := range pods.Items {
			fmt.Fprintf(&out, "%s: phase=%s", p.Name, p.Status.Phase)
			for _, cs := range p.Status.ContainerStatuses {
				if cs.State.Waiting != nil {
					fmt.Fprintf(&out, " [%s: waiting %s: %s]", cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message)
				}
				if cs.State.Terminated != nil {
					fmt.Fprintf(&out, " [%s: terminated %s exit=%d]", cs.Name, cs.State.Terminated.Reason, cs.State.Terminated.ExitCode)
				}
			}
			fmt.Fprintln(&out)
		}
		// Logs of pods that are not ready.
		for _, p := range pods.Items {
			ready := false
			for _, cond := range p.Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					ready = true
				}
			}
			if ready {
				continue
			}
			for _, cs := range p.Spec.Containers {
				logReq := clientset.CoreV1().Pods(ns).GetLogs(p.Name, &corev1.PodLogOptions{Container: cs.Name, TailLines: ptr.To[int64](100)})
				logs, err := logReq.DoRaw(s.testCtx.Context)
				if err == nil && len(logs) > 0 {
					fmt.Fprintf(&out, "\n=== logs %s/%s ===\n%s\n", p.Name, cs.Name, string(logs))
				}
			}
		}
	}
	if err := os.WriteFile(filepath.Join(s.testCtx.ArtifactDir, fmt.Sprintf("target-control-plane-state-%s.txt", name)), []byte(out.String()), 0644); err != nil {
		GinkgoWriter.Printf("failed to write diagnostics: %v\n", err)
	}
}

// describeConditions renders conditions compactly for failure messages.
func describeConditions(conds []metav1.Condition) string {
	if len(conds) == 0 {
		return "<none>"
	}
	parts := make([]string, 0, len(conds))
	for _, c := range conds {
		parts = append(parts, fmt.Sprintf("%s=%s(%s)", c.Type, c.Status, c.Reason))
	}
	return strings.Join(parts, ", ")
}

func eventuallyWithContext(ctx context.Context, timeout time.Duration, f func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		done, err := f()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}

func (s *shardedE2E) guestClient(hc *hyperv1.HostedCluster) (crclient.Client, error) {
	return s.testCtx.GetHostedClusterClient(hc)
}

func (s *shardedE2E) seedData(hc *hyperv1.HostedCluster) error {
	guest, err := s.guestClient(hc)
	if err != nil {
		return err
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: shardedE2ESecretName, Namespace: "default"},
		StringData: map[string]string{"data": shardedE2ESecretValue},
	}
	if err := guest.Create(s.testCtx.Context, secret); err != nil {
		return fmt.Errorf("failed to create seed secret: %w", err)
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: shardedE2EConfigMapName, Namespace: "default"},
		Data:       map[string]string{"data": shardedE2EConfigMapValue},
	}
	if err := guest.Create(s.testCtx.Context, configMap); err != nil {
		return fmt.Errorf("failed to create seed configmap: %w", err)
	}
	// Allow the writes to be observed by etcd before snapshotting.
	time.Sleep(10 * time.Second)
	return nil
}

func (s *shardedE2E) verifyData(hc *hyperv1.HostedCluster) error {
	guest, err := s.guestClient(hc)
	if err != nil {
		return err
	}
	secret := &corev1.Secret{}
	if err := guest.Get(s.testCtx.Context, types.NamespacedName{Name: shardedE2ESecretName, Namespace: "default"}, secret); err != nil {
		return fmt.Errorf("restored secret not found (default shard): %w", err)
	}
	if string(secret.Data["data"]) != shardedE2ESecretValue {
		return fmt.Errorf("restored secret has wrong data: %q", string(secret.Data["data"]))
	}
	configMap := &corev1.ConfigMap{}
	if err := guest.Get(s.testCtx.Context, types.NamespacedName{Name: shardedE2EConfigMapName, Namespace: "default"}, configMap); err != nil {
		return fmt.Errorf("restored configmap not found (named shard): %w", err)
	}
	if configMap.Data["data"] != shardedE2EConfigMapValue {
		return fmt.Errorf("restored configmap has wrong data: %q", configMap.Data["data"])
	}
	return nil
}

func (s *shardedE2E) destroyCluster(ctx context.Context, name string) {
	args := []string{
		"destroy", "cluster", "aws",
		"--name=" + name,
		"--region=" + s.region,
		"--base-domain=" + s.baseDomain,
		"--aws-creds=" + s.awsCreds,
	}
	if err := s.runCLI(ctx, fmt.Sprintf("destroy-%s.log", name), args...); err != nil {
		GinkgoWriter.Printf("WARNING: failed to destroy cluster %s: %v\n", name, err)
	}
}

// resolveBSL reads the lane's OADP BackupStorageLocation to reuse its bucket
// and credentials for the direct HCPEtcdBackup.
func (s *shardedE2E) resolveBSL() (bucket, region string, credsSecret *corev1.Secret, err error) {
	bsl := &unstructured.Unstructured{}
	bsl.SetGroupVersionKind(schema.GroupVersionKind{Group: "velero.io", Version: "v1", Kind: "BackupStorageLocation"})
	err = s.testCtx.MgmtClient.Get(s.testCtx.Context, crclient.ObjectKey{
		Namespace: "openshift-adp",
		Name:      s.testCtx.ClusterName,
	}, bsl)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to get BackupStorageLocation: %w", err)
	}
	// The lane's OADP setup creates the BSL with the legacy provider-based
	// structure (spec.objectStorage/config/credential); support the newer
	// storageType-based structure as well.
	bucket, _, _ = unstructured.NestedString(bsl.Object, "spec", "storageType", "s3", "bucket")
	region, _, _ = unstructured.NestedString(bsl.Object, "spec", "storageType", "s3", "region")
	credName, _, _ := unstructured.NestedString(bsl.Object, "spec", "storageType", "s3", "credential", "name")
	if bucket == "" {
		bucket, _, _ = unstructured.NestedString(bsl.Object, "spec", "objectStorage", "bucket")
		region, _, _ = unstructured.NestedString(bsl.Object, "spec", "config", "region")
		credName, _, _ = unstructured.NestedString(bsl.Object, "spec", "credential", "name")
	}
	if bucket == "" || credName == "" {
		return "", "", nil, fmt.Errorf("BackupStorageLocation %s has no s3 bucket or credential (spec: %v)", s.testCtx.ClusterName, bsl.Object["spec"])
	}
	credsSecret = &corev1.Secret{}
	if err := s.testCtx.MgmtClient.Get(s.testCtx.Context, types.NamespacedName{Namespace: "openshift-adp", Name: credName}, credsSecret); err != nil {
		return "", "", nil, fmt.Errorf("failed to get BSL credential secret: %w", err)
	}
	return bucket, region, credsSecret, nil
}

// awsConfig builds an AWS SDK config from the credentials in the given
// secret (velero "cloud" key or raw "credentials" key).
func awsConfigFromSecret(ctx context.Context, secret *corev1.Secret, region string) (aws.Config, error) {
	credsData := secret.Data["cloud"]
	if len(credsData) == 0 {
		credsData = secret.Data["credentials"]
	}
	if len(credsData) == 0 {
		return aws.Config{}, fmt.Errorf("credential secret has neither cloud nor credentials key")
	}
	dir, err := os.MkdirTemp("", "sharded-e2e-creds")
	if err != nil {
		return aws.Config{}, err
	}
	credsFile := filepath.Join(dir, "credentials")
	if err := os.WriteFile(credsFile, credsData, 0600); err != nil {
		return aws.Config{}, err
	}
	return awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithSharedConfigFiles([]string{credsFile}),
		awsconfig.WithRegion(region),
	)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:EtcdSnapshot] ShardedEtcdSnapshotBackupRestore", Label("backup-restore", "etcd-snapshot", "sharded-etcd"), Ordered, Serial, func() {
	var e shardedE2E

	BeforeAll(func() {
		e.testCtx = internal.GetTestContext()
		Expect(e.testCtx).NotTo(BeNil())

		primaryHC, err := e.testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		if primaryHC.Spec.Platform.Type != hyperv1.AWSPlatform {
			Skip(fmt.Sprintf("sharded etcd snapshot e2e is only supported on AWS (got %s)", primaryHC.Spec.Platform.Type))
		}

		By("Checking if HCPEtcdBackup feature gate is enabled")
		hcpEtcdBackupList := &hyperv1.HCPEtcdBackupList{}
		err = e.testCtx.MgmtClient.List(e.testCtx.Context, hcpEtcdBackupList, crclient.InNamespace(e.testCtx.ControlPlaneNamespace))
		if err != nil {
			if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
				Skip("HCPEtcdBackup feature gate is not enabled")
			}
			Expect(err).NotTo(HaveOccurred())
		}

		By("Locating the hypershift CLI")
		cliPath, err := exec.LookPath("hypershift")
		Expect(err).NotTo(HaveOccurred(), "the hypershift CLI must be on PATH")
		e.cliPath = cliPath

		By("Checking that a BackupStorageLocation exists for the primary cluster")
		probe := &unstructured.Unstructured{}
		probe.SetGroupVersionKind(schema.GroupVersionKind{Group: "velero.io", Version: "v1", Kind: "BackupStorageLocation"})
		probeErr := e.testCtx.MgmtClient.Get(e.testCtx.Context, crclient.ObjectKey{
			Namespace: "openshift-adp",
			Name:      e.testCtx.ClusterName,
		}, probe)
		if probeErr != nil {
			Skip(fmt.Sprintf("no BackupStorageLocation %s in openshift-adp (OADP not set up on this lane): %v", e.testCtx.ClusterName, probeErr))
		}

		By("Checking for AWS credentials")
		e.awsCreds = internal.GetEnvVarValue("AWS_GUEST_INFRA_CREDENTIALS_FILE")
		if _, err := os.Stat(e.awsCreds); err != nil {
			Skip(fmt.Sprintf("AWS credentials file %s not available in the test pod", e.awsCreds))
		}

		e.region = os.Getenv("HYPERSHIFT_AWS_REGION")
		if e.region == "" {
			e.region = "us-east-1"
		}
		e.zones = os.Getenv("HYPERSHIFT_AWS_ZONES")
		if e.zones == "" {
			e.zones = "us-east-1a"
		}
		e.baseDomain = primaryHC.Spec.DNS.BaseDomain
		e.releaseImage = primaryHC.Spec.Release.Image

		By("Writing the pull secret to a file")
		pullSecret := &corev1.Secret{}
		err = e.testCtx.MgmtClient.Get(e.testCtx.Context, types.NamespacedName{
			Namespace: primaryHC.Namespace,
			Name:      primaryHC.Spec.PullSecret.Name,
		}, pullSecret)
		Expect(err).NotTo(HaveOccurred())
		e.pullSecret = filepath.Join(e.testCtx.ArtifactDir, "sharded-e2e-pull-secret.json")
		Expect(os.WriteFile(e.pullSecret, pullSecret.Data[".dockerconfigjson"], 0600)).To(Succeed())

		DeferCleanup(func() {
			if e.sourceCluster != "" {
				e.destroyCluster(e.testCtx.Context, e.sourceCluster)
			}
			if e.targetCluster != "" {
				e.destroyCluster(e.testCtx.Context, e.targetCluster)
			}
		})
	})

	Context("SourceCluster", func() {
		It("should create a sharded node-less hosted cluster and seed distinguishable data", func() {
			e.sourceCluster = "sharded-src-" + fmt.Sprintf("%d", time.Now().Unix()%100000)
			By(fmt.Sprintf("Creating sharded source cluster %s", e.sourceCluster))
			Expect(e.runCLI(e.testCtx.Context, "create-source-cluster.log", e.commonCreateArgs(e.sourceCluster)...)).To(Succeed())

			By("Waiting for the source cluster API to be reachable")
			_, err := e.waitForGuestAPI(e.sourceCluster, 30*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("Seeding distinguishable data into both shards")
			hc, err := e.getHostedCluster(e.sourceCluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(e.seedData(hc)).To(Succeed())
		})
	})

	Context("DirectBackup", func() {
		var backupName string
		var credsSecret *corev1.Secret
		var bucket, bslRegion string

		It("should back up both shards directly via HCPEtcdBackup and verify per-shard status and objects", func() {
			By("Resolving storage from the lane's BackupStorageLocation")
			var err error
			bucket, bslRegion, credsSecret, err = e.resolveBSL()
			Expect(err).NotTo(HaveOccurred())
			if bslRegion == "" {
				bslRegion = e.region
			}

			By("Copying the BSL credentials into the HyperShift operator namespace")
			hoNamespace := discoverHONamespace(e.testCtx)
			credsCopy := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sharded-e2e-backup-creds",
					Namespace: hoNamespace,
				},
				Data: credsSecret.Data,
			}
			err = e.testCtx.MgmtClient.Create(e.testCtx.Context, credsCopy)
			if apierrors.IsAlreadyExists(err) {
				err = e.testCtx.MgmtClient.Update(e.testCtx.Context, credsCopy)
			}
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				_ = e.testCtx.MgmtClient.Delete(e.testCtx.Context, &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: credsCopy.Name, Namespace: credsCopy.Namespace},
				})
			})

			By("Creating the HCPEtcdBackup")
			backupName = "sharded-e2e-backup"
			backup := &hyperv1.HCPEtcdBackup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      backupName,
					Namespace: "clusters-" + e.sourceCluster,
				},
				Spec: hyperv1.HCPEtcdBackupSpec{
					Storage: hyperv1.HCPEtcdBackupStorage{
						StorageType: hyperv1.S3BackupStorage,
						S3: hyperv1.HCPEtcdBackupS3{
							Bucket:      bucket,
							Region:      bslRegion,
							KeyPrefix:   fmt.Sprintf("backups/%s", e.sourceCluster),
							Credentials: hyperv1.SecretReference{Name: credsCopy.Name},
						},
					},
				},
			}
			Expect(e.testCtx.MgmtClient.Create(e.testCtx.Context, backup)).To(Succeed())

			backupStart := time.Now()
			By("Waiting for BackupCompleted=True")
			Eventually(func(g Gomega) {
				updated := &hyperv1.HCPEtcdBackup{}
				g.Expect(e.testCtx.MgmtClient.Get(e.testCtx.Context, types.NamespacedName{Name: backupName, Namespace: backup.Namespace}, updated)).To(Succeed())
				cond := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.BackupCompleted))
				g.Expect(cond).NotTo(BeNil(), "BackupCompleted condition not set yet")
				if cond.Status != metav1.ConditionTrue {
					GinkgoWriter.Printf("BackupCompleted=%s reason=%s message=%s\n", cond.Status, cond.Reason, cond.Message)
				}
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue), "backup should succeed, reason=%s message=%s", cond.Reason, cond.Message)
				shardedSnapshotResults = updated.Status.ShardSnapshots
			}).WithTimeout(30 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())
			GinkgoWriter.Printf("Observed backup duration: %s (recorded for the consistency model)\n", time.Since(backupStart))

			By("Verifying per-shard snapshot status")
			Expect(shardedSnapshotResults).To(HaveLen(2), "expected exactly the default and the named shard")
			byIdentity := map[string]string{}
			for _, ss := range shardedSnapshotResults {
				byIdentity[ss.Name] = ss.SnapshotURL
			}
			Expect(byIdentity).To(HaveKeyWithValue("default", Not(BeEmpty())))
			Expect(byIdentity).To(HaveKeyWithValue(shardedE2EShardName, Not(BeEmpty())))
			Expect(byIdentity["default"]).ToNot(Equal(byIdentity[shardedE2EShardName]), "each shard must have its own object")

			By("Verifying the per-shard mapping is persisted on the source HostedCluster")
			Eventually(func(g Gomega) {
				updated := &hyperv1.HostedCluster{}
				g.Expect(e.testCtx.MgmtClient.Get(e.testCtx.Context, types.NamespacedName{Namespace: "clusters", Name: e.sourceCluster}, updated)).To(Succeed())
				mapping := map[string]string{}
				for _, ss := range updated.Status.LastSuccessfulEtcdShardBackupURLs {
					mapping[ss.Name] = ss.SnapshotURL
				}
				g.Expect(mapping).To(HaveKeyWithValue("default", byIdentity["default"]), "the archived mapping must survive HCPEtcdBackup retention")
				g.Expect(mapping).To(HaveKeyWithValue(shardedE2EShardName, byIdentity[shardedE2EShardName]))
			}).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			By("Verifying the S3 objects exist and are distinct")
			cfg, err := awsConfigFromSecret(e.testCtx.Context, credsSecret, bslRegion)
			Expect(err).NotTo(HaveOccurred())
			s3Client := awss3.NewFromConfig(cfg)
			for identity, url := range byIdentity {
				b, key := parseS3URL(url)
				Expect(b).To(Equal(bucket))
				_, err := s3Client.HeadObject(e.testCtx.Context, &awss3.HeadObjectInput{
					Bucket: aws.String(b),
					Key:    aws.String(key),
				})
				Expect(err).NotTo(HaveOccurred(), "snapshot object for shard %s should exist", identity)
			}
		})
	})

	Context("Restore", func() {
		It("should restore both shards into a fresh cluster with presigned per-shard URLs and verify the data", func() {
			Expect(shardedSnapshotResults).To(HaveLen(2))

			By("Destroying the source cluster to free management-cluster resources")
			// The nested management cluster cannot fit two full control
			// planes plus OADP at once: with the source still running, the
			// target's kube-apiserver pod never becomes schedulable. The
			// restore only needs the presigned snapshot URLs (and the
			// expected data values, which are constants), not the source.
			e.destroyCluster(e.testCtx.Context, e.sourceCluster)
			e.sourceCluster = ""

			By("Presigning both snapshot URLs")
			_, bslRegion, credsSecret, err := e.resolveBSL()
			Expect(err).NotTo(HaveOccurred())
			if bslRegion == "" {
				bslRegion = e.region
			}
			cfg, err := awsConfigFromSecret(e.testCtx.Context, credsSecret, bslRegion)
			Expect(err).NotTo(HaveOccurred())
			presignClient := awss3.NewPresignClient(awss3.NewFromConfig(cfg))
			presign := func(url string) string {
				b, key := parseS3URL(url)
				req, err := presignClient.PresignGetObject(e.testCtx.Context, &awss3.GetObjectInput{
					Bucket: aws.String(b),
					Key:    aws.String(key),
				}, awss3.WithPresignExpires(time.Hour))
				Expect(err).NotTo(HaveOccurred())
				return req.URL
			}
			defaultURL := ""
			namedURL := ""
			for _, ss := range shardedSnapshotResults {
				switch ss.Name {
				case "default":
					defaultURL = presign(ss.SnapshotURL)
				case shardedE2EShardName:
					namedURL = presign(ss.SnapshotURL)
				}
			}
			Expect(defaultURL).NotTo(BeEmpty())
			Expect(namedURL).NotTo(BeEmpty())

			By("Creating target cluster infrastructure")
			e.targetCluster = "sharded-tgt-" + fmt.Sprintf("%d", time.Now().Unix()%100000)
			infraJSON := filepath.Join(e.testCtx.ArtifactDir, "target-infra.json")
			Expect(e.runCLI(e.testCtx.Context, "create-target-infra.log",
				"create", "infra", "aws",
				"--infra-id="+e.targetCluster,
				"--name="+e.targetCluster,
				"--region="+e.region,
				"--base-domain="+e.baseDomain,
				"--zones="+e.zones,
				"--aws-creds="+e.awsCreds,
				"--public-only",
				"--output-file="+infraJSON,
			)).To(Succeed())

			By("Rendering the target cluster manifests")
			renderArgs := append(e.commonCreateArgs(e.targetCluster),
				"--infra-json="+infraJSON,
				"--render", "--render-sensitive",
			)
			renderCmd := exec.CommandContext(e.testCtx.Context, e.cliPath, renderArgs...)
			rendered, err := renderCmd.Output()
			Expect(err).NotTo(HaveOccurred(), "failed to render target cluster manifests")

			By("Injecting the per-shard restore URLs into the rendered HostedCluster")
			Expect(os.WriteFile(filepath.Join(e.testCtx.ArtifactDir, "target-cluster-rendered.yaml"), rendered, 0644)).To(Succeed())
			applyRestoredShardedCluster(e.testCtx, rendered, defaultURL, namedURL, shardedE2EShardName)

			By("Waiting for EtcdSnapshotRestored=True on the target control plane")
			hcpNamespace := "clusters-" + e.targetCluster
			Eventually(func(g Gomega) {
				hcp := &hyperv1.HostedControlPlane{}
				g.Expect(e.testCtx.MgmtClient.Get(e.testCtx.Context, types.NamespacedName{Name: e.targetCluster, Namespace: hcpNamespace}, hcp)).To(Succeed())
				cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.EtcdSnapshotRestored))
				// A nil condition must fail this poll, not succeed it: the
				// condition is only set once the CPO observes the restore.
				g.Expect(cond).NotTo(BeNil(), "EtcdSnapshotRestored condition not set yet")
				if cond.Status != metav1.ConditionTrue {
					GinkgoWriter.Printf("EtcdSnapshotRestored=%s reason=%s message=%s\n", cond.Status, cond.Reason, cond.Message)
				}
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue), "restore should complete, reason=%s message=%s", cond.Reason, cond.Message)
			}).WithTimeout(30 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())

			By("Verifying the restored data in both shards")
			hc, err := e.waitForGuestAPI(e.targetCluster, 30*time.Minute)
			Expect(err).NotTo(HaveOccurred())
			Expect(e.verifyData(hc)).To(Succeed())
		})
	})
})

// shardedSnapshotResults is populated by the DirectBackup context and
// consumed by the Restore context.
var shardedSnapshotResults []hyperv1.HCPEtcdShardSnapshot

// discoverHONamespace finds the HyperShift operator's namespace from its pod.
func discoverHONamespace(testCtx *internal.TestContext) string {
	pods := &corev1.PodList{}
	if err := testCtx.MgmtClient.List(testCtx.Context, pods, crclient.MatchingLabels{"app": "operator"}); err == nil && len(pods.Items) > 0 {
		return pods.Items[0].Namespace
	}
	return "hypershift"
}

// parseS3URL splits an s3://bucket/key URL.
func parseS3URL(url string) (bucket, key string) {
	rest := strings.TrimPrefix(url, "s3://")
	bucket, key, _ = strings.Cut(rest, "/")
	return bucket, key
}

// applyRestoredShardedCluster splits the rendered multi-document YAML, injects
// the restore URLs into the HostedCluster document, and applies every object.
func applyRestoredShardedCluster(testCtx *internal.TestContext, rendered []byte, defaultURL, namedURL, shardName string) {
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(rendered), 4096)
	for {
		var raw map[string]interface{}
		if err := decoder.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			Expect(err).NotTo(HaveOccurred(), "failed to decode rendered manifests")
		}
		if raw == nil {
			continue
		}
		obj := &unstructured.Unstructured{Object: raw}
		if obj.GetKind() == "HostedCluster" {
			// Default shard restore URL (legacy storage field).
			Expect(unstructured.SetNestedStringSlice(obj.Object, []string{defaultURL}, "spec", "etcd", "managed", "storage", "restoreSnapshotURL")).To(Succeed())
			// Named shard restore URL.
			shards, found, err := unstructured.NestedSlice(obj.Object, "spec", "etcd", "managed", "shards")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue(), "rendered HostedCluster should have shards")
			for i, raw := range shards {
				shard, ok := raw.(map[string]interface{})
				Expect(ok).To(BeTrue())
				if shard["name"] == shardName {
					shard["restoreSnapshotURL"] = namedURL
					shards[i] = shard
				}
			}
			Expect(unstructured.SetNestedSlice(obj.Object, shards, "spec", "etcd", "managed", "shards")).To(Succeed())
		}
		err := testCtx.MgmtClient.Create(testCtx.Context, obj)
		if apierrors.IsAlreadyExists(err) {
			// The rendered manifests include the cluster namespace and other
			// pre-existing objects; patch them instead.
			err = testCtx.MgmtClient.Patch(testCtx.Context, obj, crclient.Apply, crclient.ForceOwnership, crclient.FieldOwner("sharded-e2e"))
		}
		Expect(err).To(Succeed(), "failed to create %s %s/%s", obj.GetKind(), obj.GetNamespace(), obj.GetName())
	}
}
