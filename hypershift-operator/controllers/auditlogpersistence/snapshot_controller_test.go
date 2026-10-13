package auditlogpersistence

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	auditlogpersistencev1alpha1 "github.com/openshift/hypershift/api/auditlogpersistence/v1alpha1"
	hyperapi "github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
)

// validateNoSnapshotsCreated verifies that no snapshots exist in the given namespace
func validateNoSnapshotsCreated(g *WithT, c client.Client, namespace string, errorMsg string) {
	snapshotList := &snapshotv1.VolumeSnapshotList{}
	err := c.List(context.Background(), snapshotList, client.InNamespace(namespace))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(snapshotList.Items).To(HaveLen(0), errorMsg)
}

// validatePodAnnotationsUnchanged verifies that pod annotations have not been modified
func validatePodAnnotationsUnchanged(g *WithT, c client.Client, originalPod *corev1.Pod, errorMsg string) {
	updatedPod := &corev1.Pod{}
	err := c.Get(context.Background(), types.NamespacedName{Name: originalPod.Name, Namespace: originalPod.Namespace}, updatedPod)
	g.Expect(err).ToNot(HaveOccurred())
	// Normalize nil annotations to empty map for comparison
	originalAnnotations := originalPod.Annotations
	if originalAnnotations == nil {
		originalAnnotations = make(map[string]string)
	}
	updatedAnnotations := updatedPod.Annotations
	if updatedAnnotations == nil {
		updatedAnnotations = make(map[string]string)
	}
	g.Expect(updatedAnnotations).To(Equal(originalAnnotations), errorMsg)
}

// validateNoSideEffects verifies that no snapshots were created and pod annotations were not modified
func validateNoSideEffects(g *WithT, c client.Client, pod *corev1.Pod, snapshotErrorMsg, annotationErrorMsg string) {
	validateNoSnapshotsCreated(g, c, pod.Namespace, snapshotErrorMsg)
	validatePodAnnotationsUnchanged(g, c, pod, annotationErrorMsg)
}

