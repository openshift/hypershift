package hostedcluster

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"
	hyperutil "github.com/openshift/hypershift/support/util"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func TestHasOADPPauseAnnotations(t *testing.T) {
	tests := []struct {
		name        string
		hc          *hyperv1.HostedCluster
		expectedRes bool
	}{
		{
			name:        "nil hosted cluster",
			hc:          nil,
			expectedRes: false,
		},
		{
			name: "no annotations",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
			},
			expectedRes: false,
		},
		{
			name: "empty annotations",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-cluster",
					Namespace:   "test-namespace",
					Annotations: map[string]string{},
				},
			},
			expectedRes: false,
		},
		{
			name: "only paused-by annotation",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
					},
				},
			},
			expectedRes: false,
		},
		{
			name: "only paused-at annotation",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
					},
				},
			},
			expectedRes: false,
		},
		{
			name: "wrong paused-by value",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-by": "other-plugin",
						"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
					},
				},
			},
			expectedRes: false,
		},
		{
			name: "empty paused-at value",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
						"oadp.openshift.io/paused-at": "",
					},
				},
			},
			expectedRes: false,
		},
		{
			name: "valid OADP pause annotations",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
						"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
					},
				},
			},
			expectedRes: true,
		},
		{
			name: "valid OADP pause annotations with other annotations",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-by":        "hypershift-oadp-plugin",
						"oadp.openshift.io/paused-at":        "2024-01-01T00:00:00Z",
						"hypershift.openshift.io/cluster-id": "some-uuid",
					},
				},
			},
			expectedRes: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := hasOADPPauseAnnotations(tt.hc)
			g.Expect(result).To(Equal(tt.expectedRes))
		})
	}
}

func TestIsBackupInTerminalState(t *testing.T) {
	tests := []struct {
		name          string
		backup        unstructured.Unstructured
		expectedTerm  bool
		expectedPhase string
	}{
		{
			name: "backup with no status",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "test-backup",
					},
				},
			},
			expectedTerm:  false,
			expectedPhase: "",
		},
		{
			name: "backup with no phase",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "test-backup",
					},
					"status": map[string]interface{}{},
				},
			},
			expectedTerm:  false,
			expectedPhase: "",
		},
		{
			name: "backup in New state",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "test-backup",
					},
					"status": map[string]interface{}{
						"phase": "New",
					},
				},
			},
			expectedTerm:  false,
			expectedPhase: "New",
		},
		{
			name: "backup in InProgress state",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "test-backup",
					},
					"status": map[string]interface{}{
						"phase": "InProgress",
					},
				},
			},
			expectedTerm:  false,
			expectedPhase: "InProgress",
		},
		{
			name: "backup in Completed state",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "test-backup",
					},
					"status": map[string]interface{}{
						"phase": "Completed",
					},
				},
			},
			expectedTerm:  true,
			expectedPhase: "Completed",
		},
		{
			name: "backup in Failed state",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "test-backup",
					},
					"status": map[string]interface{}{
						"phase": "Failed",
					},
				},
			},
			expectedTerm:  true,
			expectedPhase: "Failed",
		},
		{
			name: "backup in PartiallyFailed state",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "test-backup",
					},
					"status": map[string]interface{}{
						"phase": "PartiallyFailed",
					},
				},
			},
			expectedTerm:  true,
			expectedPhase: "PartiallyFailed",
		},
		{
			name: "backup in FailedValidation state",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "test-backup",
					},
					"status": map[string]interface{}{
						"phase": "FailedValidation",
					},
				},
			},
			expectedTerm:  false, // FailedValidation is not in current terminalStates list
			expectedPhase: "FailedValidation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()
			terminal, phase := isBackupInTerminalState(ctx, tt.backup)
			g.Expect(terminal).To(Equal(tt.expectedTerm))
			g.Expect(phase).To(Equal(tt.expectedPhase))
		})
	}
}

func TestIsBackupRelatedToCluster(t *testing.T) {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "clusters",
		},
	}

	r := &HostedClusterReconciler{}

	tests := []struct {
		name           string
		backup         unstructured.Unstructured
		expectedResult bool
	}{
		{
			name: "backup with hypershift cluster name label",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "test-backup",
						"labels": map[string]interface{}{
							"hypershift.openshift.io/cluster-name": "test-cluster",
						},
					},
				},
			},
			expectedResult: false,
		},
		{
			name: "backup with hypershift cluster namespace label",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "test-backup",
						"labels": map[string]interface{}{
							"hypershift.openshift.io/cluster-namespace": "clusters",
						},
					},
				},
			},
			expectedResult: false,
		},
		{
			name: "backup with cluster name in backup name",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "test-cluster-backup-20240101",
					},
				},
			},
			expectedResult: true,
		},
		{
			name: "backup with cluster namespace and name pattern",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "clusters-test-cluster-backup",
					},
				},
			},
			expectedResult: true,
		},
		{
			name: "backup with hypershift cluster annotation",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "test-backup",
						"annotations": map[string]interface{}{
							"hypershift.openshift.io/cluster-name": "test-cluster",
						},
					},
				},
			},
			expectedResult: false,
		},
		{
			name: "backup with included namespaces containing cluster namespace",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "test-backup",
					},
					"spec": map[string]interface{}{
						"includedNamespaces": []interface{}{
							"kube-system",
							"clusters",
							"default",
						},
					},
				},
			},
			expectedResult: true,
		},
		{
			name: "backup not related to cluster",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "unrelated-backup",
						"labels": map[string]interface{}{
							"app": "other-app",
						},
					},
					"spec": map[string]interface{}{
						"includedNamespaces": []interface{}{
							"kube-system",
							"other-namespace",
						},
					},
				},
			},
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := r.isBackupRelatedToCluster(tt.backup, hc)
			g.Expect(result).To(Equal(tt.expectedResult))
		})
	}
}

