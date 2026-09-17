package util

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestBringUpSubPhases(t *testing.T) {
	creation := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	condition := func(conditionType hyperv1.ConditionType, status metav1.ConditionStatus, offset time.Duration) metav1.Condition {
		return metav1.Condition{
			Type:               string(conditionType),
			Status:             status,
			LastTransitionTime: metav1.NewTime(creation.Add(offset)),
		}
	}

	tests := []struct {
		name       string
		conditions []metav1.Condition
		expected   map[string]float64
	}{
		{
			name: "When all bring-up conditions are true, it should report every milestone",
			conditions: []metav1.Condition{
				condition(hyperv1.InfrastructureReady, metav1.ConditionTrue, 3*time.Minute),
				condition(hyperv1.EtcdAvailable, metav1.ConditionTrue, 5*time.Minute),
				condition(hyperv1.KubeAPIServerAvailable, metav1.ConditionTrue, 7*time.Minute),
				condition(hyperv1.HostedControlPlaneAvailable, metav1.ConditionTrue, 10*time.Minute),
			},
			expected: map[string]float64{
				string(hyperv1.InfrastructureReady):         180,
				string(hyperv1.EtcdAvailable):               300,
				string(hyperv1.KubeAPIServerAvailable):      420,
				string(hyperv1.HostedControlPlaneAvailable): 600,
			},
		},
		{
			name: "When a condition is false, it should omit that milestone",
			conditions: []metav1.Condition{
				condition(hyperv1.EtcdAvailable, metav1.ConditionTrue, 5*time.Minute),
				condition(hyperv1.KubeAPIServerAvailable, metav1.ConditionFalse, 7*time.Minute),
			},
			expected: map[string]float64{
				string(hyperv1.EtcdAvailable): 300,
			},
		},
		{
			name: "When a condition transitioned before creation, it should clamp the elapsed time to zero",
			conditions: []metav1.Condition{
				condition(hyperv1.EtcdAvailable, metav1.ConditionTrue, -1*time.Minute),
			},
			expected: map[string]float64{
				string(hyperv1.EtcdAvailable): 0,
			},
		},
		{
			name:       "When no conditions are reported, it should return an empty breakdown",
			conditions: nil,
			expected:   map[string]float64{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(bringUpSubPhases(creation, test.conditions)).To(Equal(test.expected))
		})
	}
}

func TestNodePoolTimingMetadata(t *testing.T) {
	awsNodePool := func(name string, replicas int32, instanceType string) *hyperv1.NodePool {
		return &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: hyperv1.NodePoolSpec{
				Replicas: ptr.To(replicas),
				Platform: hyperv1.NodePoolPlatform{
					Type: hyperv1.AWSPlatform,
					AWS:  &hyperv1.AWSNodePoolPlatform{InstanceType: instanceType},
				},
			},
		}
	}

	tests := []struct {
		name      string
		nodePools []*hyperv1.NodePool
		expected  map[string]string
	}{
		{
			name:      "When a single NodePool is measured, it should report its scale and instance type",
			nodePools: []*hyperv1.NodePool{awsNodePool("workers", 10, "m5.large")},
			expected: map[string]string{
				"nodeCount":     "10",
				"nodePoolCount": "1",
				"nodePools":     "workers",
				"instanceTypes": "m5.large",
			},
		},
		{
			name: "When multiple NodePools are measured, it should sum the replicas",
			nodePools: []*hyperv1.NodePool{
				awsNodePool("workers-1", 30, "m5.large"),
				awsNodePool("workers-2", 20, "m5.xlarge"),
			},
			expected: map[string]string{
				"nodeCount":     "50",
				"nodePoolCount": "2",
				"nodePools":     "workers-1,workers-2",
				"instanceTypes": "m5.large,m5.xlarge",
			},
		},
		{
			name: "When the platform exposes no instance type, it should omit it",
			nodePools: []*hyperv1.NodePool{{
				ObjectMeta: metav1.ObjectMeta{Name: "workers"},
				Spec: hyperv1.NodePoolSpec{
					Replicas: ptr.To(int32(3)),
					Platform: hyperv1.NodePoolPlatform{Type: hyperv1.KubevirtPlatform},
				},
			}},
			expected: map[string]string{
				"nodeCount":     "3",
				"nodePoolCount": "1",
				"nodePools":     "workers",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(nodePoolTimingMetadata(test.nodePools)).To(Equal(test.expected))
		})
	}
}

func TestRecordPerfTiming(t *testing.T) {
	g := NewWithT(t)

	dir := t.TempDir()
	t.Setenv(PerfTimingDirEnvVar, dir)
	t.Setenv("JOB_NAME", "e2e-aws")

	RecordPerfTiming(t, PerfTimingRecord{
		Metric:          MetricHostedControlPlaneBringUp,
		Platform:        string(hyperv1.AWSPlatform),
		Namespace:       "e2e-clusters",
		Name:            "example",
		DurationSeconds: 612.5,
		SubPhases:       map[string]float64{string(hyperv1.EtcdAvailable): 120},
	})
	RecordPerfTiming(t, PerfTimingRecord{
		Metric:          MetricNodePoolNodeJoin,
		Platform:        string(hyperv1.AWSPlatform),
		Namespace:       "e2e-clusters",
		Name:            "example",
		DurationSeconds: 420,
	})

	file, err := os.Open(filepath.Join(dir, perfTimingArtifactFile))
	g.Expect(err).ToNot(HaveOccurred())
	defer file.Close()

	var records []PerfTimingRecord
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record PerfTimingRecord
		g.Expect(json.Unmarshal(scanner.Bytes(), &record)).To(Succeed())
		records = append(records, record)
	}
	g.Expect(scanner.Err()).ToNot(HaveOccurred())

	g.Expect(records).To(HaveLen(2), "each record should be appended as its own JSON line")
	g.Expect(records[0].Metric).To(Equal(MetricHostedControlPlaneBringUp))
	g.Expect(records[0].SchemaVersion).To(Equal(perfTimingSchemaVersion))
	g.Expect(records[0].Test).To(Equal(t.Name()), "the test name should be filled in automatically")
	g.Expect(records[0].Timestamp).ToNot(BeEmpty())
	g.Expect(records[0].Metadata).To(HaveKeyWithValue("jobName", "e2e-aws"))
	g.Expect(records[0].SubPhases).To(HaveKeyWithValue(string(hyperv1.EtcdAvailable), 120.0))
	g.Expect(records[1].Metric).To(Equal(MetricNodePoolNodeJoin))
}

func TestPerfTimingDir(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		expected    func(base string) string
		expectEmpty bool
	}{
		{
			name:     "When the override is set, it should take precedence over the artifact dir",
			env:      map[string]string{PerfTimingDirEnvVar: "/tmp/override", "ARTIFACT_DIR": "/tmp/artifacts"},
			expected: func(string) string { return "/tmp/override" },
		},
		{
			name:     "When only the artifact dir is set, it should nest the timings under it",
			env:      map[string]string{PerfTimingDirEnvVar: "", "ARTIFACT_DIR": "/tmp/artifacts"},
			expected: func(string) string { return filepath.Join("/tmp/artifacts", perfTimingArtifactSubdir) },
		},
		{
			name:        "When no directory is configured, it should disable persistence",
			env:         map[string]string{PerfTimingDirEnvVar: "", "ARTIFACT_DIR": ""},
			expectEmpty: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			if test.expectEmpty {
				g.Expect(perfTimingDir()).To(BeEmpty())
				return
			}
			g.Expect(perfTimingDir()).To(Equal(test.expected("")))
		})
	}
}