func TestSnapshotReconciler_Reconcile(t *testing.T) {
	tests := []struct {
		name              string
		pod               *corev1.Pod
		namespace         *corev1.Namespace
		config            *auditlogpersistencev1alpha1.AuditLogPersistenceConfig
		pvc               *corev1.PersistentVolumeClaim
		existingSnapshots []*snapshotv1.VolumeSnapshot
		expectedError     bool
		validateResult    func(*WithT, client.Client, *corev1.Pod)
	}{
		{
			name: "When pod is not found, it should return without error",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				},
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hcp-namespace",
					Labels: map[string]string{
						controlPlaneNamespaceLabel: "true",
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						Enabled: true,
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				validateNoSnapshotsCreated(g, c, pod.Namespace, "No snapshots should be created when pod doesn't exist")
			},
		},
		{
			name: "When pod is not a kube-apiserver pod, it should return without error",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-pod",
					Namespace: "hcp-namespace",
					Labels: map[string]string{
						"app": "other-app",
					},
				},
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hcp-namespace",
					Labels: map[string]string{
						controlPlaneNamespaceLabel: "true",
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						Enabled: true,
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				validateNoSideEffects(g, c, pod,
					"No snapshots should be created for non-kube-apiserver pods",
					"Pod annotations should not be modified")
			},
		},
		{
			name: "When namespace is not a control plane namespace, it should return without error",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "regular-namespace",
					Labels: map[string]string{
						kubeAPIServerLabel: kubeAPIServerLabelValue,
					},
				},
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "regular-namespace",
					Labels: map[string]string{
						"other-label": "value",
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						Enabled: true,
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				validateNoSideEffects(g, c, pod,
					"No snapshots should be created for non-control-plane namespaces",
					"Pod annotations should not be modified")
			},
		},
		{
			name: "When AuditLogPersistenceConfig is not found, it should return without error",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
					Labels: map[string]string{
						kubeAPIServerLabel: kubeAPIServerLabelValue,
					},
				},
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hcp-namespace",
					Labels: map[string]string{
						controlPlaneNamespaceLabel: "true",
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				validateNoSideEffects(g, c, pod,
					"No snapshots should be created when config is not found",
					"Pod annotations should not be modified")
			},
		},
		{
			name: "When feature is disabled, it should return without error",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
					Labels: map[string]string{
						kubeAPIServerLabel: kubeAPIServerLabelValue,
					},
				},
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hcp-namespace",
					Labels: map[string]string{
						controlPlaneNamespaceLabel: "true",
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: false,
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						Enabled: true,
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				validateNoSideEffects(g, c, pod,
					"No snapshots should be created when feature is disabled",
					"Pod annotations should not be modified")
			},
		},
		{
			name: "When snapshots are disabled, it should return without error",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
					Labels: map[string]string{
						kubeAPIServerLabel: kubeAPIServerLabelValue,
					},
				},
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hcp-namespace",
					Labels: map[string]string{
						controlPlaneNamespaceLabel: "true",
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						Enabled: false,
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				validateNoSideEffects(g, c, pod,
					"No snapshots should be created when snapshots are disabled",
					"Pod annotations should not be modified")
			},
		},
		{
			name: "When restart count has not increased, it should return without error",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
					Labels: map[string]string{
						kubeAPIServerLabel: kubeAPIServerLabelValue,
					},
					Annotations: map[string]string{
						lastObservedRestartCountAnnotation: "5",
					},
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:         "kube-apiserver",
							RestartCount: 5,
						},
					},
				},
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hcp-namespace",
					Labels: map[string]string{
						controlPlaneNamespaceLabel: "true",
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						Enabled: true,
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				validateNoSideEffects(g, c, pod,
					"No snapshots should be created when restart count hasn't increased",
					"Pod annotations should not be modified when restart count hasn't increased")
			},
		},
		{
			name: "When PVC is not found, it should update observed restart count but return without error",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
					Labels: map[string]string{
						kubeAPIServerLabel: kubeAPIServerLabelValue,
					},
					Annotations: map[string]string{
						lastObservedRestartCountAnnotation: "4",
					},
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:         "kube-apiserver",
							RestartCount: 5,
						},
					},
				},
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hcp-namespace",
					Labels: map[string]string{
						controlPlaneNamespaceLabel: "true",
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						Enabled: true,
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				validateNoSnapshotsCreated(g, c, pod.Namespace, "No snapshots should be created when PVC is not found")
				// Verify that lastObservedRestartCount was updated even though PVC is not found
				updatedPod := &corev1.Pod{}
				err := c.Get(context.Background(), types.NamespacedName{Name: "kube-apiserver-abc123", Namespace: "hcp-namespace"}, updatedPod)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(updatedPod.Annotations[lastObservedRestartCountAnnotation]).To(Equal("5"), "Should update observed restart count even when PVC is not found")
			},
		},
		{
			name: "When restart count increased and conditions are met, it should create snapshot and update annotations",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
					Labels: map[string]string{
						kubeAPIServerLabel: kubeAPIServerLabelValue,
					},
					Annotations: map[string]string{
						lastObservedRestartCountAnnotation: "4",
					},
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:         "kube-apiserver",
							RestartCount: 5,
						},
					},
				},
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hcp-namespace",
					Labels: map[string]string{
						controlPlaneNamespaceLabel: "true",
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						Enabled:                 true,
						MinInterval:             "1h",
						PerPodRetentionCount:    ptr.To(int32(10)),
						NamespaceRetentionCount: ptr.To(int32(50)),
					},
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kas-audit-logs-kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				// Verify snapshot was created
				snapshotList := &snapshotv1.VolumeSnapshotList{}
				err := c.List(context.Background(), snapshotList, client.InNamespace("hcp-namespace"), client.MatchingLabels{
					auditLogsPVCLabelKey: "kas-audit-logs-kube-apiserver-abc123",
				})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(snapshotList.Items).To(HaveLen(1))
				snapshot := snapshotList.Items[0]
				g.Expect(snapshot.Labels[auditLogsPVCLabelKey]).To(Equal("kas-audit-logs-kube-apiserver-abc123"))
				g.Expect(snapshot.Labels[auditLogsPodLabelKey]).To(Equal("kube-apiserver-abc123"))
				g.Expect(snapshot.Spec.Source.PersistentVolumeClaimName).ToNot(BeNil())
				g.Expect(*snapshot.Spec.Source.PersistentVolumeClaimName).To(Equal("kas-audit-logs-kube-apiserver-abc123"))

				// Verify pod annotations were updated
				updatedPod := &corev1.Pod{}
				err = c.Get(context.Background(), types.NamespacedName{Name: "kube-apiserver-abc123", Namespace: "hcp-namespace"}, updatedPod)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(updatedPod.Annotations[lastObservedRestartCountAnnotation]).To(Equal("5"))
				g.Expect(updatedPod.Annotations[lastSnapshotTimeAnnotation]).ToNot(BeEmpty())
			},
		},
		{
			name: "When minimum interval has not passed, it should update observed restart count but skip snapshot creation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
					Labels: map[string]string{
						kubeAPIServerLabel: kubeAPIServerLabelValue,
					},
					Annotations: map[string]string{
						lastObservedRestartCountAnnotation: "4",
						lastSnapshotTimeAnnotation:         time.Now().Format(time.RFC3339),
					},
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:         "kube-apiserver",
							RestartCount: 5,
						},
					},
				},
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hcp-namespace",
					Labels: map[string]string{
						controlPlaneNamespaceLabel: "true",
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						Enabled:     true,
						MinInterval: "1h",
					},
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kas-audit-logs-kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				validateNoSnapshotsCreated(g, c, pod.Namespace, "No snapshots should be created when minimum interval has not passed")
				// Verify that lastObservedRestartCount was updated even though snapshot was skipped
				updatedPod := &corev1.Pod{}
				err := c.Get(context.Background(), types.NamespacedName{Name: "kube-apiserver-abc123", Namespace: "hcp-namespace"}, updatedPod)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(updatedPod.Annotations[lastObservedRestartCountAnnotation]).To(Equal("5"))
				// Verify lastSnapshotTime was not updated
				g.Expect(updatedPod.Annotations[lastSnapshotTimeAnnotation]).To(Equal(pod.Annotations[lastSnapshotTimeAnnotation]))
			},
		},
		{
			name: "When corrupted restart count annotation exists, it should reset it and continue",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
					Labels: map[string]string{
						kubeAPIServerLabel: kubeAPIServerLabelValue,
					},
					Annotations: map[string]string{
						lastObservedRestartCountAnnotation: "invalid",
					},
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:         "kube-apiserver",
							RestartCount: 5,
						},
					},
				},
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hcp-namespace",
					Labels: map[string]string{
						controlPlaneNamespaceLabel: "true",
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						Enabled: true,
					},
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kas-audit-logs-kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				// Verify annotation was reset and then updated after snapshot creation
				// When annotation is corrupted, it's reset to 0, then since restart count (5) > 0,
				// a snapshot is created and annotation is updated to current restart count
				updatedPod := &corev1.Pod{}
				err := c.Get(context.Background(), types.NamespacedName{Name: "kube-apiserver-abc123", Namespace: "hcp-namespace"}, updatedPod)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(updatedPod.Annotations[lastObservedRestartCountAnnotation]).To(Equal("5"))
			},
		},
		{
			name: "When multiple restarts occur within min interval, it should update observed count but only snapshot once after interval",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
					Labels: map[string]string{
						kubeAPIServerLabel: kubeAPIServerLabelValue,
					},
					Annotations: map[string]string{
						lastObservedRestartCountAnnotation: "1",
						lastSnapshotTimeAnnotation:         time.Now().Add(-2 * time.Hour).Format(time.RFC3339), // 2 hours ago
					},
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:         "kube-apiserver",
							RestartCount: 3, // Restarted 2 more times (from 1 to 3)
						},
					},
				},
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hcp-namespace",
					Labels: map[string]string{
						controlPlaneNamespaceLabel: "true",
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						Enabled:     true,
						MinInterval: "1h",
					},
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kas-audit-logs-kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				// Verify snapshot was created (minInterval has passed)
				snapshotList := &snapshotv1.VolumeSnapshotList{}
				err := c.List(context.Background(), snapshotList, client.InNamespace("hcp-namespace"), client.MatchingLabels{
					auditLogsPVCLabelKey: "kas-audit-logs-kube-apiserver-abc123",
				})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(snapshotList.Items).To(HaveLen(1), "Should create one snapshot after minInterval passes")

				// Verify annotations were updated
				updatedPod := &corev1.Pod{}
				err = c.Get(context.Background(), types.NamespacedName{Name: "kube-apiserver-abc123", Namespace: "hcp-namespace"}, updatedPod)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(updatedPod.Annotations[lastObservedRestartCountAnnotation]).To(Equal("3"), "Should update to highest observed restart count")
				g.Expect(updatedPod.Annotations[lastSnapshotTimeAnnotation]).ToNot(BeEmpty(), "Should update snapshot time")
			},
		},
		{
			name: "When multiple restarts occur but min interval has not passed, it should update observed count but not snapshot",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
					Labels: map[string]string{
						kubeAPIServerLabel: kubeAPIServerLabelValue,
					},
					Annotations: map[string]string{
						lastObservedRestartCountAnnotation: "1",
						lastSnapshotTimeAnnotation:         time.Now().Add(-30 * time.Minute).Format(time.RFC3339), // 30 minutes ago
					},
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:         "kube-apiserver",
							RestartCount: 3, // Restarted 2 more times (from 1 to 3)
						},
					},
				},
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hcp-namespace",
					Labels: map[string]string{
						controlPlaneNamespaceLabel: "true",
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						Enabled:     true,
						MinInterval: "1h",
					},
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kas-audit-logs-kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				// Verify no snapshot was created (minInterval has not passed)
				validateNoSnapshotsCreated(g, c, pod.Namespace, "No snapshots should be created when minimum interval has not passed")

				// Verify lastObservedRestartCount was updated
				updatedPod := &corev1.Pod{}
				err := c.Get(context.Background(), types.NamespacedName{Name: "kube-apiserver-abc123", Namespace: "hcp-namespace"}, updatedPod)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(updatedPod.Annotations[lastObservedRestartCountAnnotation]).To(Equal("3"), "Should update to highest observed restart count even without snapshot")
				// Verify lastSnapshotTime was not updated
				g.Expect(updatedPod.Annotations[lastSnapshotTimeAnnotation]).To(Equal(pod.Annotations[lastSnapshotTimeAnnotation]), "Should not update snapshot time when skipping snapshot")
			},
		},
		{
			name: "When VolumeSnapshotClassName is specified, it should be set in snapshot",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
					Labels: map[string]string{
						kubeAPIServerLabel: kubeAPIServerLabelValue,
					},
					Annotations: map[string]string{
						lastObservedRestartCountAnnotation: "4",
					},
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:         "kube-apiserver",
							RestartCount: 5,
						},
					},
				},
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hcp-namespace",
					Labels: map[string]string{
						controlPlaneNamespaceLabel: "true",
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						Enabled:                 true,
						MinInterval:             "1h",
						PerPodRetentionCount:    ptr.To(int32(10)),
						NamespaceRetentionCount: ptr.To(int32(50)),
						VolumeSnapshotClassName: "custom-snapshot-class",
					},
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kas-audit-logs-kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				// Verify snapshot has VolumeSnapshotClassName set
				snapshotList := &snapshotv1.VolumeSnapshotList{}
				err := c.List(context.Background(), snapshotList, client.InNamespace("hcp-namespace"), client.MatchingLabels{
					auditLogsPVCLabelKey: "kas-audit-logs-kube-apiserver-abc123",
				})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(snapshotList.Items).To(HaveLen(1))
				snapshot := snapshotList.Items[0]
				g.Expect(snapshot.Spec.VolumeSnapshotClassName).ToNot(BeNil())
				g.Expect(*snapshot.Spec.VolumeSnapshotClassName).To(Equal("custom-snapshot-class"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Setup fake client with scheme
			var objects []client.Object
			if tt.pod != nil {
				objects = append(objects, tt.pod)
			}
			if tt.namespace != nil {
				objects = append(objects, tt.namespace)
			}
			if tt.config != nil {
				objects = append(objects, tt.config)
			}
			if tt.pvc != nil {
				objects = append(objects, tt.pvc)
			}
			for _, snapshot := range tt.existingSnapshots {
				objects = append(objects, snapshot)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(hyperapi.Scheme).
				WithObjects(objects...).
				Build()

			reconciler := &SnapshotReconciler{
				client: fakeClient,
				log:    logr.Discard(),
			}

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.pod.Name,
					Namespace: tt.pod.Namespace,
				},
			}

			result, err := reconciler.Reconcile(context.Background(), req)

			if tt.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(result).To(Equal(reconcile.Result{}))
				if tt.validateResult != nil {
					tt.validateResult(g, fakeClient, tt.pod)
				}
			}
		})
	}
}

