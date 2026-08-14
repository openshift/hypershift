//go:build integration
// +build integration

// Package controller contains integration tests for the HCPEtcdBackup controller.
//
// These tests validate the end-to-end controller flow against a live
// management cluster hosting a HostedCluster. The controller creates
// RBAC, NetworkPolicy, and a backup Job, then reports status through
// the HCPEtcdBackup CR conditions.
//
// Required environment variables:
//   - KUBECONFIG: path to the management cluster kubeconfig
//   - ETCD_BACKUP_TEST_HCP_NAMESPACE: the HCP namespace (e.g. clusters-my-hcp)
//
// S3 test environment variables:
//   - ETCD_BACKUP_TEST_S3_BUCKET: S3 bucket for backup storage
//   - ETCD_BACKUP_TEST_S3_REGION: AWS region of the S3 bucket
//   - ETCD_BACKUP_TEST_S3_KEY_PREFIX: S3 key prefix for backup files
//   - ETCD_BACKUP_TEST_S3_CREDENTIALS_SECRET: name of the Secret containing AWS credentials
//
// Azure test environment variables:
//   - ETCD_BACKUP_TEST_AZURE_CONTAINER: Azure Blob container name
//   - ETCD_BACKUP_TEST_AZURE_STORAGE_ACCOUNT: Azure Storage Account name
//   - ETCD_BACKUP_TEST_AZURE_KEY_PREFIX: blob name prefix for backup files
//   - ETCD_BACKUP_TEST_AZURE_CREDENTIALS_SECRET: name of the Secret containing Azure credentials
//
// Optional environment variables:
//   - ETCD_BACKUP_TEST_HO_NAMESPACE: the HO namespace (defaults to "hypershift")
//   - ETCD_BACKUP_TEST_TIMEOUT: polling timeout in seconds (defaults to 300)
//   - ETCD_BACKUP_TEST_POLL_INTERVAL: polling interval in seconds (defaults to 5)
//
// Run S3 tests:
//
//	KUBECONFIG=/path/to/kubeconfig \
//	ETCD_BACKUP_TEST_HCP_NAMESPACE=clusters-my-hcp \
//	ETCD_BACKUP_TEST_S3_BUCKET=my-bucket \
//	ETCD_BACKUP_TEST_S3_REGION=us-east-2 \
//	ETCD_BACKUP_TEST_S3_KEY_PREFIX=etcd-backups/my-hcp \
//	ETCD_BACKUP_TEST_S3_CREDENTIALS_SECRET=hypershift-operator-aws-credentials \
//	  go test -tags integration -v -timeout 10m ./test/integration/oadp/controller/...
//
// Run Azure tests:
//
//	KUBECONFIG=/path/to/kubeconfig \
//	ETCD_BACKUP_TEST_HCP_NAMESPACE=clusters-my-hcp \
//	ETCD_BACKUP_TEST_AZURE_CONTAINER=oadp \
//	ETCD_BACKUP_TEST_AZURE_STORAGE_ACCOUNT=jparrill \
//	ETCD_BACKUP_TEST_AZURE_KEY_PREFIX=etcd-backups/my-hcp \
//	ETCD_BACKUP_TEST_AZURE_CREDENTIALS_SECRET=azure-backup-credentials \
//	  go test -tags integration -v -timeout 10m ./test/integration/oadp/controller/...
package controller

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/etcdbackup"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"go.uber.org/zap/zapcore"
)

func TestMain(m *testing.M) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))
	os.Exit(m.Run())
}

const (
	defaultHONamespace  = "hypershift"
	defaultTimeout      = 300
	defaultPollInterval = 5

	// cleanupCmdTimeout is the timeout for cloud CLI cleanup commands.
	cleanupCmdTimeout = 30 * time.Second
)

type baseConfig struct {
	HCPNamespace string
	HONamespace  string
	Timeout      time.Duration
	PollInterval time.Duration
}

type s3Config struct {
	Bucket            string
	Region            string
	KeyPrefix         string
	CredentialsSecret string
}

