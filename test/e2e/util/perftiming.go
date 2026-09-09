package util

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (
	// PerfTimingLogPrefix prefixes every structured timing record emitted to stdout so that
	// CI log scrapers can recover the records even when no artifact directory is available.
	PerfTimingLogPrefix = "PERF_TIMING "

	// perfTimingSchemaVersion is bumped whenever the shape of PerfTimingRecord changes in a way
	// that is not backwards compatible with previously published baselines.
	perfTimingSchemaVersion = 1

	// perfTimingArtifactSubdir is the directory under the artifact dir where timing records are written.
	perfTimingArtifactSubdir = "perf-timings"

	// perfTimingArtifactFile is the JSON Lines file collecting all timing records of a test run.
	perfTimingArtifactFile = "timings.json"

	// PerfTimingDirEnvVar overrides the directory timing records are written to. When unset, the
	// records are written under $ARTIFACT_DIR/perf-timings.
	PerfTimingDirEnvVar = "HYPERSHIFT_PERF_TIMING_DIR"
)

// Metric names tracked for bring-up and node join baselines. These are the keys the baseline
// tracking tooling (hack/track-perf-baselines.py) groups measurements by.
const (
	// MetricHostedControlPlaneBringUp measures the time from HostedCluster creation until all
	// HostedControlPlane readiness conditions are true.
	MetricHostedControlPlaneBringUp = "hosted_control_plane_bring_up"

	// MetricNodePoolNodeJoin measures the time from NodePool creation until all desired nodes joined.
	MetricNodePoolNodeJoin = "nodepool_node_join"
)