func TestSnapshotReconciler_manageRetention(t *testing.T) {
	tests := []struct {
		name              string
		pod               *corev1.Pod
		pvc               *corev1.PersistentVolumeClaim
		config            *auditlogpersistencev1alpha1.AuditLogPersistenceConfig
		existingSnapshots []*snapshotv1.VolumeSnapshot
		expectedError     bool
		validateResult    func(*WithT, client.Client)
	}{
		{
			name: "When snapshots are under per-pod retention limit, it should not delete any",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kas-audit-logs-kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						PerPodRetentionCount:    ptr.To(int32(10)),
						NamespaceRetentionCount: ptr.To(int32(50)),
					},
				},
			},
			existingSnapshots: []*snapshotv1.VolumeSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "snapshot-1",
						Namespace: "hcp-namespace",
						Labels: map[string]string{
							auditLogsPVCLabelKey: "kas-audit-logs-kube-apiserver-abc123",
						},
						CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Hour)),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "snapshot-2",
						Namespace: "hcp-namespace",
						Labels: map[string]string{
							auditLogsPVCLabelKey: "kas-audit-logs-kube-apiserver-abc123",
						},
						CreationTimestamp: metav1.NewTime(time.Now().Add(-1 * time.Hour)),
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client) {
				snapshotList := &snapshotv1.VolumeSnapshotList{}
				err := c.List(context.Background(), snapshotList, client.InNamespace("hcp-namespace"), client.MatchingLabels{
					auditLogsPVCLabelKey: "kas-audit-logs-kube-apiserver-abc123",
				})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(snapshotList.Items).To(HaveLen(2))
			},
		},
		{
			name: "When snapshots exceed per-pod retention limit, it should delete oldest ones",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kas-audit-logs-kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						PerPodRetentionCount:    ptr.To(int32(3)),
						NamespaceRetentionCount: ptr.To(int32(50)),
					},
				},
			},
			existingSnapshots: []*snapshotv1.VolumeSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "snapshot-oldest",
						Namespace: "hcp-namespace",
						Labels: map[string]string{
							auditLogsPVCLabelKey: "kas-audit-logs-kube-apiserver-abc123",
						},
						CreationTimestamp: metav1.NewTime(time.Now().Add(-5 * time.Hour)),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "snapshot-old",
						Namespace: "hcp-namespace",
						Labels: map[string]string{
							auditLogsPVCLabelKey: "kas-audit-logs-kube-apiserver-abc123",
						},
						CreationTimestamp: metav1.NewTime(time.Now().Add(-4 * time.Hour)),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "snapshot-middle",
						Namespace: "hcp-namespace",
						Labels: map[string]string{
							auditLogsPVCLabelKey: "kas-audit-logs-kube-apiserver-abc123",
						},
						CreationTimestamp: metav1.NewTime(time.Now().Add(-3 * time.Hour)),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "snapshot-recent",
						Namespace: "hcp-namespace",
						Labels: map[string]string{
							auditLogsPVCLabelKey: "kas-audit-logs-kube-apiserver-abc123",
						},
						CreationTimestamp: metav1.NewTime(time.Now().Add(-1 * time.Hour)),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "snapshot-newest",
						Namespace: "hcp-namespace",
						Labels: map[string]string{
							auditLogsPVCLabelKey: "kas-audit-logs-kube-apiserver-abc123",
						},
						CreationTimestamp: metav1.NewTime(time.Now()),
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client) {
				snapshotList := &snapshotv1.VolumeSnapshotList{}
				err := c.List(context.Background(), snapshotList, client.InNamespace("hcp-namespace"), client.MatchingLabels{
					auditLogsPVCLabelKey: "kas-audit-logs-kube-apiserver-abc123",
				})
				g.Expect(err).ToNot(HaveOccurred())
				// Should have 3 snapshots remaining (oldest 2 deleted)
				g.Expect(snapshotList.Items).To(HaveLen(3))
				// Verify oldest ones are deleted
				names := make([]string, len(snapshotList.Items))
				for i := range snapshotList.Items {
					names[i] = snapshotList.Items[i].Name
				}
				g.Expect(names).ToNot(ContainElement("snapshot-oldest"))
				g.Expect(names).ToNot(ContainElement("snapshot-old"))
				g.Expect(names).To(ContainElement("snapshot-middle"))
				g.Expect(names).To(ContainElement("snapshot-recent"))
				g.Expect(names).To(ContainElement("snapshot-newest"))
			},
		},
		{
			name: "When snapshots exceed namespace retention limit, it should delete oldest ones",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kas-audit-logs-kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						PerPodRetentionCount:    ptr.To(int32(10)),
						NamespaceRetentionCount: ptr.To(int32(3)),
					},
				},
			},
			existingSnapshots: []*snapshotv1.VolumeSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "snapshot-oldest",
						Namespace: "hcp-namespace",
						Labels: map[string]string{
							controlPlaneNamespaceLabelKey: "hcp-namespace",
						},
						CreationTimestamp: metav1.NewTime(time.Now().Add(-5 * time.Hour)),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "snapshot-old",
						Namespace: "hcp-namespace",
						Labels: map[string]string{
							controlPlaneNamespaceLabelKey: "hcp-namespace",
						},
						CreationTimestamp: metav1.NewTime(time.Now().Add(-4 * time.Hour)),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "snapshot-recent",
						Namespace: "hcp-namespace",
						Labels: map[string]string{
							controlPlaneNamespaceLabelKey: "hcp-namespace",
						},
						CreationTimestamp: metav1.NewTime(time.Now().Add(-1 * time.Hour)),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "snapshot-newest",
						Namespace: "hcp-namespace",
						Labels: map[string]string{
							controlPlaneNamespaceLabelKey: "hcp-namespace",
						},
						CreationTimestamp: metav1.NewTime(time.Now()),
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client) {
				snapshotList := &snapshotv1.VolumeSnapshotList{}
				err := c.List(context.Background(), snapshotList, client.InNamespace("hcp-namespace"), client.MatchingLabels{
					controlPlaneNamespaceLabelKey: "hcp-namespace",
				})
				g.Expect(err).ToNot(HaveOccurred())
				// Should have 3 snapshots remaining (oldest 1 deleted)
				g.Expect(snapshotList.Items).To(HaveLen(3))
				// Verify oldest one is deleted
				names := make([]string, len(snapshotList.Items))
				for i := range snapshotList.Items {
					names[i] = snapshotList.Items[i].Name
				}
				g.Expect(names).ToNot(ContainElement("snapshot-oldest"))
				g.Expect(names).To(ContainElement("snapshot-old"))
				g.Expect(names).To(ContainElement("snapshot-recent"))
				g.Expect(names).To(ContainElement("snapshot-newest"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Setup fake client with scheme
			var objects []client.Object
			if tt.pod != nil {
				objects = append(objects, tt.pod)
			}
			if tt.pvc != nil {
				objects = append(objects, tt.pvc)
			}
			if tt.config != nil {
				objects = append(objects, tt.config)
			}
			for _, snapshot := range tt.existingSnapshots {
				objects = append(objects, snapshot)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(hyperapi.Scheme).
				WithObjects(objects...).
				Build()

			reconciler := &SnapshotReconciler{
				client: fakeClient,
				log:    logr.Discard(),
			}

			err := reconciler.manageRetention(context.Background(), tt.pod, tt.pvc, &tt.config.Spec)

			if tt.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				if tt.validateResult != nil {
					tt.validateResult(g, fakeClient)
				}
			}
		})
	}
}

