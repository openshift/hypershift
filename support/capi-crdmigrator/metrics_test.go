package capicrdmigrator

import (
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestMigrationMetricsCollector_Collect(t *testing.T) {
	const namespace = "test-ns"

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add corev1 to scheme: %v", err)
	}

	validStatus := CAPIMigrationStatus{
		TotalCRDs:    12,
		MigratedCRDs: 8,
	}
	validStatusBytes, _ := json.Marshal(validStatus)

	tests := map[string]struct {
		objects         []runtime.Object
		expectedSuccess float64
		expectCRDs      bool
	}{
		"When the status ConfigMap does not exist, it should emit success=0": {
			objects:         nil,
			expectedSuccess: 0,
			expectCRDs:      false,
		},
		"When the status ConfigMap exists but has no data key, it should emit success=0": {
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      StatusConfigMapName,
						Namespace: namespace,
					},
					Data: map[string]string{},
				},
			},
			expectedSuccess: 0,
			expectCRDs:      false,
		},
		"When the status ConfigMap has invalid JSON, it should emit success=0": {
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      StatusConfigMapName,
						Namespace: namespace,
					},
					Data: map[string]string{
						statusDataKey: "not-valid-json",
					},
				},
			},
			expectedSuccess: 0,
			expectCRDs:      false,
		},
		"When the status ConfigMap has valid data, it should emit success=1 and CRD metrics": {
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      StatusConfigMapName,
						Namespace: namespace,
					},
					Data: map[string]string{
						statusDataKey: string(validStatusBytes),
					},
				},
			},
			expectedSuccess: 1,
			expectCRDs:      true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(tc.objects...).
				Build()

			collector := &migrationMetricsCollector{
				client:    client,
				namespace: namespace,
			}

			ch := make(chan prometheus.Metric, 10)
			collector.Collect(ch)
			close(ch)

			var collected []prometheus.Metric
			for m := range ch {
				collected = append(collected, m)
			}

			if tc.expectCRDs {
				// success + total_crds + migrated_crds
				if len(collected) != 3 {
					t.Fatalf("expected 3 metrics, got %d", len(collected))
				}
			} else {
				// only success
				if len(collected) != 1 {
					t.Fatalf("expected 1 metric, got %d", len(collected))
				}
			}

			// The success metric is always the last one emitted
			successMetric := collected[len(collected)-1]
			d := &dto.Metric{}
			if err := successMetric.Write(d); err != nil {
				t.Fatalf("failed to write metric: %v", err)
			}
			if got := d.GetGauge().GetValue(); got != tc.expectedSuccess {
				t.Errorf("expected collector_success=%v, got %v", tc.expectedSuccess, got)
			}
		})
	}
}