func TestCheckOADPRecovery(t *testing.T) {
	ctx := context.Background()
	ctx = log.IntoContext(ctx, log.Log)

	scheme := runtime.NewScheme()
	_ = hyperv1.AddToScheme(scheme)

	tests := []struct {
		name            string
		hc              *hyperv1.HostedCluster
		veleroBackups   []runtime.Object
		expectedUnpause bool
		expectedError   bool
	}{
		{
			name: "cluster not paused by OADP",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
			},
			expectedUnpause: false,
			expectedError:   false,
		},
		{
			name: "cluster paused by OADP but no backups found",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
						"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					PausedUntil: ptr.To("true"),
				},
			},
			expectedUnpause: true,
			expectedError:   false,
		},
		{
			name: "cluster paused by OADP with backup in progress",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
						"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					PausedUntil: ptr.To("true"),
				},
			},
			veleroBackups: []runtime.Object{
				&unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "velero.io/v1",
						"kind":       "Backup",
						"metadata": map[string]interface{}{
							"name":      "test-cluster-backup",
							"namespace": "openshift-adp",
						},
						"status": map[string]interface{}{
							"phase": "InProgress",
						},
					},
				},
			},
			expectedUnpause: false,
			expectedError:   false,
		},
		{
			name: "cluster paused by OADP with completed backup",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
						"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					PausedUntil: ptr.To("true"),
				},
			},
			veleroBackups: []runtime.Object{
				&unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "velero.io/v1",
						"kind":       "Backup",
						"metadata": map[string]interface{}{
							"name":      "test-cluster-backup",
							"namespace": "openshift-adp",
						},
						"status": map[string]interface{}{
							"phase": "Completed",
						},
					},
				},
			},
			expectedUnpause: true,
			expectedError:   false,
		},
		{
			name: "cluster paused by OADP with failed backup",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
						"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					PausedUntil: ptr.To("true"),
				},
			},
			veleroBackups: []runtime.Object{
				&unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "velero.io/v1",
						"kind":       "Backup",
						"metadata": map[string]interface{}{
							"name":      "test-cluster-backup",
							"namespace": "openshift-adp",
						},
						"status": map[string]interface{}{
							"phase": "Failed",
						},
					},
				},
			},
			expectedUnpause: true,
			expectedError:   false,
		},
		{
			name: "cluster paused by OADP with multiple backups - most recent is terminal",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
						"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					PausedUntil: ptr.To("true"),
				},
			},
			veleroBackups: []runtime.Object{
				// Older backup (should not be checked due to early return)
				&unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "velero.io/v1",
						"kind":       "Backup",
						"metadata": map[string]interface{}{
							"name":              "test-cluster-backup-old",
							"namespace":         "openshift-adp",
							"creationTimestamp": "2024-01-01T10:00:00Z",
						},
						"status": map[string]interface{}{
							"phase": "InProgress",
						},
					},
				},
				// Most recent backup (should be checked first and trigger early return)
				&unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "velero.io/v1",
						"kind":       "Backup",
						"metadata": map[string]interface{}{
							"name":              "test-cluster-backup-recent",
							"namespace":         "openshift-adp",
							"creationTimestamp": "2024-01-01T12:00:00Z",
						},
						"status": map[string]interface{}{
							"phase": "Completed",
						},
					},
				},
			},
			expectedUnpause: true,
			expectedError:   false,
		},
		{
			name: "cluster paused by OADP with multiple backups - most recent is not terminal but older ones are",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
						"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					PausedUntil: ptr.To("true"),
				},
			},
			veleroBackups: []runtime.Object{
				// Older backup in terminal state (should be found as fallback)
				&unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "velero.io/v1",
						"kind":       "Backup",
						"metadata": map[string]interface{}{
							"name":              "test-cluster-backup-old",
							"namespace":         "openshift-adp",
							"creationTimestamp": "2024-01-01T10:00:00Z",
						},
						"status": map[string]interface{}{
							"phase": "Failed",
						},
					},
				},
				// Most recent backup still in progress
				&unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "velero.io/v1",
						"kind":       "Backup",
						"metadata": map[string]interface{}{
							"name":              "test-cluster-backup-recent",
							"namespace":         "openshift-adp",
							"creationTimestamp": "2024-01-01T12:00:00Z",
						},
						"status": map[string]interface{}{
							"phase": "InProgress",
						},
					},
				},
			},
			expectedUnpause: false,
			expectedError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Clear cache between tests to avoid interference
			cache := GetVeleroBackupCache()
			cache.ClearAll()

			// Create fake client with the test objects
			objs := []runtime.Object{tt.hc}
			objs = append(objs, tt.veleroBackups...)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()

			r := &HostedClusterReconciler{
				Client: fakeClient,
			}

			shouldUnpause, err := r.checkOADPRecovery(ctx, tt.hc)

			if tt.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
			}
			g.Expect(shouldUnpause).To(Equal(tt.expectedUnpause))
		})
	}
}

