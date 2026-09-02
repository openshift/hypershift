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

// WaitForEtcdInitAndVerifyLogs polls until the etcd-init container in etcd-0 has
// terminated successfully, then reads and verifies its logs contain snapshot restore
// traces. This must run before the post-restore health check: after restore, CPO
// clears restoreSnapshotURL which triggers a second StatefulSet rollout that replaces
// the pod without the etcd-init container.
//
// Behavior:
//   - Retries when the pod is not found, the init container has not terminated yet,
//     or the log stream cannot be opened (transient errors from pod replacement).
//   - Returns immediately with an error if the init container exits with a nonzero
//     code, or if the logs indicate the restore was skipped or incomplete (terminal).
//   - Returns a timeout error if RestoreTimeout expires before the init container
//     terminates and logs are verified.
//
// TODO: In a follow-up PR, move log interpretation into the etcd controller (CPOv2)
// so the restore result is reported via a declared ConfigMap resource, decoupling the
// test from pod lifecycle timing entirely.
func WaitForEtcdInitAndVerifyLogs(ctx context.Context, logger logr.Logger, kubeClient kubernetes.Interface, controlPlaneNamespace string) error {
	return wait.PollUntilContextTimeout(ctx, PollInterval, RestoreTimeout, true, func(ctx context.Context) (bool, error) {
		pod, err := kubeClient.CoreV1().Pods(controlPlaneNamespace).Get(ctx, EtcdPodName, metav1.GetOptions{})
		if err != nil {
			logger.V(1).Info("waiting for etcd pod", "error", err)
			return false, nil
		}

		for _, cs := range pod.Status.InitContainerStatuses {
			if cs.Name != EtcdInitContainerName {
				continue
			}
			if cs.State.Terminated == nil {
				logger.V(1).Info("etcd-init container not yet terminated, waiting")
				return false, nil
			}
			if cs.State.Terminated.ExitCode != 0 {
				return false, fmt.Errorf("etcd-init container in pod %s/%s exited with code %d: %s",
					controlPlaneNamespace, EtcdPodName, cs.State.Terminated.ExitCode, cs.State.Terminated.Reason)
			}
			result, readErr := readEtcdInitLogs(ctx, kubeClient, controlPlaneNamespace)
			if readErr != nil {
				logger.V(1).Info("failed to read etcd-init logs, retrying", "error", readErr)
				return false, nil
			}
			if validationErr := validateEtcdInitResult(logger, result); validationErr != nil {
				return false, validationErr
			}
			return true, nil
		}

		logger.V(1).Info("etcd-init container not found in pod status, waiting")
		return false, nil
	})
}

// readEtcdInitLogs retrieves and parses the etcd-init container logs from etcd-0.
// Errors are transient (pod replaced between status check and log read).
func readEtcdInitLogs(ctx context.Context, kubeClient kubernetes.Interface, controlPlaneNamespace string) (*etcdInitLogResult, error) {
	podLogOpts := &corev1.PodLogOptions{
		Container: EtcdInitContainerName,
	}

	req := kubeClient.CoreV1().Pods(controlPlaneNamespace).GetLogs(EtcdPodName, podLogOpts)
	logStream, err := req.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to stream %s container logs from %s: %w", EtcdInitContainerName, EtcdPodName, err)
	}
	defer logStream.Close()

	return parseEtcdInitLogs(logStream)
}

// validateEtcdInitResult checks the parsed log result for expected restore markers.
// Errors are terminal (the logs won't change on retry).
func validateEtcdInitResult(logger logr.Logger, result *etcdInitLogResult) error {
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
