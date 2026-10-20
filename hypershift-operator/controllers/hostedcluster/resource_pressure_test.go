package hostedcluster

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileManagementClusterResourcePressure(t *testing.T) {
	const cpNamespace = "cp-ns"

	// Helper: create a pending unschedulable pod.
	unschedulablePod := func(name string, uid types.UID, message string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cpNamespace, UID: uid},
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				Conditions: []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  "Unschedulable",
						Message: message,
					},
				},
			},
		}
	}

	// Helper: create a running/scheduled pod.
	runningPod := func(name string, uid types.UID) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cpNamespace, UID: uid},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{
					{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
				},
			},
		}
	}

	// Helper: create a FailedScheduling event.
	failedSchedulingEvent := func(name string, podName string, podUID types.UID, message string) *corev1.Event {
		return &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cpNamespace},
			InvolvedObject: corev1.ObjectReference{
				Kind:      "Pod",
				Name:      podName,
				Namespace: cpNamespace,
				UID:       podUID,
			},
			Reason:  "FailedScheduling",
			Message: message,
		}
	}

	testCases := []struct {
		name           string
		objects        []client.Object
		expectedStatus metav1.ConditionStatus
		expectedReason string
		expectedMsg    string
		notExpectedMsg string // substrings that must NOT appear in the message
	}{
		{
			name:           "When no pods exist it should report no pressure",
			objects:        nil,
			expectedStatus: metav1.ConditionFalse,
			expectedReason: hyperv1.ReasonNoPressure,
		},
		{
			name:           "When all pods are running it should report no pressure",
			objects:        []client.Object{runningPod("pod-1", "uid-1")},
			expectedStatus: metav1.ConditionFalse,
			expectedReason: hyperv1.ReasonNoPressure,
		},
		{
			name: "When a pod is unschedulable (pod status only, no events) it should report pressure with resource type",
			objects: []client.Object{
				unschedulablePod("etcd-0", "uid-etcd", "0/6 nodes are available: 3 Insufficient cpu, 2 Insufficient memory, 1 node(s) had untolerated taint {node-role.kubernetes.io/master: }. preemption: 0/6 nodes are available: 3 No preemption victims found on node."),
			},
			expectedStatus: metav1.ConditionTrue,
			expectedReason: hyperv1.ReasonFailedScheduling,
			expectedMsg:    "1 pods with FailedScheduling: 1 Insufficient cpu, 1 Insufficient memory",
		},
		{
			name: "When FailedScheduling event exists with still-unschedulable pod it should report pressure",
			objects: []client.Object{
				unschedulablePod("kube-apiserver-xyz", "uid-kas", "0/3 nodes are available: 3 Insufficient cpu."),
				failedSchedulingEvent("event-1", "kube-apiserver-xyz", "uid-kas", "0/3 nodes are available: 3 Insufficient cpu. preemption: 0/3 nodes are available: 3 No preemption victims found on node."),
			},
			expectedStatus: metav1.ConditionTrue,
			expectedReason: hyperv1.ReasonFailedScheduling,
			expectedMsg:    "Insufficient cpu",
		},
		{
			name: "When FailedScheduling event exists but pod has recovered it should report no pressure",
			objects: []client.Object{
				// Pod was rescheduled and is now running.
				runningPod("kube-apiserver-xyz", "uid-kas"),
				// Stale event still exists (hasn't reached TTL).
				failedSchedulingEvent("event-1", "kube-apiserver-xyz", "uid-kas", "0/3 nodes are available: 3 Insufficient cpu."),
			},
			expectedStatus: metav1.ConditionFalse,
			expectedReason: hyperv1.ReasonNoPressure,
		},
		{
			name: "When FailedScheduling event has stale UID (pod deleted and recreated) it should not trigger",
			objects: []client.Object{
				// New pod with new UID, running fine.
				runningPod("kube-apiserver-xyz", "uid-kas-new"),
				// Stale event from old pod (old UID).
				failedSchedulingEvent("event-1", "kube-apiserver-xyz", "uid-kas-old", "0/3 nodes are available: 3 Insufficient cpu."),
			},
			expectedStatus: metav1.ConditionFalse,
			expectedReason: hyperv1.ReasonNoPressure,
		},
		{
			name: "When multiple pods are unschedulable it should group by resource type",
			objects: []client.Object{
				unschedulablePod("etcd-0", "uid-1", "0/6 nodes are available: 3 Insufficient cpu."),
				unschedulablePod("kube-apiserver-abc", "uid-2", "0/6 nodes are available: 3 Insufficient cpu, 2 Insufficient memory."),
				unschedulablePod("kube-scheduler-def", "uid-3", "0/6 nodes are available: 2 Insufficient memory."),
			},
			expectedStatus: metav1.ConditionTrue,
			expectedReason: hyperv1.ReasonFailedScheduling,
			expectedMsg:    "3 pods with FailedScheduling:",
		},
		{
			name: "When a pending pod is not unschedulable it should not report pressure",
			objects: []client.Object{
				// Pending but scheduled (e.g., waiting for volumes).
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: cpNamespace, UID: "uid-1"},
					Status: corev1.PodStatus{
						Phase: corev1.PodPending,
						Conditions: []corev1.PodCondition{
							{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
						},
					},
				},
			},
			expectedStatus: metav1.ConditionFalse,
			expectedReason: hyperv1.ReasonNoPressure,
		},
		{
			name: "Message should not contain management cluster node names",
			objects: []client.Object{
				unschedulablePod("etcd-0", "uid-1", "0/6 nodes are available: 3 Insufficient cpu, 2 Insufficient memory, 1 node(s) had untolerated taint {node-role.kubernetes.io/master: }. preemption: 0/6 nodes are available: ip-10-0-1-42.ec2.internal No preemption victims found on node, ip-10-0-2-99.ec2.internal No preemption victims found on node."),
			},
			expectedStatus: metav1.ConditionTrue,
			expectedReason: hyperv1.ReasonFailedScheduling,
			// Node names must NOT appear.
			notExpectedMsg: "ip-10-0-1-42",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			builder := fake.NewClientBuilder().WithScheme(api.Scheme)
			for _, obj := range tc.objects {
				builder = builder.WithObjects(obj)
			}
			c := builder.Build()

			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hc",
					Namespace:  "test-ns",
					Generation: 1,
				},
			}

			reconcileManagementClusterResourcePressure(context.Background(), c, hcluster, cpNamespace)

			cond := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.ManagementClusterResourcePressure))
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(tc.expectedStatus))
			g.Expect(cond.Reason).To(Equal(tc.expectedReason))
			if tc.expectedMsg != "" {
				g.Expect(cond.Message).To(ContainSubstring(tc.expectedMsg))
			}
			if tc.notExpectedMsg != "" {
				g.Expect(cond.Message).NotTo(ContainSubstring(tc.notExpectedMsg))
			}
			// EP specifies observedGeneration is not applicable.
			g.Expect(cond.ObservedGeneration).To(Equal(int64(0)))
		})
	}
}