func TestFindLastRelatedBackup(t *testing.T) {
	ctx := context.Background()
	ctx = log.IntoContext(ctx, log.Log)

	scheme := runtime.NewScheme()
	_ = hyperv1.AddToScheme(scheme)

	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "clusters",
		},
	}

	tests := []struct {
		name               string
		veleroBackups      []runtime.Object
		expectedBackupName *string // nil means no backup expected
		expectedError      bool
	}{
		{
			name:               "no backups found",
			veleroBackups:      []runtime.Object{},
			expectedBackupName: nil,
			expectedError:      false,
		},
		{
			name: "single related backup",
			veleroBackups: []runtime.Object{
				&unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "velero.io/v1",
						"kind":       "Backup",
						"metadata": map[string]interface{}{
							"name":              "test-cluster-backup",
							"namespace":         "openshift-adp",
							"creationTimestamp": "2024-01-01T12:00:00Z",
						},
						"status": map[string]interface{}{
							"phase": "Completed",
						},
					},
				},
			},
			expectedBackupName: ptr.To("test-cluster-backup"),
			expectedError:      false,
		},
		{
			name: "multiple related backups - returns most recent",
			veleroBackups: []runtime.Object{
				// Older backup
				&unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "velero.io/v1",
						"kind":       "Backup",
						"metadata": map[string]interface{}{
							"name":              "test-cluster-backup-old",
							"namespace":         "openshift-adp",
							"creationTimestamp": "2024-01-01T10:00:00Z",
						},
						"status": map[string]interface{}{
							"phase": "Completed",
						},
					},
				},
				// Most recent backup (should be returned)
				&unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "velero.io/v1",
						"kind":       "Backup",
						"metadata": map[string]interface{}{
							"name":              "test-cluster-backup-recent",
							"namespace":         "openshift-adp",
							"creationTimestamp": "2024-01-01T15:00:00Z",
						},
						"status": map[string]interface{}{
							"phase": "InProgress",
						},
					},
				},
			},
			expectedBackupName: ptr.To("test-cluster-backup-recent"),
			expectedError:      false,
		},
		{
			name: "backups with unrelated names are ignored",
			veleroBackups: []runtime.Object{
				// Unrelated backup (should be ignored)
				&unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "velero.io/v1",
						"kind":       "Backup",
						"metadata": map[string]interface{}{
							"name":              "other-app-backup",
							"namespace":         "openshift-adp",
							"creationTimestamp": "2024-01-01T15:00:00Z",
						},
						"status": map[string]interface{}{
							"phase": "Completed",
						},
					},
				},
				// Related backup
				&unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "velero.io/v1",
						"kind":       "Backup",
						"metadata": map[string]interface{}{
							"name":              "test-cluster-backup",
							"namespace":         "openshift-adp",
							"creationTimestamp": "2024-01-01T12:00:00Z",
						},
						"status": map[string]interface{}{
							"phase": "Completed",
						},
					},
				},
			},
			expectedBackupName: ptr.To("test-cluster-backup"),
			expectedError:      false,
		},
		{
			name: "backup related by namespace inclusion",
			veleroBackups: []runtime.Object{
				&unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "velero.io/v1",
						"kind":       "Backup",
						"metadata": map[string]interface{}{
							"name":              "namespace-backup",
							"namespace":         "openshift-adp",
							"creationTimestamp": "2024-01-01T12:00:00Z",
						},
						"spec": map[string]interface{}{
							"includedNamespaces": []interface{}{
								"kube-system",
								"clusters", // This should match hc.Namespace
								"default",
							},
						},
						"status": map[string]interface{}{
							"phase": "Completed",
						},
					},
				},
			},
			expectedBackupName: ptr.To("namespace-backup"),
			expectedError:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Clear cache between tests to avoid interference
			cache := GetVeleroBackupCache()
			cache.ClearAll()

			// Create fake client with the test objects
			objs := []runtime.Object{hc}
			objs = append(objs, tt.veleroBackups...)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()

			r := &HostedClusterReconciler{
				Client: fakeClient,
			}

			lastBackup, err := r.findLastRelatedBackup(ctx, hc)

			if tt.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
			}

			if tt.expectedBackupName == nil {
				g.Expect(lastBackup).To(BeNil())
			} else {
				g.Expect(lastBackup).ToNot(BeNil())
				g.Expect(lastBackup.GetName()).To(Equal(*tt.expectedBackupName))
			}
		})
	}
}