// PerfTimingRecord is a single structured timing measurement emitted by an e2e test. Records are
// written as JSON Lines so that repeated CI runs can be concatenated and fed to the baseline
// tracking tooling without any further parsing.
type PerfTimingRecord struct {
	SchemaVersion int    `json:"schemaVersion"`
	Timestamp     string `json:"timestamp"`
	// Metric is one of the Metric* constants above.
	Metric string `json:"metric"`
	// Test is the name of the Go test that produced the measurement.
	Test string `json:"test"`
	// Platform is the HostedCluster platform, e.g. AWS, Azure or KubeVirt. Infrastructure
	// provisioning times differ enough between platforms that baselines must be grouped by it.
	Platform  string `json:"platform"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// DurationSeconds is the total elapsed time of the measured phase.
	DurationSeconds float64 `json:"durationSeconds"`
	// WaitDurationSeconds is the time the test itself spent waiting. It differs from
	// DurationSeconds when the wait started after the resource was created.
	WaitDurationSeconds float64 `json:"waitDurationSeconds"`
	// SubPhases breaks the total duration down into milestones, keyed by milestone name with the
	// number of seconds elapsed between resource creation and that milestone being reached.
	SubPhases map[string]float64 `json:"subPhases,omitempty"`
	// Metadata carries contextual information used to slice baselines, e.g. release image or
	// node count.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// perfTimingMu serializes appends to the shared timing file across parallel tests.
var perfTimingMu sync.Mutex

// RecordPerfTiming emits a structured timing record to stdout and, when an artifact directory is
// configured, appends it to the timings JSON Lines file. Failures to persist a record are logged
// but never fail the test: timing collection is best effort and must not affect e2e outcomes.
func RecordPerfTiming(t testing.TB, record PerfTimingRecord) {
	t.Helper()

	record.SchemaVersion = perfTimingSchemaVersion
	if record.Timestamp == "" {
		record.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	if record.Test == "" {
		record.Test = t.Name()
	}
	if record.Metadata == nil {
		record.Metadata = map[string]string{}
	}
	for key, envVar := range map[string]string{
		"jobName": "JOB_NAME",
		"buildID": "BUILD_ID",
		"prowJob": "PROW_JOB_ID",
	} {
		if _, alreadySet := record.Metadata[key]; alreadySet {
			continue
		}
		if value := os.Getenv(envVar); value != "" {
			record.Metadata[key] = value
		}
	}

	serialized, err := json.Marshal(record)
	if err != nil {
		t.Logf("failed to serialize perf timing record for %s: %v", record.Metric, err)
		return
	}

	// Always log to stdout so the measurement survives even without an artifact directory.
	t.Logf("%s%s", PerfTimingLogPrefix, serialized)

	dir := perfTimingDir()
	if dir == "" {
		return
	}

	perfTimingMu.Lock()
	defer perfTimingMu.Unlock()

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Logf("failed to create perf timing directory %s: %v", dir, err)
		return
	}
	path := filepath.Join(dir, perfTimingArtifactFile)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Logf("failed to open perf timing file %s: %v", path, err)
		return
	}
	defer file.Close()
	if _, err := file.Write(append(serialized, '\n')); err != nil {
		t.Logf("failed to write perf timing record to %s: %v", path, err)
	}
}

// perfTimingDir resolves the directory timing records are written to, preferring the explicit
// override over the CI-provided artifact directory. An empty return value disables persistence.
func perfTimingDir() string {
	if dir := os.Getenv(PerfTimingDirEnvVar); dir != "" {
		return dir
	}
	if dir := os.Getenv("ARTIFACT_DIR"); dir != "" {
		return filepath.Join(dir, perfTimingArtifactSubdir)
	}
	return ""
}

// hostedControlPlaneBringUpMilestones are the conditions broken out as sub-phases of control plane
// bring-up, in the order they are expected to be reached.
var hostedControlPlaneBringUpMilestones = []hyperv1.ConditionType{
	hyperv1.InfrastructureReady,
	hyperv1.EtcdAvailable,
	hyperv1.KubeAPIServerAvailable,
	hyperv1.HostedControlPlaneAvailable,
}

// bringUpSubPhases computes, for each bring-up milestone, the number of seconds between the
// HostedControlPlane creation and the condition last transitioning to true. Conditions that are
// missing or not true are omitted rather than reported as zero, so that a partial record is never
// mistaken for a fast one.
func bringUpSubPhases(creation time.Time, conditions []metav1.Condition) map[string]float64 {
	subPhases := map[string]float64{}
	for _, milestone := range hostedControlPlaneBringUpMilestones {
		for _, condition := range conditions {
			if condition.Type != string(milestone) || condition.Status != metav1.ConditionTrue {
				continue
			}
			elapsed := condition.LastTransitionTime.Time.Sub(creation).Seconds()
			if elapsed < 0 {
				elapsed = 0
			}
			subPhases[string(milestone)] = elapsed
			break
		}
	}
	return subPhases
}

// nodePoolTimingMetadata summarizes the NodePool shape so that node join baselines can be compared
// across equivalent scales, e.g. a 10 node pool against a 10 node baseline.
func nodePoolTimingMetadata(nodePools []*hyperv1.NodePool) map[string]string {
	var replicas int32
	names := make([]string, 0, len(nodePools))
	instanceTypes := make([]string, 0, len(nodePools))
	for _, nodePool := range nodePools {
		replicas += ptr.Deref(nodePool.Spec.Replicas, 0)
		names = append(names, nodePool.Name)
		if instanceType := nodePoolInstanceType(nodePool); instanceType != "" {
			instanceTypes = append(instanceTypes, instanceType)
		}
	}
	metadata := map[string]string{
		"nodeCount":     fmt.Sprintf("%d", replicas),
		"nodePoolCount": fmt.Sprintf("%d", len(nodePools)),
		"nodePools":     strings.Join(names, ","),
	}
	if len(instanceTypes) > 0 {
		metadata["instanceTypes"] = strings.Join(instanceTypes, ",")
	}
	return metadata
}

// nodePoolInstanceType returns the platform specific machine size of the NodePool, when the
// platform exposes one. Instance type materially changes provisioning time, so baselines are only
// comparable when it matches.
func nodePoolInstanceType(nodePool *hyperv1.NodePool) string {
	switch nodePool.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		if nodePool.Spec.Platform.AWS != nil {
			return nodePool.Spec.Platform.AWS.InstanceType
		}
	case hyperv1.AzurePlatform:
		if nodePool.Spec.Platform.Azure != nil {
			return nodePool.Spec.Platform.Azure.VMSize
		}
	case hyperv1.OpenStackPlatform:
		if nodePool.Spec.Platform.OpenStack != nil {
			return nodePool.Spec.Platform.OpenStack.Flavor
		}
	}
	return ""
}