type azureConfig struct {
	Container         string
	StorageAccount    string
	KeyPrefix         string
	CredentialsSecret string
}

func loadBaseConfig(t *testing.T) baseConfig {
	t.Helper()

	hcpNS := os.Getenv("ETCD_BACKUP_TEST_HCP_NAMESPACE")
	if hcpNS == "" {
		t.Skip("ETCD_BACKUP_TEST_HCP_NAMESPACE not set")
	}

	hoNS := os.Getenv("ETCD_BACKUP_TEST_HO_NAMESPACE")
	if hoNS == "" {
		hoNS = defaultHONamespace
	}

	timeout := defaultTimeout
	if v := os.Getenv("ETCD_BACKUP_TEST_TIMEOUT"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			timeout = parsed
		}
	}

	pollInterval := defaultPollInterval
	if v := os.Getenv("ETCD_BACKUP_TEST_POLL_INTERVAL"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			pollInterval = parsed
		}
	}

	return baseConfig{
		HCPNamespace: hcpNS,
		HONamespace:  hoNS,
		Timeout:      time.Duration(timeout) * time.Second,
		PollInterval: time.Duration(pollInterval) * time.Second,
	}
}

func loadS3Config(t *testing.T) *s3Config {
	t.Helper()
	bucket := os.Getenv("ETCD_BACKUP_TEST_S3_BUCKET")
	region := os.Getenv("ETCD_BACKUP_TEST_S3_REGION")
	keyPrefix := os.Getenv("ETCD_BACKUP_TEST_S3_KEY_PREFIX")
	credSecret := os.Getenv("ETCD_BACKUP_TEST_S3_CREDENTIALS_SECRET")

	if bucket == "" || region == "" || keyPrefix == "" || credSecret == "" {
		return nil
	}
	return &s3Config{
		Bucket:            bucket,
		Region:            region,
		KeyPrefix:         keyPrefix,
		CredentialsSecret: credSecret,
	}
}

func loadAzureConfig(t *testing.T) *azureConfig {
	t.Helper()
	container := os.Getenv("ETCD_BACKUP_TEST_AZURE_CONTAINER")
	storageAccount := os.Getenv("ETCD_BACKUP_TEST_AZURE_STORAGE_ACCOUNT")
	keyPrefix := os.Getenv("ETCD_BACKUP_TEST_AZURE_KEY_PREFIX")
	credSecret := os.Getenv("ETCD_BACKUP_TEST_AZURE_CREDENTIALS_SECRET")

	if container == "" || storageAccount == "" || keyPrefix == "" || credSecret == "" {
		return nil
	}
	return &azureConfig{
		Container:         container,
		StorageAccount:    storageAccount,
		KeyPrefix:         keyPrefix,
		CredentialsSecret: credSecret,
	}
}

func newClient(t *testing.T) crclient.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add client-go scheme: %v", err)
	}
	if err := hyperv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hypershift scheme: %v", err)
	}

	restConfig, err := config.GetConfig()
	if err != nil {
		t.Fatalf("failed to get kubeconfig: %v", err)
	}

	k8sClient, err := crclient.New(restConfig, crclient.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("failed to create k8s client: %v", err)
	}
	return k8sClient
}

// cleanupBackup deletes the HCPEtcdBackup CR, ignoring not-found errors.
func cleanupBackup(ctx context.Context, t *testing.T, k8sClient crclient.Client, backup *hyperv1.HCPEtcdBackup) {
	t.Helper()
	if err := k8sClient.Delete(ctx, backup); crclient.IgnoreNotFound(err) != nil {
		t.Logf("warning: failed to delete HCPEtcdBackup %s/%s: %v", backup.Namespace, backup.Name, err)
	}
}