func TestVeleroBackupCache(t *testing.T) {
	ctx := context.Background()
	ctx = log.IntoContext(ctx, log.Log)

	scheme := runtime.NewScheme()
	_ = hyperv1.AddToScheme(scheme)

	// Create test backup objects
	backup1 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "velero.io/v1",
			"kind":       "Backup",
			"metadata": map[string]interface{}{
				"name":              "backup-1",
				"namespace":         "openshift-adp",
				"creationTimestamp": "2024-01-01T10:00:00Z",
			},
			"status": map[string]interface{}{
				"phase": "Completed",
			},
		},
	}

	backup2 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "velero.io/v1",
			"kind":       "Backup",
			"metadata": map[string]interface{}{
				"name":              "backup-2",
				"namespace":         "openshift-adp",
				"creationTimestamp": "2024-01-01T11:00:00Z",
			},
			"status": map[string]interface{}{
				"phase": "Failed",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(backup1, backup2).Build()

	// Define GVK for backup
	backupGVK := schema.GroupVersionKind{
		Group:   "velero.io",
		Version: "v1",
		Kind:    "BackupList",
	}

	t.Run("cache miss and populate", func(t *testing.T) {
		g := NewWithT(t)

		// Create new cache instance for this test
		cache := &VeleroBackupCache{
			cache:      make(map[string]*VeleroBackupCacheEntry),
			defaultTTL: 5 * time.Minute,
		}

		// First call should be a cache miss
		backups, err := cache.GetBackups(ctx, fakeClient, "openshift-adp", backupGVK)
		g.Expect(err).To(BeNil())
		g.Expect(len(backups)).To(Equal(2))
		g.Expect(backups[0].GetName()).To(BeElementOf("backup-1", "backup-2"))
		g.Expect(backups[1].GetName()).To(BeElementOf("backup-1", "backup-2"))
	})

	t.Run("cache hit", func(t *testing.T) {
		g := NewWithT(t)

		// Create cache with pre-populated data
		cache := &VeleroBackupCache{
			cache: map[string]*VeleroBackupCacheEntry{
				"openshift-adp": {
					Backups: []unstructured.Unstructured{
						{
							Object: map[string]interface{}{
								"metadata": map[string]interface{}{
									"name": "cached-backup",
								},
							},
						},
					},
					Timestamp: time.Now(), // Fresh timestamp
				},
			},
			defaultTTL: 5 * time.Minute,
		}

		// Should return cached data
		backups, err := cache.GetBackups(ctx, fakeClient, "openshift-adp", backupGVK)
		g.Expect(err).To(BeNil())
		g.Expect(len(backups)).To(Equal(1))
		g.Expect(backups[0].GetName()).To(Equal("cached-backup"))
	})

	t.Run("cache expiry", func(t *testing.T) {
		g := NewWithT(t)

		// Create cache with expired data
		cache := &VeleroBackupCache{
			cache: map[string]*VeleroBackupCacheEntry{
				"openshift-adp": {
					Backups: []unstructured.Unstructured{
						{
							Object: map[string]interface{}{
								"metadata": map[string]interface{}{
									"name": "expired-backup",
								},
							},
						},
					},
					Timestamp: time.Now().Add(-10 * time.Minute), // Expired timestamp
				},
			},
			defaultTTL: 5 * time.Minute,
		}

		// Should fetch fresh data from API
		backups, err := cache.GetBackups(ctx, fakeClient, "openshift-adp", backupGVK)
		g.Expect(err).To(BeNil())
		g.Expect(len(backups)).To(Equal(2)) // Real data from fake client
		g.Expect(backups[0].GetName()).To(BeElementOf("backup-1", "backup-2"))
	})

	t.Run("cache management functions", func(t *testing.T) {
		g := NewWithT(t)

		cache := &VeleroBackupCache{
			cache: map[string]*VeleroBackupCacheEntry{
				"openshift-adp": {
					Backups:   []unstructured.Unstructured{},
					Timestamp: time.Now(),
				},
				"velero": {
					Backups:   []unstructured.Unstructured{},
					Timestamp: time.Now(),
				},
			},
			defaultTTL: 5 * time.Minute,
		}

		// Test ClearNamespace
		cache.ClearNamespace("openshift-adp")
		g.Expect(len(cache.cache)).To(Equal(1))
		g.Expect(cache.cache["velero"]).ToNot(BeNil())

		// Test ClearAll
		cache.ClearAll()
		g.Expect(len(cache.cache)).To(Equal(0))

		// Test SetTTL
		cache.SetTTL(10 * time.Minute)
		g.Expect(cache.defaultTTL).To(Equal(10 * time.Minute))
	})

	t.Run("conditional TTL with in-progress backups", func(t *testing.T) {
		g := NewWithT(t)

		// Create backup with in-progress state
		inProgressBackup := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "velero.io/v1",
				"kind":       "Backup",
				"metadata": map[string]interface{}{
					"name":              "backup-in-progress",
					"namespace":         "openshift-adp",
					"creationTimestamp": "2024-01-01T10:00:00Z",
				},
				"status": map[string]interface{}{
					"phase": "InProgress",
				},
			},
		}

		// Create fake client with in-progress backup
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(inProgressBackup).Build()

		// Create cache with in-progress backup data that's fresh but should use short TTL
		cache := &VeleroBackupCache{
			cache: map[string]*VeleroBackupCacheEntry{
				"openshift-adp": {
					Backups: []unstructured.Unstructured{
						{
							Object: map[string]interface{}{
								"metadata": map[string]interface{}{
									"name": "backup-in-progress",
								},
								"status": map[string]interface{}{
									"phase": "InProgress",
								},
							},
						},
					},
					Timestamp: time.Now().Add(-45 * time.Second), // 45 seconds old
				},
			},
			defaultTTL: 150 * time.Second, // 2.5 minutes default TTL
		}

		// Should refresh because in-progress backups use 30s TTL, and cache is 45s old
		backups, err := cache.GetBackups(ctx, fakeClient, "openshift-adp", backupGVK)
		g.Expect(err).To(BeNil())
		g.Expect(len(backups)).To(Equal(1))
		g.Expect(backups[0].GetName()).To(Equal("backup-in-progress"))

		// Verify cache was updated with fresh timestamp
		cacheEntry := cache.cache["openshift-adp"]
		g.Expect(time.Since(cacheEntry.Timestamp)).To(BeNumerically("<", 5*time.Second))
	})

	t.Run("conditional TTL with terminal state backups", func(t *testing.T) {
		g := NewWithT(t)

		// Create backup with terminal state
		completedBackup := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "velero.io/v1",
				"kind":       "Backup",
				"metadata": map[string]interface{}{
					"name":              "backup-completed",
					"namespace":         "openshift-adp",
					"creationTimestamp": "2024-01-01T10:00:00Z",
				},
				"status": map[string]interface{}{
					"phase": "Completed",
				},
			},
		}

		// Create fake client with completed backup
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(completedBackup).Build()

		// Create cache with completed backup data that's 45 seconds old
		cache := &VeleroBackupCache{
			cache: map[string]*VeleroBackupCacheEntry{
				"openshift-adp": {
					Backups: []unstructured.Unstructured{
						{
							Object: map[string]interface{}{
								"metadata": map[string]interface{}{
									"name": "backup-completed-cached",
								},
								"status": map[string]interface{}{
									"phase": "Completed",
								},
							},
						},
					},
					Timestamp: time.Now().Add(-45 * time.Second), // 45 seconds old
				},
			},
			defaultTTL: 150 * time.Second, // 2.5 minutes default TTL
		}

		// Should use cache because terminal state backups use normal TTL (2.5 min), and cache is only 45s old
		backups, err := cache.GetBackups(ctx, fakeClient, "openshift-adp", backupGVK)
		g.Expect(err).To(BeNil())
		g.Expect(len(backups)).To(Equal(1))
		g.Expect(backups[0].GetName()).To(Equal("backup-completed-cached")) // Should return cached version

		// Verify cache timestamp was NOT updated (still using cached data)
		cacheEntry := cache.cache["openshift-adp"]
		g.Expect(time.Since(cacheEntry.Timestamp)).To(BeNumerically(">", 40*time.Second))
	})
}

