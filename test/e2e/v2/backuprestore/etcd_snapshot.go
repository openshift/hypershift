//go:build e2ev2 && backuprestore

package backuprestore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
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

// etcdMemberListResponse represents the JSON output of etcdctl member list -w json.
type etcdMemberListResponse struct {
	Header  etcdResponseHeader `json:"header"`
	Members []etcdMember       `json:"members"`
}

// etcdResponseHeader contains the cluster-level metadata from an etcd response.
type etcdResponseHeader struct {
	ClusterID uint64 `json:"cluster_id"`
}

// etcdMember represents a single member in an etcd cluster.
type etcdMember struct {
	ID         uint64   `json:"ID"`
	Name       string   `json:"name"`
	PeerURLs   []string `json:"peerURLs"`
	ClientURLs []string `json:"clientURLs"`
}

// parseEtcdMemberList parses the JSON output of etcdctl member list -w json.
func parseEtcdMemberList(jsonOutput string) (*etcdMemberListResponse, error) {
	var response etcdMemberListResponse
	if err := json.Unmarshal([]byte(jsonOutput), &response); err != nil {
		return nil, fmt.Errorf("failed to parse etcdctl member list output: %w", err)
	}
	return &response, nil
}

// VerifyEtcdClusterHealth verifies that all etcd members form a single cluster
// after a snapshot restore. This detects split-brain scenarios where each member
// starts as an independent 1-member cluster due to missing --name/--initial-cluster/
// --initial-cluster-token flags during snapshot restore.
func VerifyEtcdClusterHealth(ctx context.Context, logger logr.Logger, mgmtClient crclient.Client, cpNamespace string) error {
	etcdSts := cpomanifests.EtcdStatefulSet(cpNamespace)
	if err := mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(etcdSts), etcdSts); err != nil {
		return fmt.Errorf("failed to get etcd StatefulSet: %w", err)
	}
	expectedReplicas := ptr.Deref(etcdSts.Spec.Replicas, 1)

	ep := fmt.Sprintf("https://etcd-client.%s.svc:2379", cpNamespace)
	command := []string{
		"/bin/sh", "-c",
		fmt.Sprintf("/usr/bin/etcdctl --cacert=/etc/etcd/tls/etcd-ca/ca.crt --cert=/etc/etcd/tls/server/server.crt --key=/etc/etcd/tls/server/server.key --endpoints=%s member list -w json 2>/dev/null", ep),
	}

	stdout, err := e2eutil.RunCommandInPod(ctx, mgmtClient, "etcd", cpNamespace, command, "etcd", 5*time.Minute)
	if err != nil {
		return fmt.Errorf("failed to run etcdctl member list: %w", err)
	}

	result, err := parseEtcdMemberList(stdout)
	if err != nil {
		return err
	}

	if int32(len(result.Members)) != expectedReplicas {
		return fmt.Errorf("expected %d etcd members but found %d; this may indicate a split-brain cluster where each member started as an independent 1-member cluster",
			expectedReplicas, len(result.Members))
	}

	for _, member := range result.Members {
		if member.Name == "" {
			return fmt.Errorf("etcd member %d has no name, indicating it has not fully started", member.ID)
		}
		if len(member.PeerURLs) == 0 {
			return fmt.Errorf("etcd member %s has no peer URLs", member.Name)
		}
	}

	logger.Info("etcd cluster health verified: all members form a single cluster",
		"memberCount", len(result.Members),
		"clusterID", result.Header.ClusterID)

	return nil
}

// VerifyNoEtcdCrashLoop verifies that no etcd pod has excessive container restarts,
// which would indicate the member ID mismatch / WAL corruption issue caused by
// incorrect init container ordering during snapshot restore.
func VerifyNoEtcdCrashLoop(ctx context.Context, logger logr.Logger, mgmtClient crclient.Client, cpNamespace string, maxRestarts int32) error {
	etcdSts := cpomanifests.EtcdStatefulSet(cpNamespace)
	if err := mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(etcdSts), etcdSts); err != nil {
		return fmt.Errorf("failed to get etcd StatefulSet: %w", err)
	}

	etcdPods := &corev1.PodList{}
	if err := mgmtClient.List(ctx, etcdPods, &crclient.ListOptions{
		Namespace:     cpNamespace,
		LabelSelector: labels.Set(etcdSts.Spec.Selector.MatchLabels).AsSelector(),
	}); err != nil {
		return fmt.Errorf("failed to list etcd pods: %w", err)
	}

	if len(etcdPods.Items) == 0 {
		return fmt.Errorf("no etcd pods found in namespace %s", cpNamespace)
	}

	for _, pod := range etcdPods.Items {
		foundEtcdStatus := false
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name == "etcd" {
				foundEtcdStatus = true
				if cs.RestartCount > maxRestarts {
					return fmt.Errorf("etcd container in pod %s has %d restarts (threshold: %d), indicating possible CrashLoopBackOff from member ID mismatch during restore",
						pod.Name, cs.RestartCount, maxRestarts)
				}
			}
		}
		if !foundEtcdStatus {
			return fmt.Errorf("etcd pod %s has no etcd container status", pod.Name)
		}
	}

	logger.Info("no etcd CrashLoopBackOff detected", "podCount", len(etcdPods.Items))
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