func TestReconcileManagementClusterResourcePressureMessageTruncation(t *testing.T) {
	g := NewGomegaWithT(t)

	var objects []client.Object
	for i := 0; i < 50; i++ {
		objects = append(objects, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("pod-%03d", i),
				Namespace: "cp-ns",
				UID:       types.UID(fmt.Sprintf("uid-%03d", i)),
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				Conditions: []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  "Unschedulable",
						Message: "0/6 nodes are available: 3 Insufficient cpu, 2 Insufficient memory.",
					},
				},
			},
		})
	}

	builder := fake.NewClientBuilder().WithScheme(api.Scheme)
	for _, obj := range objects {
		builder = builder.WithObjects(obj)
	}

	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hc",
			Namespace: "test-ns",
		},
	}

	reconcileManagementClusterResourcePressure(context.Background(), builder.Build(), hcluster, "cp-ns")

	cond := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.ManagementClusterResourcePressure))
	g.Expect(cond).NotTo(BeNil())
	g.Expect(len(cond.Message)).To(BeNumerically("<=", maxResourcePressureMessageLen))
}

func TestReconcileManagementClusterResourcePressureSecuritySanitization(t *testing.T) {
	const cpNamespace = "cp-ns"

	testCases := []struct {
		name             string
		schedulerMessage string
		mustNotContain   []string
		mustContain      string // resource type that should survive sanitization
	}{
		{
			name:             "AWS node names must not appear in message",
			schedulerMessage: "0/6 nodes are available: 3 Insufficient cpu, 2 Insufficient memory, 1 node(s) had untolerated taint {node-role.kubernetes.io/master: }. preemption: 0/6 nodes are available: ip-10-0-1-42.ec2.internal No preemption victims, ip-10-0-2-99.ec2.internal No preemption victims.",
			mustNotContain:   []string{"ip-10-0-1-42", "ip-10-0-2-99", "ec2.internal"},
			mustContain:      "Insufficient cpu",
		},
		{
			name:             "Azure VMSS node names must not appear in message",
			schedulerMessage: "0/3 nodes are available: 2 Insufficient memory. aks-nodepool1-12345678-vmss000000 had untolerated taint, aks-nodepool1-12345678-vmss000001 had untolerated taint.",
			mustNotContain:   []string{"aks-nodepool1-12345678-vmss000000", "aks-nodepool1-12345678-vmss000001", "vmss"},
			mustContain:      "Insufficient memory",
		},
		{
			name:             "GCP node names must not appear in message",
			schedulerMessage: "0/4 nodes are available: 2 Insufficient cpu. gke-mgmt-cluster-pool-0-a1b2c3d4-wxyz had untolerated taint, gke-mgmt-cluster-pool-0-e5f6g7h8-abcd had untolerated taint.",
			mustNotContain:   []string{"gke-mgmt-cluster", "a1b2c3d4", "e5f6g7h8"},
			mustContain:      "Insufficient cpu",
		},
		{
			name:             "OpenShift worker node FQDNs must not appear in message",
			schedulerMessage: "0/5 nodes are available: 1 Insufficient cpu, 2 Insufficient memory. worker-0.mgmt.ocp.example.com had untolerated taint, master-2.mgmt.ocp.example.com had untolerated taint.",
			mustNotContain:   []string{"worker-0.mgmt", "master-2.mgmt", "ocp.example.com"},
			mustContain:      "Insufficient cpu",
		},
		{
			name:             "IPv4 addresses must not appear in message",
			schedulerMessage: "0/3 nodes are available: 2 Insufficient cpu. 10.0.1.42 had taint, 172.16.0.5 had taint, 192.168.1.100 had taint.",
			mustNotContain:   []string{"10.0.1.42", "172.16.0.5", "192.168.1.100"},
			mustContain:      "Insufficient cpu",
		},
		{
			name:             "IPv6 addresses must not appear in message",
			schedulerMessage: "0/3 nodes are available: 1 Insufficient memory. fd00::1 had taint, 2001:db8::abcd:1234 had taint.",
			mustNotContain:   []string{"fd00::1", "2001:db8::abcd:1234"},
			mustContain:      "Insufficient memory",
		},
		{
			name:             "Management cluster node count must not appear in message",
			schedulerMessage: "0/42 nodes are available: 10 Insufficient cpu, 32 Insufficient memory.",
			mustNotContain:   []string{"0/42 nodes"},
			mustContain:      "Insufficient cpu",
		},
		{
			name:             "Registry hostnames must not appear in message",
			schedulerMessage: "0/6 nodes are available: 3 Insufficient memory. quay.io/openshift-release-dev image pull failed on registry.ci.openshift.org.",
			mustNotContain:   []string{"quay.io", "registry.ci.openshift.org"},
			mustContain:      "Insufficient memory",
		},
		{
			name:             "Preemption details must not appear in message",
			schedulerMessage: "0/3 nodes are available: 3 Insufficient cpu. preemption: 0/3 nodes are available: 3 No preemption victims found for incoming pod.",
			mustNotContain:   []string{"preemption", "No preemption victims"},
			mustContain:      "Insufficient cpu",
		},
		{
			name:             "Taint details must not appear in message",
			schedulerMessage: "0/3 nodes are available: 1 Insufficient cpu, 2 node(s) had untolerated taint {node-role.kubernetes.io/master: }.",
			mustNotContain:   []string{"untolerated taint", "node-role.kubernetes.io"},
			mustContain:      "Insufficient cpu",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "etcd-0", Namespace: cpNamespace, UID: "uid-1"},
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
					Conditions: []corev1.PodCondition{
						{
							Type:    corev1.PodScheduled,
							Status:  corev1.ConditionFalse,
							Reason:  "Unschedulable",
							Message: tc.schedulerMessage,
						},
					},
				},
			}

			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(pod).Build()

			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hc", Namespace: "test-ns"},
			}

			reconcileManagementClusterResourcePressure(context.Background(), c, hcluster, cpNamespace)

			cond := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.ManagementClusterResourcePressure))
			g.Expect(cond).NotTo(BeNil())
			for _, forbidden := range tc.mustNotContain {
				g.Expect(cond.Message).NotTo(ContainSubstring(forbidden),
					"message %q should not contain %q", cond.Message, forbidden)
			}
			if tc.mustContain != "" {
				g.Expect(cond.Message).To(ContainSubstring(tc.mustContain),
					"message %q should contain %q", cond.Message, tc.mustContain)
			}
		})
	}
}