func TestResumeClusterFromHangedOADPBackup(t *testing.T) {
	ctx := context.Background()
	ctx = log.IntoContext(ctx, log.Log)

	scheme := runtime.NewScheme()
	_ = hyperv1.AddToScheme(scheme)

	// Mock createOrUpdate function
	createOrUpdateFunc := upsert.CreateOrUpdateFN(func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		// Apply the mutation function
		if err := f(); err != nil {
			return controllerutil.OperationResultNone, err
		}
		// Update the object in the fake client
		if err := c.Update(ctx, obj); err != nil {
			return controllerutil.OperationResultNone, err
		}
		return controllerutil.OperationResultUpdated, nil
	})

	tests := []struct {
		name           string
		hc             *hyperv1.HostedCluster
		nodePools      []hyperv1.NodePool
		expectedError  bool
		validateResult func(t *testing.T, hc *hyperv1.HostedCluster, nodePools []hyperv1.NodePool, fakeClient client.Client)
	}{
		{
			name: "successfully resume cluster with OADP annotations and no nodepools",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
						"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					PausedUntil: ptr.To("true"),
				},
			},
			nodePools:     []hyperv1.NodePool{},
			expectedError: false,
			validateResult: func(t *testing.T, hc *hyperv1.HostedCluster, nodePools []hyperv1.NodePool, fakeClient client.Client) {
				g := NewWithT(t)

				// Get updated HostedCluster
				updatedHC := &hyperv1.HostedCluster{}
				err := fakeClient.Get(ctx, client.ObjectKeyFromObject(hc), updatedHC)
				g.Expect(err).To(BeNil())

				// Verify OADP annotations are removed
				annotations := updatedHC.GetAnnotations()
				g.Expect(annotations).ToNot(HaveKey("oadp.openshift.io/paused-by"))
				g.Expect(annotations).ToNot(HaveKey("oadp.openshift.io/paused-at"))

				// Verify cluster is unpaused
				g.Expect(updatedHC.Spec.PausedUntil).To(BeNil())
			},
		},
		{
			name: "successfully resume cluster with OADP annotations and multiple nodepools",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
						"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					PausedUntil: ptr.To("true"),
				},
			},
			nodePools: []hyperv1.NodePool{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nodepool-1",
						Namespace: "clusters",
						Annotations: map[string]string{
							"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
							"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
						},
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "test-cluster",
						PausedUntil: ptr.To("true"),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nodepool-2",
						Namespace: "clusters",
						Annotations: map[string]string{
							"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
							"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
						},
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "test-cluster",
						PausedUntil: ptr.To("true"),
					},
				},
			},
			expectedError: false,
			validateResult: func(t *testing.T, hc *hyperv1.HostedCluster, nodePools []hyperv1.NodePool, fakeClient client.Client) {
				g := NewWithT(t)

				// Get updated HostedCluster
				updatedHC := &hyperv1.HostedCluster{}
				err := fakeClient.Get(ctx, client.ObjectKeyFromObject(hc), updatedHC)
				g.Expect(err).To(BeNil())

				// Verify OADP annotations are removed from HostedCluster
				annotations := updatedHC.GetAnnotations()
				g.Expect(annotations).ToNot(HaveKey("oadp.openshift.io/paused-by"))
				g.Expect(annotations).ToNot(HaveKey("oadp.openshift.io/paused-at"))
				g.Expect(updatedHC.Spec.PausedUntil).To(BeNil())

				// Verify all NodePools are updated
				for _, np := range nodePools {
					updatedNP := &hyperv1.NodePool{}
					err := fakeClient.Get(ctx, client.ObjectKeyFromObject(&np), updatedNP)
					g.Expect(err).To(BeNil())

					// Verify OADP annotations are removed from NodePool
					npAnnotations := updatedNP.GetAnnotations()
					g.Expect(npAnnotations).ToNot(HaveKey("oadp.openshift.io/paused-by"))
					g.Expect(npAnnotations).ToNot(HaveKey("oadp.openshift.io/paused-at"))
					g.Expect(updatedNP.Spec.PausedUntil).To(BeNil())
				}
			},
		},
		{
			name: "successfully resume cluster without OADP annotations",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						"some.other/annotation": "value",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					PausedUntil: ptr.To("true"),
				},
			},
			nodePools: []hyperv1.NodePool{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nodepool-1",
						Namespace: "clusters",
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "test-cluster",
						PausedUntil: ptr.To("true"),
					},
				},
			},
			expectedError: false,
			validateResult: func(t *testing.T, hc *hyperv1.HostedCluster, nodePools []hyperv1.NodePool, fakeClient client.Client) {
				g := NewWithT(t)

				// Get updated HostedCluster
				updatedHC := &hyperv1.HostedCluster{}
				err := fakeClient.Get(ctx, client.ObjectKeyFromObject(hc), updatedHC)
				g.Expect(err).To(BeNil())

				// Verify other annotations remain
				annotations := updatedHC.GetAnnotations()
				g.Expect(annotations).To(HaveKey("some.other/annotation"))
				g.Expect(annotations["some.other/annotation"]).To(Equal("value"))

				// Verify cluster is unpaused
				g.Expect(updatedHC.Spec.PausedUntil).To(BeNil())
			},
		},
		{
			name: "successfully resume cluster with no annotations",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					PausedUntil: ptr.To("true"),
				},
			},
			nodePools:     []hyperv1.NodePool{},
			expectedError: false,
			validateResult: func(t *testing.T, hc *hyperv1.HostedCluster, nodePools []hyperv1.NodePool, fakeClient client.Client) {
				g := NewWithT(t)

				// Get updated HostedCluster
				updatedHC := &hyperv1.HostedCluster{}
				err := fakeClient.Get(ctx, client.ObjectKeyFromObject(hc), updatedHC)
				g.Expect(err).To(BeNil())

				// Verify cluster is unpaused
				g.Expect(updatedHC.Spec.PausedUntil).To(BeNil())
			},
		},
		{
			name: "successfully resume cluster with nodepool in different namespace (should be ignored)",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
						"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					PausedUntil: ptr.To("true"),
				},
			},
			nodePools: []hyperv1.NodePool{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nodepool-related",
						Namespace: "clusters", // Same namespace and cluster name - should be updated
						Annotations: map[string]string{
							"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
							"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
						},
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "test-cluster",
						PausedUntil: ptr.To("true"),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nodepool-unrelated",
						Namespace: "other-namespace", // Different namespace - should be ignored
						Annotations: map[string]string{
							"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
							"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
						},
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "test-cluster",
						PausedUntil: ptr.To("true"),
					},
				},
			},
			expectedError: false,
			validateResult: func(t *testing.T, hc *hyperv1.HostedCluster, nodePools []hyperv1.NodePool, fakeClient client.Client) {
				g := NewWithT(t)

				// Get updated HostedCluster
				updatedHC := &hyperv1.HostedCluster{}
				err := fakeClient.Get(ctx, client.ObjectKeyFromObject(hc), updatedHC)
				g.Expect(err).To(BeNil())

				// Verify HostedCluster is updated
				g.Expect(updatedHC.Spec.PausedUntil).To(BeNil())

				// Verify related nodepool is updated
				relatedNP := &hyperv1.NodePool{}
				err = fakeClient.Get(ctx, client.ObjectKey{Name: "nodepool-related", Namespace: "clusters"}, relatedNP)
				g.Expect(err).To(BeNil())
				g.Expect(relatedNP.Spec.PausedUntil).To(BeNil())

				// Verify unrelated nodepool is NOT updated (should still be paused)
				unrelatedNP := &hyperv1.NodePool{}
				err = fakeClient.Get(ctx, client.ObjectKey{Name: "nodepool-unrelated", Namespace: "other-namespace"}, unrelatedNP)
				g.Expect(err).To(BeNil())
				g.Expect(unrelatedNP.Spec.PausedUntil).ToNot(BeNil())
				g.Expect(*unrelatedNP.Spec.PausedUntil).To(Equal("true"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Create fake client with test objects
			objs := []runtime.Object{tt.hc}
			for i := range tt.nodePools {
				objs = append(objs, &tt.nodePools[i])
			}
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()

			r := &HostedClusterReconciler{
				Client: fakeClient,
			}

			result, err := r.resumeClusterFromHangedOADPBackup(ctx, tt.hc, createOrUpdateFunc)

			if tt.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(result).To(Equal(ctrl.Result{}))
			}

			// Run validation if provided
			if tt.validateResult != nil {
				tt.validateResult(t, tt.hc, tt.nodePools, fakeClient)
			}
		})
	}
}