// cleanupSnapshot deletes the uploaded snapshot from cloud storage using the CLI.
// Supported URL formats:
//   - S3:    s3://<bucket>/<key>
//   - Azure: https://<account>.blob.core.windows.net/<container>/<key>
func cleanupSnapshot(t *testing.T, snapshotURL string) {
	t.Helper()
	if snapshotURL == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), cleanupCmdTimeout)
	defer cancel()

	if strings.HasPrefix(snapshotURL, "s3://") {
		t.Logf("Deleting S3 snapshot: %s", snapshotURL)
		out, err := exec.CommandContext(ctx, "aws", "s3", "rm", snapshotURL).CombinedOutput()
		if err != nil {
			t.Logf("warning: failed to delete S3 snapshot %s: %v\n%s", snapshotURL, err, string(out))
		}
		return
	}

	if strings.Contains(snapshotURL, ".blob.core.windows.net/") {
		parsed, err := url.Parse(snapshotURL)
		if err != nil {
			t.Logf("warning: failed to parse Azure snapshot URL %s: %v", snapshotURL, err)
			return
		}
		host := parsed.Hostname()
		account := strings.SplitN(host, ".", 2)[0]
		// path is /<container>/<key>
		parts := strings.SplitN(strings.TrimPrefix(parsed.Path, "/"), "/", 2)
		if len(parts) != 2 {
			t.Logf("warning: unexpected Azure blob URL path: %s", parsed.Path)
			return
		}
		container, blobName := parts[0], parts[1]

		t.Logf("Deleting Azure blob: account=%s container=%s blob=%s", account, container, blobName)
		// Use account key for deletion to avoid RBAC issues with the current user.
		keyOut, keyErr := exec.CommandContext(ctx, "az", "storage", "account", "keys", "list",
			"--account-name", account,
			"--query", "[0].value",
			"-o", "tsv",
		).Output()
		if keyErr != nil {
			t.Logf("warning: failed to get storage account key for %s: %v", account, keyErr)
			return
		}
		accountKey := strings.TrimSpace(string(keyOut))
		out, err := exec.CommandContext(ctx, "az", "storage", "blob", "delete",
			"--account-name", account,
			"--account-key", accountKey,
			"--container-name", container,
			"--name", blobName,
		).CombinedOutput()
		if err != nil {
			t.Logf("warning: failed to delete Azure blob %s: %v\n%s", snapshotURL, err, string(out))
		}
		return
	}

	t.Logf("warning: unknown snapshot URL scheme, skipping cleanup: %s", snapshotURL)
}

// waitForBackupCondition polls the HCPEtcdBackup CR until the BackupCompleted
// condition reaches a terminal state (True, BackupFailed, or BackupRejected).
// It also monitors the backup Job to detect pod-level failures early (e.g.
// init container errors) instead of waiting for the full timeout.
func waitForBackupCondition(ctx context.Context, k8sClient crclient.Client, backup *hyperv1.HCPEtcdBackup, cfg baseConfig) (*metav1.Condition, error) {
	var lastCond *metav1.Condition
	err := wait.PollUntilContextTimeout(ctx, cfg.PollInterval, cfg.Timeout, true, func(ctx context.Context) (bool, error) {
		if err := k8sClient.Get(ctx, crclient.ObjectKeyFromObject(backup), backup); err != nil {
			return false, err
		}

		cond := meta.FindStatusCondition(backup.Status.Conditions, string(hyperv1.BackupCompleted))
		if cond != nil {
			lastCond = cond
			if cond.Status == metav1.ConditionTrue {
				return true, nil
			}
			if cond.Reason == hyperv1.BackupFailedReason || cond.Reason == hyperv1.BackupRejectedReason {
				return true, nil
			}
		}

		// Check Job/Pod status to detect early failures (e.g. init container errors)
		// before the controller has had time to update the CR condition.
		jobList := &batchv1.JobList{}
		if err := k8sClient.List(ctx, jobList,
			crclient.InNamespace(cfg.HONamespace),
			crclient.MatchingLabels{
				etcdbackup.LabelBackupName:   backup.Name,
				etcdbackup.LabelHCPNamespace: backup.Namespace,
			},
		); err == nil && len(jobList.Items) > 0 {
			job := &jobList.Items[0]
			for _, c := range job.Status.Conditions {
				if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
					return true, fmt.Errorf("backup Job %q failed: %s", job.Name, c.Message)
				}
			}
		}

		return false, nil
	})
	return lastCond, err
}