func TestBuildResourcePressureMessage(t *testing.T) {
	testCases := []struct {
		name               string
		podCount           int
		resourceTypeCounts map[string]int
		expected           string
	}{
		{
			name:               "When no resource types are extracted, it should return count-only message",
			podCount:           2,
			resourceTypeCounts: map[string]int{},
			expected:           "2 pods with FailedScheduling",
		},
		{
			name:     "When a single resource type is present, it should include it in message",
			podCount: 3,
			resourceTypeCounts: map[string]int{
				"Insufficient cpu": 3,
			},
			expected: "3 pods with FailedScheduling: 3 Insufficient cpu",
		},
		{
			name:     "When multiple resource types are present, it should sort them alphabetically with counts",
			podCount: 5,
			resourceTypeCounts: map[string]int{
				"Insufficient memory": 2,
				"Insufficient cpu":    3,
			},
			expected: "5 pods with FailedScheduling: 3 Insufficient cpu, 2 Insufficient memory",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			msg := buildResourcePressureMessage(tc.podCount, tc.resourceTypeCounts)
			g.Expect(msg).To(Equal(tc.expected))
		})
	}
}

func TestExtractResourceTypes(t *testing.T) {
	testCases := []struct {
		name     string
		message  string
		expected map[string]int
	}{
		{
			name:     "When message has cpu and memory, it should extract both resource types",
			message:  "0/6 nodes are available: 3 Insufficient cpu, 2 Insufficient memory.",
			expected: map[string]int{"Insufficient cpu": 1, "Insufficient memory": 1},
		},
		{
			name:     "When message has ephemeral-storage, it should extract the resource type",
			message:  "0/3 nodes are available: 2 Insufficient ephemeral-storage.",
			expected: map[string]int{"Insufficient ephemeral-storage": 1},
		},
		{
			name:     "When message has no Insufficient matches, it should return empty map",
			message:  "0/3 nodes are available: 1 node(s) had untolerated taint.",
			expected: map[string]int{},
		},
		{
			name:     "When message is empty, it should return empty map",
			message:  "",
			expected: map[string]int{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			counts := make(map[string]int)
			extractResourceTypes(tc.message, counts)
			g.Expect(counts).To(Equal(tc.expected))
		})
	}
}

