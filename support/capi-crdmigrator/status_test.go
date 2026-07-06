package capicrdmigrator

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	return s
}

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}

func TestStatusReporter_Reconcile(t *testing.T) {
	const namespace = "hypershift"

	tests := []struct {
		name             string
		configByCRDName  map[string]ByObjectConfig
		migratedCRDs     []string
		existing         []client.Object
		reconcileErr     error
		expectedTotal    int
		expectedMigrated int
		expectedComplete metav1.ConditionStatus
		expectedProgress metav1.ConditionStatus
		expectedDegraded metav1.ConditionStatus
	}{
		{
			name: "When all CRDs are migrated, it should report complete",
			configByCRDName: map[string]ByObjectConfig{
				"clusters.cluster.x-k8s.io": {},
				"machines.cluster.x-k8s.io": {},
			},
			migratedCRDs:     []string{"clusters.cluster.x-k8s.io", "machines.cluster.x-k8s.io"},
			expectedTotal:    2,
			expectedMigrated: 2,
			expectedComplete: metav1.ConditionTrue,
			expectedProgress: metav1.ConditionFalse,
			expectedDegraded: metav1.ConditionFalse,
		},
		{
			name: "When no CRDs are migrated, it should report progressing",
			configByCRDName: map[string]ByObjectConfig{
				"clusters.cluster.x-k8s.io": {},
				"machines.cluster.x-k8s.io": {},
			},
			migratedCRDs:     nil,
			expectedTotal:    2,
			expectedMigrated: 0,
			expectedComplete: metav1.ConditionFalse,
			expectedProgress: metav1.ConditionTrue,
			expectedDegraded: metav1.ConditionFalse,
		},
		{
			name: "When some CRDs are migrated, it should report partial progress",
			configByCRDName: map[string]ByObjectConfig{
				"clusters.cluster.x-k8s.io":    {},
				"machines.cluster.x-k8s.io":    {},
				"machinesets.cluster.x-k8s.io": {},
			},
			migratedCRDs:     []string{"clusters.cluster.x-k8s.io", "machinesets.cluster.x-k8s.io"},
			expectedTotal:    3,
			expectedMigrated: 2,
			expectedComplete: metav1.ConditionFalse,
			expectedProgress: metav1.ConditionTrue,
			expectedDegraded: metav1.ConditionFalse,
		},
		{
			name: "When ConfigMap already exists, it should update it",
			configByCRDName: map[string]ByObjectConfig{
				"clusters.cluster.x-k8s.io": {},
			},
			migratedCRDs: []string{"clusters.cluster.x-k8s.io"},
			existing: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      StatusConfigMapName,
						Namespace: namespace,
					},
					Data: map[string]string{
						statusDataKey: `{"totalCRDs":1,"migratedCRDs":0}`,
					},
				},
			},
			expectedTotal:    1,
			expectedMigrated: 1,
			expectedComplete: metav1.ConditionTrue,
			expectedProgress: metav1.ConditionFalse,
			expectedDegraded: metav1.ConditionFalse,
		},
		{
			name: "When reconcile error is present, it should set degraded condition",
			configByCRDName: map[string]ByObjectConfig{
				"clusters.cluster.x-k8s.io": {},
			},
			migratedCRDs:     nil,
			reconcileErr:     fmt.Errorf("failed to migrate storage version"),
			expectedTotal:    1,
			expectedMigrated: 0,
			expectedComplete: metav1.ConditionFalse,
			expectedProgress: metav1.ConditionTrue,
			expectedDegraded: metav1.ConditionTrue,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(fakeScheme()).
				WithObjects(tc.existing...).
				Build()

			reporter := NewStatusReporter(c, namespace, tc.configByCRDName)
			for _, name := range tc.migratedCRDs {
				reporter.SetCRDMigrated(name)
			}
			err := reporter.Reconcile(context.Background(), tc.reconcileErr)
			require.NoError(t, err)

			cm := &corev1.ConfigMap{}
			err = c.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: StatusConfigMapName}, cm)
			require.NoError(t, err)

			var status CAPIMigrationStatus
			err = json.Unmarshal([]byte(cm.Data[statusDataKey]), &status)
			require.NoError(t, err)

			assert.Equal(t, tc.expectedTotal, status.TotalCRDs)
			assert.Equal(t, tc.expectedMigrated, status.MigratedCRDs)

			complete := findCondition(status.Conditions, MigrationCompleteCondition)
			require.NotNil(t, complete, "MigrationComplete condition missing")
			assert.Equal(t, tc.expectedComplete, complete.Status)

			progressing := findCondition(status.Conditions, ProgressingCondition)
			require.NotNil(t, progressing, "Progressing condition missing")
			assert.Equal(t, tc.expectedProgress, progressing.Status)

			degraded := findCondition(status.Conditions, DegradedCondition)
			require.NotNil(t, degraded, "Degraded condition missing")
			assert.Equal(t, tc.expectedDegraded, degraded.Status)

			if tc.reconcileErr != nil {
				assert.Contains(t, degraded.Message, tc.reconcileErr.Error())
			}
		})
	}
}