func TestSortSnapshotsByCreationTime(t *testing.T) {
	tests := []struct {
		name      string
		snapshots []snapshotv1.VolumeSnapshot
		validate  func(*WithT, []snapshotv1.VolumeSnapshot)
	}{
		{
			name: "When snapshots are unsorted, it should sort them by creation time oldest first",
			snapshots: []snapshotv1.VolumeSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "newest",
						CreationTimestamp: metav1.NewTime(time.Now()),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "oldest",
						CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Hour)),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "middle",
						CreationTimestamp: metav1.NewTime(time.Now().Add(-1 * time.Hour)),
					},
				},
			},
			validate: func(g *WithT, snapshots []snapshotv1.VolumeSnapshot) {
				g.Expect(snapshots).To(HaveLen(3))
				g.Expect(snapshots[0].Name).To(Equal("oldest"))
				g.Expect(snapshots[1].Name).To(Equal("middle"))
				g.Expect(snapshots[2].Name).To(Equal("newest"))
			},
		},
		{
			name: "When snapshots are already sorted, it should maintain order",
			snapshots: []snapshotv1.VolumeSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "oldest",
						CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Hour)),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "middle",
						CreationTimestamp: metav1.NewTime(time.Now().Add(-1 * time.Hour)),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "newest",
						CreationTimestamp: metav1.NewTime(time.Now()),
					},
				},
			},
			validate: func(g *WithT, snapshots []snapshotv1.VolumeSnapshot) {
				g.Expect(snapshots).To(HaveLen(3))
				g.Expect(snapshots[0].Name).To(Equal("oldest"))
				g.Expect(snapshots[1].Name).To(Equal("middle"))
				g.Expect(snapshots[2].Name).To(Equal("newest"))
			},
		},
		{
			name:      "When snapshots list is empty, it should handle gracefully",
			snapshots: []snapshotv1.VolumeSnapshot{},
			validate: func(g *WithT, snapshots []snapshotv1.VolumeSnapshot) {
				g.Expect(snapshots).To(HaveLen(0))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			sortSnapshotsByCreationTime(tt.snapshots)
			tt.validate(g, tt.snapshots)
		})
	}
}