// verifyBackupSuccess asserts that the backup completed successfully and
// that the controller cleaned up temporary resources (NetworkPolicy, RBAC).
// It also verifies the EtcdBackupSucceeded condition was propagated to the HCP.
func verifyBackupSuccess(ctx context.Context, t *testing.T, g Gomega, k8sClient crclient.Client, backup *hyperv1.HCPEtcdBackup, cfg baseConfig) {
	t.Helper()

	cond, err := waitForBackupCondition(ctx, k8sClient, backup, cfg)
	g.Expect(err).ToNot(HaveOccurred(), "timed out waiting for BackupCompleted condition")
	g.Expect(cond).ToNot(BeNil(), "BackupCompleted condition not found")

	g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
		"expected BackupCompleted=True, got reason=%s message=%s", cond.Reason, cond.Message)
	g.Expect(cond.Reason).To(Equal(hyperv1.BackupSucceededReason))
	t.Logf("Backup completed successfully: reason=%s message=%s", cond.Reason, cond.Message)

	g.Expect(backup.Status.SnapshotURL).ToNot(BeEmpty(), "snapshotURL should be populated after successful backup")
	t.Logf("Snapshot URL: %s", backup.Status.SnapshotURL)

	// Verify NetworkPolicy cleaned up
	np := &networkingv1.NetworkPolicy{}
	err = k8sClient.Get(ctx, crclient.ObjectKey{Name: etcdbackup.NetworkPolicyName, Namespace: cfg.HCPNamespace}, np)
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(),
		"NetworkPolicy %s should be cleaned up after backup completion", etcdbackup.NetworkPolicyName)
	t.Log("Verified: NetworkPolicy cleaned up")

	// Verify RBAC cleaned up
	role := &rbacv1.Role{}
	err = k8sClient.Get(ctx, crclient.ObjectKey{Name: etcdbackup.RBACName, Namespace: cfg.HCPNamespace}, role)
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(),
		"Role %s should be cleaned up after backup completion", etcdbackup.RBACName)

	rb := &rbacv1.RoleBinding{}
	err = k8sClient.Get(ctx, crclient.ObjectKey{Name: etcdbackup.RBACName, Namespace: cfg.HCPNamespace}, rb)
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(),
		"RoleBinding %s should be cleaned up after backup completion", etcdbackup.RBACName)
	t.Log("Verified: RBAC cleaned up")

	// Verify backup Job exists in HO namespace
	jobList := &batchv1.JobList{}
	g.Expect(k8sClient.List(ctx, jobList,
		crclient.InNamespace(cfg.HONamespace),
		crclient.MatchingLabels{
			etcdbackup.LabelBackupName:   backup.Name,
			etcdbackup.LabelHCPNamespace: cfg.HCPNamespace,
		},
	)).To(Succeed())
	g.Expect(jobList.Items).To(HaveLen(1), "expected exactly one backup Job")
	t.Logf("Backup Job: %s (succeeded=%d)", jobList.Items[0].Name, jobList.Items[0].Status.Succeeded)

	// Verify EtcdBackupSucceeded condition propagated to HCP
	hcpList := &hyperv1.HostedControlPlaneList{}
	g.Expect(k8sClient.List(ctx, hcpList, crclient.InNamespace(cfg.HCPNamespace))).To(Succeed())
	g.Expect(hcpList.Items).ToNot(BeEmpty(), "HostedControlPlane not found in namespace %s", cfg.HCPNamespace)
	hcpCond := meta.FindStatusCondition(hcpList.Items[0].Status.Conditions, "EtcdBackupSucceeded")
	g.Expect(hcpCond).ToNot(BeNil(), "EtcdBackupSucceeded condition should be set on HCP")
	g.Expect(hcpCond.Status).To(Equal(metav1.ConditionTrue),
		"expected EtcdBackupSucceeded=True on HCP, got reason=%s", hcpCond.Reason)
	t.Logf("Verified: HCP EtcdBackupSucceeded condition: status=%s reason=%s", hcpCond.Status, hcpCond.Reason)
}

