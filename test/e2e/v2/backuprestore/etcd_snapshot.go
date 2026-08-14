//go:build e2ev2 && backuprestore

package backuprestore

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// EtcdPodName is the name of the etcd pod whose init container logs are verified.
	EtcdPodName = "etcd-0"
	// EtcdInitContainerName is the name of the init container in the etcd pod.
	EtcdInitContainerName = "etcd-init"

	// HCPEtcdBackupNamePrefix is the prefix used by the OADP plugin when creating
	// HCPEtcdBackup resources. The full name follows the pattern: oadp-<BackupName>-<random>.
	HCPEtcdBackupNamePrefix = "oadp-"

	// logRestoringSnapshot is emitted by etcdutl/etcdctl when starting a snapshot restore.
	logRestoringSnapshot = "restoring snapshot"
	// logRestoredSnapshot is emitted by etcdutl/etcdctl when snapshot restore completes.
	logRestoredSnapshot = "restored snapshot"
	// logNotRestoringSnapshot indicates the restore was skipped because data already existed.
	logNotRestoringSnapshot = "not empty, not restoring snapshot"
)

// MatchesHCPEtcdBackupName checks whether an HCPEtcdBackup resource name matches the
// expected naming pattern for a given OADP backup name. The OADP plugin creates
// HCPEtcdBackup resources with the naming pattern: oadp-<BackupName>-<random>.
func MatchesHCPEtcdBackupName(hcpEtcdBackupName, oadpBackupName string) bool {
	return strings.HasPrefix(hcpEtcdBackupName, HCPEtcdBackupNamePrefix+oadpBackupName+"-")
}

// WaitForHCPEtcdBackupCondition waits for an HCPEtcdBackup resource matching the given
// OADP backup name to have a BackupCompleted condition with the specified status.
// HCPEtcdBackup names follow the pattern: oadp-<BackupName>-<random>.
func WaitForHCPEtcdBackupCondition(testCtx *internal.TestContext, backupName string, expectedStatus metav1.ConditionStatus) error {
	return wait.PollUntilContextTimeout(testCtx.Context, PollInterval, BackupTimeout, true, func(ctx context.Context) (bool, error) {
		hcpEtcdBackupList := &hyperv1.HCPEtcdBackupList{}
		if err := testCtx.MgmtClient.List(ctx, hcpEtcdBackupList, crclient.InNamespace(testCtx.ControlPlaneNamespace)); err != nil {
			return false, fmt.Errorf("failed to list HCPEtcdBackup resources: %w", err)
		}

		for _, backup := range hcpEtcdBackupList.Items {
			if !MatchesHCPEtcdBackupName(backup.Name, backupName) {
				continue
			}
			condition := meta.FindStatusCondition(backup.Status.Conditions, string(hyperv1.BackupCompleted))
			if condition == nil {
				return false, nil
			}
			if condition.Status == expectedStatus {
				return true, nil
			}
			// If the condition is explicitly False, the backup failed - stop polling.
			if expectedStatus == metav1.ConditionTrue && condition.Status == metav1.ConditionFalse {
				return false, fmt.Errorf("HCPEtcdBackup %s has BackupCompleted=False: reason=%s, message=%s",
					backup.Name, condition.Reason, condition.Message)
			}
			return false, nil
		}
		return false, nil
	})
}

// VerifyEtcdInitLogs retrieves the etcd-init container logs from the etcd-0 pod in the
// control plane namespace and verifies that they contain expected snapshot restore traces.
// The expected log lines from etcdutl/etcdctl indicate a successful snapshot restore:
//   - "restoring snapshot" (restore started)
//   - "restored snapshot" (restore completed)
//
// It also checks that the restore was not skipped due to existing data:
//   - "not empty, not restoring snapshot" must NOT be present
func VerifyEtcdInitLogs(ctx context.Context, logger logr.Logger, kubeClient kubernetes.Interface, controlPlaneNamespace string) error {
	podLogOpts := &corev1.PodLogOptions{
		Container: EtcdInitContainerName,
	}

	req := kubeClient.CoreV1().Pods(controlPlaneNamespace).GetLogs(EtcdPodName, podLogOpts)
	logStream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("failed to stream %s container logs from %s: %w", EtcdInitContainerName, EtcdPodName, err)
	}
	defer logStream.Close()

	result, err := parseEtcdInitLogs(logStream)
	if err != nil {
		return err
	}

	logger.Info("etcd-init container logs scanned", "lines", result.lineCount)

	if result.restoreSkipped {
		for _, line := range result.tailLines {
			logger.V(1).Info("etcd-init tail", "log", line)
		}
		return fmt.Errorf("etcd-init logs contain '%s'; restore was skipped because data directory was not empty", logNotRestoringSnapshot)
	}
	if !result.restoreStarted {
		for _, line := range result.tailLines {
			logger.V(1).Info("etcd-init tail", "log", line)
		}
		return fmt.Errorf("etcd-init logs do not contain '%s'; snapshot restore may not have started", logRestoringSnapshot)
	}
	if !result.restoreCompleted {
		for _, line := range result.tailLines {
			logger.V(1).Info("etcd-init tail", "log", line)
		}
		return fmt.Errorf("etcd-init logs do not contain '%s'; snapshot restore may have failed", logRestoredSnapshot)
	}

	return nil
}

// etcdInitLogResult holds the results of parsing etcd-init container logs.
type etcdInitLogResult struct {
	restoreStarted   bool
	restoreCompleted bool
	restoreSkipped   bool
	lineCount        int
	tailLines        []string
}

// parseEtcdInitLogs scans etcd-init container log output and checks for expected
// snapshot restore trace messages from etcdutl/etcdctl.
func parseEtcdInitLogs(reader io.Reader) (*etcdInitLogResult, error) {
	const tailSize = 50

	result := &etcdInitLogResult{}

	// Use a ring buffer so old strings become eligible for GC immediately
	// instead of being retained by the underlying slice array.
	ring := make([]string, tailSize)
	ringIdx := 0
	ringLen := 0

	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 256*1024)
	scanner.Buffer(buf, 512*1024)
	for scanner.Scan() {
		line := scanner.Text()
		result.lineCount++
		ring[ringIdx] = line
		ringIdx = (ringIdx + 1) % tailSize
		if ringLen < tailSize {
			ringLen++
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, logNotRestoringSnapshot) {
			result.restoreSkipped = true
		} else if strings.Contains(lower, logRestoredSnapshot) {
			result.restoreCompleted = true
		} else if strings.Contains(lower, logRestoringSnapshot) {
			result.restoreStarted = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading etcd-init logs: %w", err)
	}

	// Flatten the ring buffer into chronological order.
	result.tailLines = make([]string, ringLen)
	start := (ringIdx - ringLen + tailSize) % tailSize
	for i := range ringLen {
		result.tailLines[i] = ring[(start+i)%tailSize]
	}

	return result, nil
}