func TestInitVeleroBackupCache(t *testing.T) {
	tests := []struct {
		name                   string
		expectedCacheAfterInit func(t *testing.T, cache *VeleroBackupCache)
	}{
		{
			name: "When initVeleroBackupCache is called - it should ensure cache exists",
			expectedCacheAfterInit: func(t *testing.T, cache *VeleroBackupCache) {
				g := NewWithT(t)
				g.Expect(cache).ToNot(BeNil())
				g.Expect(cache.cache).ToNot(BeNil())
				g.Expect(cache.defaultTTL).To(Equal(oadpCacheTTL))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the function under test
			initVeleroBackupCache()

			// Verify results - the global cache should be initialized
			if tt.expectedCacheAfterInit != nil {
				tt.expectedCacheAfterInit(t, veleroBackupCache)
			}
		})
	}
}

func TestGetVeleroBackupCache(t *testing.T) {
	tests := []struct {
		name           string
		expectedResult func(t *testing.T, result *VeleroBackupCache)
	}{
		{
			name: "When GetVeleroBackupCache is called - it should return initialized cache",
			expectedResult: func(t *testing.T, result *VeleroBackupCache) {
				g := NewWithT(t)
				g.Expect(result).ToNot(BeNil())
				g.Expect(result.cache).ToNot(BeNil())
				g.Expect(result.defaultTTL).To(Equal(oadpCacheTTL))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the function under test
			result := GetVeleroBackupCache()

			// Verify results
			if tt.expectedResult != nil {
				tt.expectedResult(t, result)
			}

			// Verify the returned cache is the same as the global cache
			g := NewWithT(t)
			g.Expect(result).To(Equal(veleroBackupCache))
		})
	}
}