// TestWhenS3BackupCRIsCreated_ControllerShouldCompleteBackupSuccessfully
// validates the full HCPEtcdBackup controller flow with S3 storage:
//  1. Create an HCPEtcdBackup CR with S3 storage config
//  2. Wait for the controller to reconcile and complete the backup
//  3. Verify BackupCompleted=True with snapshotURL populated
//  4. Verify controller cleaned up NetworkPolicy and RBAC after completion
//  5. Verify EtcdBackupSucceeded condition on HCP
func TestWhenS3BackupCRIsCreated_ControllerShouldCompleteBackupSuccessfully(t *testing.T) {
	cfg := loadBaseConfig(t)
	s3 := loadS3Config(t)
	if s3 == nil {
		t.Skip("S3 env vars not set (ETCD_BACKUP_TEST_S3_BUCKET, ETCD_BACKUP_TEST_S3_REGION, ETCD_BACKUP_TEST_S3_KEY_PREFIX, ETCD_BACKUP_TEST_S3_CREDENTIALS_SECRET)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout+1*time.Minute)
	defer cancel()

	k8sClient := newClient(t)
	g := NewWithT(t)

	backupName := fmt.Sprintf("etcd-backup-s3-%d", time.Now().Unix())
	backup := &hyperv1.HCPEtcdBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backupName,
			Namespace: cfg.HCPNamespace,
		},
		Spec: hyperv1.HCPEtcdBackupSpec{
			Storage: hyperv1.HCPEtcdBackupStorage{
				StorageType: hyperv1.S3BackupStorage,
				S3: hyperv1.HCPEtcdBackupS3{
					Bucket:    s3.Bucket,
					Region:    s3.Region,
					KeyPrefix: s3.KeyPrefix,
					Credentials: hyperv1.SecretReference{
						Name: s3.CredentialsSecret,
					},
				},
			},
		},
	}

	t.Cleanup(func() {
		cleanupSnapshot(t, backup.Status.SnapshotURL)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		t.Log("Cleaning up S3 HCPEtcdBackup CR...")
		cleanupBackup(cleanupCtx, t, k8sClient, backup)
	})

	t.Logf("Creating HCPEtcdBackup %s in %s (S3: %s/%s)", backupName, cfg.HCPNamespace, s3.Bucket, s3.KeyPrefix)
	g.Expect(k8sClient.Create(ctx, backup)).To(Succeed())

	t.Log("Waiting for S3 backup to complete...")
	verifyBackupSuccess(ctx, t, g, k8sClient, backup, cfg)
}