func TestParseInt32(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedValue int32
		expectedError bool
	}{
		{
			name:          "When input is valid positive integer, it should parse correctly",
			input:         "42",
			expectedValue: 42,
			expectedError: false,
		},
		{
			name:          "When input is zero, it should parse correctly",
			input:         "0",
			expectedValue: 0,
			expectedError: false,
		},
		{
			name:          "When input is maximum int32, it should parse correctly",
			input:         "2147483647",
			expectedValue: 2147483647,
			expectedError: false,
		},
		{
			name:          "When input is invalid string, it should return error",
			input:         "invalid",
			expectedValue: 0,
			expectedError: true,
		},
		{
			name:          "When input is empty string, it should return error",
			input:         "",
			expectedValue: 0,
			expectedError: true,
		},
		{
			name:          "When input exceeds int32 range, it should return error",
			input:         "2147483648",
			expectedValue: 0,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result, err := parseInt32(tt.input)
			if tt.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(result).To(Equal(tt.expectedValue))
			}
		})
	}
}

func TestIsKubeAPIServerPod(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		expected bool
	}{
		{
			name: "When pod has kube-apiserver label, it should return true",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						kubeAPIServerLabel: kubeAPIServerLabelValue,
					},
				},
			},
			expected: true,
		},
		{
			name: "When pod has different label value, it should return false",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						kubeAPIServerLabel: "other-value",
					},
				},
			},
			expected: false,
		},
		{
			name: "When pod has no labels, it should return false",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{},
			},
			expected: false,
		},
		{
			name: "When pod has nil labels, it should return false",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: nil,
				},
			},
			expected: false,
		},
		{
			name: "When pod has other labels but not kube-apiserver, it should return false",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "other-app",
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := isKubeAPIServerPod(tt.pod)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestSnapshotReconciler_createSnapshot(t *testing.T) {
	tests := []struct {
		name           string
		pod            *corev1.Pod
		pvc            *corev1.PersistentVolumeClaim
		config         *auditlogpersistencev1alpha1.AuditLogPersistenceConfig
		expectedError  bool
		validateResult func(*WithT, client.Client, *snapshotv1.VolumeSnapshot)
	}{
		{
			name: "When creating snapshot without VolumeSnapshotClassName, it should create snapshot without class",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kas-audit-logs-kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						Enabled: true,
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, snapshot *snapshotv1.VolumeSnapshot) {
				g.Expect(snapshot).ToNot(BeNil())
				g.Expect(snapshot.Name).To(ContainSubstring("kas-audit-logs-kube-apiserver-abc123-snapshot-"))
				g.Expect(snapshot.Namespace).To(Equal("hcp-namespace"))
				g.Expect(snapshot.Labels[auditLogsPVCLabelKey]).To(Equal("kas-audit-logs-kube-apiserver-abc123"))
				g.Expect(snapshot.Labels[auditLogsPodLabelKey]).To(Equal("kube-apiserver-abc123"))
				g.Expect(snapshot.Labels[controlPlaneNamespaceLabelKey]).To(Equal("hcp-namespace"))
				g.Expect(snapshot.Spec.Source.PersistentVolumeClaimName).ToNot(BeNil())
				g.Expect(*snapshot.Spec.Source.PersistentVolumeClaimName).To(Equal("kas-audit-logs-kube-apiserver-abc123"))
				g.Expect(snapshot.Spec.VolumeSnapshotClassName).To(BeNil())
			},
		},
		{
			name: "When creating snapshot with VolumeSnapshotClassName, it should set the class",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kas-audit-logs-kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Snapshots: auditlogpersistencev1alpha1.SnapshotConfig{
						Enabled:                 true,
						VolumeSnapshotClassName: "custom-class",
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, snapshot *snapshotv1.VolumeSnapshot) {
				g.Expect(snapshot).ToNot(BeNil())
				g.Expect(snapshot.Spec.VolumeSnapshotClassName).ToNot(BeNil())
				g.Expect(*snapshot.Spec.VolumeSnapshotClassName).To(Equal("custom-class"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Setup fake client with scheme
			fakeClient := fake.NewClientBuilder().
				WithScheme(hyperapi.Scheme).
				WithObjects(tt.pod, tt.pvc, tt.config).
				Build()

			reconciler := &SnapshotReconciler{
				client: fakeClient,
				log:    logr.Discard(),
			}

			err := reconciler.createSnapshot(context.Background(), tt.pod, tt.pvc, &tt.config.Spec)

			if tt.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				if tt.validateResult != nil {
					// Find the created snapshot
					snapshotList := &snapshotv1.VolumeSnapshotList{}
					err := fakeClient.List(context.Background(), snapshotList, client.InNamespace(tt.pod.Namespace))
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(snapshotList.Items).To(HaveLen(1))
					tt.validateResult(g, fakeClient, &snapshotList.Items[0])
				}
			}
		})
	}
}