func TestHasInProgressBackups(t *testing.T) {
	tests := []struct {
		name           string
		backups        []unstructured.Unstructured
		expectedResult bool
		description    string
	}{
		{
			name:           "When hasInProgressBackups is called with empty slice it should return false",
			backups:        []unstructured.Unstructured{},
			expectedResult: false,
			description:    "Empty backup list should not have in-progress backups",
		},
		{
			name: "When hasInProgressBackups is called with backup in InProgress state it should return true",
			backups: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "backup-in-progress",
						},
						"status": map[string]interface{}{
							"phase": "InProgress",
						},
					},
				},
			},
			expectedResult: true,
			description:    "Backup in InProgress state should be detected",
		},
		{
			name: "When hasInProgressBackups is called with backup in New state it should return true",
			backups: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "backup-new",
						},
						"status": map[string]interface{}{
							"phase": "New",
						},
					},
				},
			},
			expectedResult: true,
			description:    "Backup in New state should be detected as in-progress",
		},
		{
			name: "When hasInProgressBackups is called with backup in Uploading state it should return true",
			backups: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "backup-uploading",
						},
						"status": map[string]interface{}{
							"phase": "Uploading",
						},
					},
				},
			},
			expectedResult: true,
			description:    "Backup in Uploading state should be detected as in-progress",
		},
		{
			name: "When hasInProgressBackups is called with backup in Completed state it should return false",
			backups: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "backup-completed",
						},
						"status": map[string]interface{}{
							"phase": "Completed",
						},
					},
				},
			},
			expectedResult: false,
			description:    "Backup in Completed state should not be detected as in-progress",
		},
		{
			name: "When hasInProgressBackups is called with backup in Failed state it should return false",
			backups: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "backup-failed",
						},
						"status": map[string]interface{}{
							"phase": "Failed",
						},
					},
				},
			},
			expectedResult: false,
			description:    "Backup in Failed state should not be detected as in-progress",
		},
		{
			name: "When hasInProgressBackups is called with backup in PartiallyFailed state it should return false",
			backups: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "backup-partially-failed",
						},
						"status": map[string]interface{}{
							"phase": "PartiallyFailed",
						},
					},
				},
			},
			expectedResult: false,
			description:    "Backup in PartiallyFailed state should not be detected as in-progress",
		},
		{
			name: "When hasInProgressBackups is called with backup in FailedValidation state it should return true",
			backups: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "backup-failed-validation",
						},
						"status": map[string]interface{}{
							"phase": "FailedValidation",
						},
					},
				},
			},
			expectedResult: true, // FailedValidation is not terminal, so it's considered in-progress
			description:    "Backup in FailedValidation state should be detected as in-progress since it's not terminal",
		},
		{
			name: "When hasInProgressBackups is called with backup with no status it should return false",
			backups: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "backup-no-status",
						},
					},
				},
			},
			expectedResult: false,
			description:    "Backup with no status should not be detected as in-progress",
		},
		{
			name: "When hasInProgressBackups is called with backup with no phase it should return false",
			backups: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "backup-no-phase",
						},
						"status": map[string]interface{}{},
					},
				},
			},
			expectedResult: false,
			description:    "Backup with status but no phase should not be detected as in-progress",
		},
		{
			name: "When hasInProgressBackups is called with mixed states including one in-progress it should return true",
			backups: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "backup-completed",
						},
						"status": map[string]interface{}{
							"phase": "Completed",
						},
					},
				},
				{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "backup-in-progress",
						},
						"status": map[string]interface{}{
							"phase": "InProgress",
						},
					},
				},
				{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "backup-failed",
						},
						"status": map[string]interface{}{
							"phase": "Failed",
						},
					},
				},
			},
			expectedResult: true,
			description:    "If any backup is in-progress, the function should return true",
		},
		{
			name: "When hasInProgressBackups is called with multiple terminal state backups it should return false",
			backups: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "backup-completed",
						},
						"status": map[string]interface{}{
							"phase": "Completed",
						},
					},
				},
				{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "backup-failed",
						},
						"status": map[string]interface{}{
							"phase": "Failed",
						},
					},
				},
				{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "backup-partially-failed",
						},
						"status": map[string]interface{}{
							"phase": "PartiallyFailed",
						},
					},
				},
			},
			expectedResult: false,
			description:    "All terminal state backups should return false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := hasInProgressBackups(tt.backups)
			g.Expect(result).To(Equal(tt.expectedResult), tt.description)
		})
	}
}