// TestWhenAzureBlobBackupCRIsCreated_ControllerShouldCompleteBackupSuccessfully
// validates the full HCPEtcdBackup controller flow with Azure Blob storage:
//  1. Create an HCPEtcdBackup CR with AzureBlob storage config
//  2. Wait for the controller to reconcile and complete the backup
//  3. Verify BackupCompleted=True with snapshotURL populated
//  4. Verify controller cleaned up NetworkPolicy and RBAC after completion
//  5. Verify EtcdBackupSucceeded condition on HCP
func TestWhenAzureBlobBackupCRIsCreated_ControllerShouldCompleteBackupSuccessfully(t *testing.T) {
	cfg := loadBaseConfig(t)
	az := loadAzureConfig(t)
	if az == nil {
		t.Skip("Azure env vars not set (ETCD_BACKUP_TEST_AZURE_CONTAINER, ETCD_BACKUP_TEST_AZURE_STORAGE_ACCOUNT, ETCD_BACKUP_TEST_AZURE_KEY_PREFIX, ETCD_BACKUP_TEST_AZURE_CREDENTIALS_SECRET)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout+1*time.Minute)
	defer cancel()

	k8sClient := newClient(t)
	g := NewWithT(t)

	backupName := fmt.Sprintf("etcd-backup-azure-%d", time.Now().Unix())
	backup := &hyperv1.HCPEtcdBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backupName,
			Namespace: cfg.HCPNamespace,
		},
		Spec: hyperv1.HCPEtcdBackupSpec{
			Storage: hyperv1.HCPEtcdBackupStorage{
				StorageType: hyperv1.AzureBlobBackupStorage,
				AzureBlob: hyperv1.HCPEtcdBackupAzureBlob{
					Container:      az.Container,
					StorageAccount: az.StorageAccount,
					KeyPrefix:      az.KeyPrefix,
					Credentials: hyperv1.SecretReference{
						Name: az.CredentialsSecret,
					},
				},
			},
		},
	}

	t.Cleanup(func() {
		cleanupSnapshot(t, backup.Status.SnapshotURL)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		t.Log("Cleaning up Azure HCPEtcdBackup CR...")
		cleanupBackup(cleanupCtx, t, k8sClient, backup)
	})

	t.Logf("Creating HCPEtcdBackup %s in %s (Azure: %s/%s/%s)", backupName, cfg.HCPNamespace, az.StorageAccount, az.Container, az.KeyPrefix)
	g.Expect(k8sClient.Create(ctx, backup)).To(Succeed())

	t.Log("Waiting for Azure backup to complete...")
	verifyBackupSuccess(ctx, t, g, k8sClient, backup, cfg)
}

// TestWhenBackupCRHasInvalidCredentials_ControllerShouldSetBackupFailed
// validates that the controller correctly reports failure when the
// credentials Secret does not exist.
func TestWhenBackupCRHasInvalidCredentials_ControllerShouldSetBackupFailed(t *testing.T) {
	cfg := loadBaseConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout+1*time.Minute)
	defer cancel()

	k8sClient := newClient(t)
	g := NewWithT(t)

	backupName := fmt.Sprintf("etcd-backup-fail-%d", time.Now().Unix())
	backup := &hyperv1.HCPEtcdBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backupName,
			Namespace: cfg.HCPNamespace,
		},
		Spec: hyperv1.HCPEtcdBackupSpec{
			Storage: hyperv1.HCPEtcdBackupStorage{
				StorageType: hyperv1.S3BackupStorage,
				S3: hyperv1.HCPEtcdBackupS3{
					Bucket:    "nonexistent-bucket",
					Region:    "us-east-1",
					KeyPrefix: "test",
					Credentials: hyperv1.SecretReference{
						Name: "nonexistent-credentials-secret",
					},
				},
			},
		},
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		t.Log("Cleaning up failed HCPEtcdBackup CR...")
		cleanupBackup(cleanupCtx, t, k8sClient, backup)
	})

	t.Logf("Creating HCPEtcdBackup %s with invalid credentials", backupName)
	g.Expect(k8sClient.Create(ctx, backup)).To(Succeed())

	t.Log("Waiting for backup to reach terminal state...")
	cond, err := waitForBackupCondition(ctx, k8sClient, backup, cfg)
	g.Expect(err).ToNot(HaveOccurred(), "timed out waiting for terminal condition")
	g.Expect(cond).ToNot(BeNil(), "BackupCompleted condition not found")

	g.Expect(cond.Status).To(Equal(metav1.ConditionFalse),
		"expected BackupCompleted=False for invalid credentials")
	g.Expect(cond.Reason).To(Equal(hyperv1.BackupFailedReason),
		"expected reason BackupFailed, got %s: %s", cond.Reason, cond.Message)
	t.Logf("Backup failed as expected: reason=%s message=%s", cond.Reason, cond.Message)

	// Verify controller cleaned up resources even on failure
	np := &networkingv1.NetworkPolicy{}
	err = k8sClient.Get(ctx, crclient.ObjectKey{Name: etcdbackup.NetworkPolicyName, Namespace: cfg.HCPNamespace}, np)
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(),
		"NetworkPolicy should be cleaned up after backup failure")
	t.Log("Verified: NetworkPolicy cleaned up after failure")
}