func TestPropagateInstallationStage(t *testing.T) {
	testCases := []struct {
		name          string
		hcp           *hyperv1.HostedControlPlane
		expectedStage hyperv1.InstallationStage
	}{
		{
			name:          "When HCP is nil it should preserve existing HC value",
			hcp:           nil,
			expectedStage: "",
		},
		{
			name: "When HCP has a stage it should copy to HC",
			hcp: &hyperv1.HostedControlPlane{
				Status: hyperv1.HostedControlPlaneStatus{
					Installation: &hyperv1.InstallationStatus{
						Stage:              hyperv1.InstallationStageControlPlaneProvisioning,
						LastTransitionTime: metav1.Now(),
					},
				},
			},
			expectedStage: hyperv1.InstallationStageControlPlaneProvisioning,
		},
		{
			name: "When HCP stage is empty it should copy empty value",
			hcp: &hyperv1.HostedControlPlane{
				Status: hyperv1.HostedControlPlaneStatus{},
			},
			expectedStage: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			hcluster := &hyperv1.HostedCluster{}
			propagateInstallationStage(hcluster, tc.hcp)

			var actualStage hyperv1.InstallationStage
			if hcluster.Status.Installation != nil {
				actualStage = hcluster.Status.Installation.Stage
			}
			g.Expect(actualStage).To(Equal(tc.expectedStage))
			if tc.hcp != nil && tc.hcp.Status.Installation != nil {
				g.Expect(hcluster.Status.Installation).NotTo(BeNil())
				g.Expect(hcluster.Status.Installation.LastTransitionTime).To(Equal(tc.hcp.Status.Installation.LastTransitionTime))
			}
		})
	}
}
