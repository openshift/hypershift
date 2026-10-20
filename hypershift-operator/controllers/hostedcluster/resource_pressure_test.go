package hostedcluster

import (
	"context"
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileManagementClusterResourcePressure(t *testing.T) {
	const cpNamespace = "cp-ns"

	// Helper: create a CPC with PodSchedulingFailed condition.
	schedulingFailedCPC := func(name, message string) *hyperv1.ControlPlaneComponent {
		return &hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cpNamespace},
			Status: hyperv1.ControlPlaneComponentStatus{
				Conditions: []metav1.Condition{
					{
						Type:    string(hyperv1.ControlPlaneComponentAvailable),
						Status:  metav1.ConditionFalse,
						Reason:  hyperv1.ReasonPodSchedulingFailed,
						Message: message,
					},
				},
			},
		}
	}

	// Helper: create a CPC that is available.
	availableCPC := func(name string) *hyperv1.ControlPlaneComponent {
		return &hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cpNamespace},
			Status: hyperv1.ControlPlaneComponentStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(hyperv1.ControlPlaneComponentAvailable),
						Status: metav1.ConditionTrue,
						Reason: "AsExpected",
					},
				},
			},
		}
	}

	// Helper: create a CPC unavailable for a non-scheduling reason.
	unavailableNonSchedulingCPC := func(name string) *hyperv1.ControlPlaneComponent {
		return &hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cpNamespace},
			Status: hyperv1.ControlPlaneComponentStatus{
				Conditions: []metav1.Condition{
					{
						Type:    string(hyperv1.ControlPlaneComponentAvailable),
						Status:  metav1.ConditionFalse,
						Reason:  hyperv1.ReasonCrashLoopBackOff,
						Message: "Pod etcd-0 container etcd: CrashLoopBackOff",
					},
				},
			},
		}
	}

	testCases := []struct {
		name           string
		objects        []client.Object
		expectedStatus metav1.ConditionStatus
		expectedReason string
		expectedMsg    string
		notExpectedMsg string
	}{
		{
			name:           "When no CPCs exist it should report no pressure",
			objects:        nil,
			expectedStatus: metav1.ConditionFalse,
			expectedReason: hyperv1.ReasonNoPressure,
		},
		{
			name:           "When all CPCs are available it should report no pressure",
			objects:        []client.Object{availableCPC("kube-apiserver")},
			expectedStatus: metav1.ConditionFalse,
			expectedReason: hyperv1.ReasonNoPressure,
		},
		{
			name: "When a CPC has PodSchedulingFailed it should report pressure with resource type",
			objects: []client.Object{
				schedulingFailedCPC("etcd", "Pod etcd-0 is unschedulable: Insufficient cpu, Insufficient memory"),
			},
			expectedStatus: metav1.ConditionTrue,
			expectedReason: hyperv1.ReasonFailedScheduling,
			expectedMsg:    "1 component with scheduling failures (etcd): Insufficient cpu, Insufficient memory",
		},
		{
			name: "When multiple CPCs have PodSchedulingFailed it should list all affected components",
			objects: []client.Object{
				schedulingFailedCPC("etcd", "Pod etcd-0 is unschedulable: Insufficient cpu"),
				schedulingFailedCPC("kube-apiserver", "Pod kube-apiserver-abc is unschedulable: Insufficient cpu, Insufficient memory"),
				availableCPC("kube-scheduler"),
			},
			expectedStatus: metav1.ConditionTrue,
			expectedReason: hyperv1.ReasonFailedScheduling,
			expectedMsg:    "2 components with scheduling failures (etcd, kube-apiserver)",
		},
		{
			name: "When a CPC is unavailable for a non-scheduling reason it should not report pressure",
			objects: []client.Object{
				unavailableNonSchedulingCPC("etcd"),
				availableCPC("kube-apiserver"),
			},
			expectedStatus: metav1.ConditionFalse,
			expectedReason: hyperv1.ReasonNoPressure,
		},
		{
			name: "When a CPC has no Available condition it should not report pressure",
			objects: []client.Object{
				&hyperv1.ControlPlaneComponent{
					ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver", Namespace: cpNamespace},
					Status:     hyperv1.ControlPlaneComponentStatus{},
				},
			},
			expectedStatus: metav1.ConditionFalse,
			expectedReason: hyperv1.ReasonNoPressure,
		},
		{
			name: "When scheduling failure message has no Insufficient match it should still report pressure",
			objects: []client.Object{
				schedulingFailedCPC("etcd", "Pod etcd-0 is unschedulable"),
			},
			expectedStatus: metav1.ConditionTrue,
			expectedReason: hyperv1.ReasonFailedScheduling,
			expectedMsg:    "1 component with scheduling failures (etcd)",
		},
		{
			name: "Message should not contain management cluster node names",
			objects: []client.Object{
				// The CPO sanitizes messages before setting CPC conditions, so
				// node names should already be stripped. This test verifies that
				// our message building doesn't reintroduce them.
				schedulingFailedCPC("etcd", "Pod etcd-0 is unschedulable: Insufficient cpu"),
			},
			expectedStatus: metav1.ConditionTrue,
			expectedReason: hyperv1.ReasonFailedScheduling,
			notExpectedMsg: "ip-10-0-1-42",
		},
		{
			name: "Components should be sorted alphabetically in message",
			objects: []client.Object{
				schedulingFailedCPC("kube-scheduler", "Pod kube-scheduler-0 is unschedulable: Insufficient memory"),
				schedulingFailedCPC("etcd", "Pod etcd-0 is unschedulable: Insufficient cpu"),
				schedulingFailedCPC("kube-apiserver", "Pod kube-apiserver-0 is unschedulable: Insufficient cpu"),
			},
			expectedStatus: metav1.ConditionTrue,
			expectedReason: hyperv1.ReasonFailedScheduling,
			expectedMsg:    "3 components with scheduling failures (etcd, kube-apiserver, kube-scheduler)",
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
		objects = append(objects, &hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("component-%03d", i),
				Namespace: "cp-ns",
			},
			Status: hyperv1.ControlPlaneComponentStatus{
				Conditions: []metav1.Condition{
					{
						Type:    string(hyperv1.ControlPlaneComponentAvailable),
						Status:  metav1.ConditionFalse,
						Reason:  hyperv1.ReasonPodSchedulingFailed,
						Message: "Pod pod-0 is unschedulable: Insufficient cpu, Insufficient memory",
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

func TestBuildResourcePressureMessage(t *testing.T) {
	testCases := []struct {
		name          string
		components    []string
		resourceTypes map[string]struct{}
		expected      string
	}{
		{
			name:          "When no resource types are extracted, it should return component-list-only message",
			components:    []string{"etcd", "kube-apiserver"},
			resourceTypes: map[string]struct{}{},
			expected:      "2 components with scheduling failures (etcd, kube-apiserver)",
		},
		{
			name:       "When a single resource type is present, it should include it in message",
			components: []string{"etcd"},
			resourceTypes: map[string]struct{}{
				"Insufficient cpu": {},
			},
			expected: "1 component with scheduling failures (etcd): Insufficient cpu",
		},
		{
			name:       "When multiple resource types are present, it should sort them alphabetically",
			components: []string{"etcd", "kube-apiserver", "kube-scheduler"},
			resourceTypes: map[string]struct{}{
				"Insufficient memory": {},
				"Insufficient cpu":    {},
			},
			expected: "3 components with scheduling failures (etcd, kube-apiserver, kube-scheduler): Insufficient cpu, Insufficient memory",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			msg := buildResourcePressureMessage(tc.components, tc.resourceTypes)
			g.Expect(msg).To(Equal(tc.expected))
		})
	}
}

func TestExtractResourceTypes(t *testing.T) {
	testCases := []struct {
		name     string
		message  string
		expected map[string]struct{}
	}{
		{
			name:     "When message has cpu and memory, it should extract both resource types",
			message:  "Pod etcd-0 is unschedulable: Insufficient cpu, Insufficient memory",
			expected: map[string]struct{}{"Insufficient cpu": {}, "Insufficient memory": {}},
		},
		{
			name:     "When message has ephemeral-storage, it should extract the resource type",
			message:  "Pod etcd-0 is unschedulable: Insufficient ephemeral-storage",
			expected: map[string]struct{}{"Insufficient ephemeral-storage": {}},
		},
		{
			name:     "When message has no Insufficient matches, it should return empty map",
			message:  "Pod etcd-0 is unschedulable",
			expected: map[string]struct{}{},
		},
		{
			name:     "When message is empty, it should return empty map",
			message:  "",
			expected: map[string]struct{}{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			types := make(map[string]struct{})
			extractResourceTypes(tc.message, types)
			g.Expect(types).To(Equal(tc.expected))
		})
	}
}

func TestBuildResourcePressureMessageTruncation(t *testing.T) {
	g := NewGomegaWithT(t)

	// Create enough components to exceed the max message length.
	var components []string
	for i := 0; i < 50; i++ {
		components = append(components, fmt.Sprintf("very-long-component-name-%03d", i))
	}

	msg := buildResourcePressureMessage(components, map[string]struct{}{
		"Insufficient cpu":    {},
		"Insufficient memory": {},
	})
	g.Expect(len(msg)).To(BeNumerically("<=", maxResourcePressureMessageLen))
	g.Expect(msg).To(HaveSuffix("..."))
}

func TestResourcePressureMessageDoesNotContainSensitiveInfo(t *testing.T) {
	// The CPO sanitizes scheduler messages before setting CPC conditions.
	// This test verifies that the resource_pressure message builder does
	// not reintroduce any sensitive patterns.
	sensitivePatterns := []string{
		"ip-10-0-1-42",
		"ec2.internal",
		"aks-nodepool1",
		"vmss",
		"gke-mgmt-cluster",
		"ocp.example.com",
		"10.0.1.42",
		"quay.io",
	}

	// The CPC message is already sanitized by the CPO, so it should only
	// contain patterns like "Pod X is unschedulable: Insufficient cpu".
	cpcMessage := "Pod etcd-0 is unschedulable: Insufficient cpu, Insufficient memory"
	components := []string{"etcd"}
	resourceTypes := make(map[string]struct{})
	extractResourceTypes(cpcMessage, resourceTypes)
	msg := buildResourcePressureMessage(components, resourceTypes)

	g := NewGomegaWithT(t)
	for _, pattern := range sensitivePatterns {
		g.Expect(msg).NotTo(ContainSubstring(pattern))
	}
	// The message should still mention the resource type.
	g.Expect(msg).To(ContainSubstring("Insufficient cpu"))
}

func TestReconcileManagementClusterResourcePressureResourceTypesDeduplication(t *testing.T) {
	g := NewGomegaWithT(t)
	const cpNamespace = "cp-ns"

	// Multiple CPCs report the same resource type — it should appear once.
	objects := []client.Object{
		&hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: cpNamespace},
			Status: hyperv1.ControlPlaneComponentStatus{
				Conditions: []metav1.Condition{
					{
						Type:    string(hyperv1.ControlPlaneComponentAvailable),
						Status:  metav1.ConditionFalse,
						Reason:  hyperv1.ReasonPodSchedulingFailed,
						Message: "Pod etcd-0 is unschedulable: Insufficient cpu",
					},
				},
			},
		},
		&hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver", Namespace: cpNamespace},
			Status: hyperv1.ControlPlaneComponentStatus{
				Conditions: []metav1.Condition{
					{
						Type:    string(hyperv1.ControlPlaneComponentAvailable),
						Status:  metav1.ConditionFalse,
						Reason:  hyperv1.ReasonPodSchedulingFailed,
						Message: "Pod kube-apiserver-0 is unschedulable: Insufficient cpu",
					},
				},
			},
		},
	}

	builder := fake.NewClientBuilder().WithScheme(api.Scheme)
	for _, obj := range objects {
		builder = builder.WithObjects(obj)
	}

	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-hc", Namespace: "test-ns"},
	}

	reconcileManagementClusterResourcePressure(context.Background(), builder.Build(), hcluster, cpNamespace)

	cond := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.ManagementClusterResourcePressure))
	g.Expect(cond).NotTo(BeNil())
	g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	// "Insufficient cpu" should appear exactly once in the message.
	g.Expect(strings.Count(cond.Message, "Insufficient cpu")).To(Equal(1))
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