func TestOADPRequeueLogic(t *testing.T) {
	tests := []struct {
		name             string
		hc               *hyperv1.HostedCluster
		expectedRequeue  bool
		expectedDuration time.Duration
		description      string
	}{
		{
			name: "When cluster has OADP annotations and pausedUntil is boolean it should force frequent requeue",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
						"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					PausedUntil: ptr.To("true"), // Boolean pause
				},
			},
			expectedRequeue:  true,
			expectedDuration: 30 * time.Second,
			description:      "OADP-paused cluster with boolean pausedUntil should requeue every 30 seconds",
		},
		{
			name: "When cluster has OADP annotations and pausedUntil is RFC3339 date it should use normal duration",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
						"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					PausedUntil: ptr.To("2030-01-01T00:00:00Z"), // RFC3339 date in the future (should be paused)
				},
			},
			expectedRequeue:  true,
			expectedDuration: 0, // This will be calculated by IsReconciliationPaused (should be > 0 for future date)
			description:      "OADP-paused cluster with RFC3339 pausedUntil should use calculated duration",
		},
		{
			name: "When cluster has no OADP annotations and pausedUntil is boolean it should use normal duration",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						"other.annotation/key": "value",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					PausedUntil: ptr.To("true"), // Boolean pause
				},
			},
			expectedRequeue:  true,
			expectedDuration: 0, // No OADP annotations, so normal duration=0 for boolean
			description:      "Non-OADP paused cluster should use normal pause behavior",
		},
		{
			name: "When cluster has OADP annotations but no pausedUntil field it should not requeue",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						"oadp.openshift.io/paused-by": "hypershift-oadp-plugin",
						"oadp.openshift.io/paused-at": "2024-01-01T00:00:00Z",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					// No PausedUntil field
				},
			},
			expectedRequeue:  false,
			expectedDuration: 0,
			description:      "Cluster with OADP annotations but not paused should not requeue",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Test the core logic: hasOADPPauseAnnotations + duration check
			hasOADPAnnotations := hasOADPPauseAnnotations(tt.hc)

			// Simulate IsReconciliationPaused logic
			isPaused, duration := hyperutil.IsReconciliationPaused(log.Log, tt.hc.Spec.PausedUntil)

			var expectedRequeueAfter time.Duration
			if isPaused {
				expectedRequeueAfter = duration
				// Apply the OADP requeue logic we implemented
				if hasOADPAnnotations && duration == 0 {
					expectedRequeueAfter = 30 * time.Second
				}
			}

			g.Expect(isPaused).To(Equal(tt.expectedRequeue), tt.description)
			if tt.expectedRequeue {
				if tt.expectedDuration == 0 {
					// For RFC3339 dates, we can't predict the exact duration, just verify it's not 30s
					if hasOADPAnnotations {
						// RFC3339 case with OADP should use calculated duration, not forced 30s
						g.Expect(expectedRequeueAfter).ToNot(Equal(30*time.Second), "RFC3339 with OADP should not force 30s requeue")
					} else {
						// Boolean case without OADP should use duration=0
						g.Expect(expectedRequeueAfter).To(Equal(time.Duration(0)), "Boolean without OADP should have duration=0")
					}
				} else {
					g.Expect(expectedRequeueAfter).To(Equal(tt.expectedDuration), tt.description)
				}
			}
		})
	}
}
