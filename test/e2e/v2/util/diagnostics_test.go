//go:build e2ev2

package util

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

func TestPodTimingRecorderObserve(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name       string
		conditions []corev1.PodCondition
		created    time.Time
		count      int
	}{
		{name: "When a pod becomes Ready, it should record one duration", conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}, created: now.Add(-time.Minute), count: 1},
		{name: "When a pod is not Ready, it should not record a duration", conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}, created: now.Add(-time.Minute)},
		{name: "When readiness is absent, it should not record a duration", created: now.Add(-time.Minute)},
		{name: "When creation time is missing, it should not record an invalid duration", conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
		{name: "When creation time is in the future, it should not record a negative counter", conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}, created: now.Add(2 * time.Minute)},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := &podTimingRecorder{
				durations: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_pod_duration_seconds"}, []string{"namespace", "name", "uid"}),
				recorded:  sets.New[string](),
			}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-control-plane", Name: "kube-apiserver", UID: types.UID("original"), CreationTimestamp: metav1.NewTime(test.created)}, Status: corev1.PodStatus{Conditions: test.conditions}}
			recorder.observe(pod, now)
			recorder.observe(pod, now.Add(time.Minute))
			if len(recorder.recorded) != test.count {
				t.Fatalf("expected %d recorded pods, got %d", test.count, len(recorder.recorded))
			}
			if test.count > 0 {
				if value := testutil.ToFloat64(recorder.durations.WithLabelValues(pod.Namespace, pod.Name, string(pod.UID))); value != 60 {
					t.Fatalf("expected the first duration to remain 60 seconds, got %f", value)
				}
				pod.UID = "replacement"
				recorder.observe(pod, now)
				if len(recorder.recorded) != 2 {
					t.Fatal("replacement pod identity was not observed independently")
				}
			}
		})
	}
}

func TestStartPodTimingObserver(t *testing.T) {
	t.Run("When observation is stopped, it should write test metrics and allow repeated stops", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "metrics", "e2e-metrics-raw.prometheus")
		stop, err := StartPodTimingObserver(t.Context(), fake.NewClientBuilder().Build(), []string{"clusters-control-plane"}, path)
		if err != nil {
			t.Fatal(err)
		}
		if err := stop(); err != nil {
			t.Fatal(err)
		}
		if err := stop(); err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(content), "go_goroutines") {
			t.Fatalf("expected the process metric artifact, error: %v", err)
		}
	})
	t.Run("When required observer inputs are absent, it should return an error", func(t *testing.T) {
		for _, namespaces := range [][]string{nil, {""}} {
			if _, err := StartPodTimingObserver(t.Context(), fake.NewClientBuilder().Build(), namespaces, filepath.Join(t.TempDir(), "metrics")); err == nil {
				t.Fatal("expected namespace validation failure")
			}
		}
		if _, err := StartPodTimingObserver(t.Context(), nil, []string{"clusters"}, filepath.Join(t.TempDir(), "metrics")); err == nil {
			t.Fatal("expected client validation failure")
		}
	})
	t.Run("When the artifact directory cannot be created, it should not start an observer", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(path, []byte("not a directory"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := StartPodTimingObserver(t.Context(), fake.NewClientBuilder().Build(), []string{"clusters"}, filepath.Join(path, "metrics")); err == nil {
			t.Fatal("expected artifact error")
		}
	})
}

type diagnosticGatherer struct {
	err error
}

func (gatherer diagnosticGatherer) Gather() ([]*dto.MetricFamily, error) {
	return nil, gatherer.err
}

func TestWriteTestMetrics(t *testing.T) {
	t.Run("When gathering fails, it should preserve the error without writing an artifact", func(t *testing.T) {
		expected := errors.New("collection failed")
		if err := writeTestMetrics(diagnosticGatherer{err: expected}, filepath.Join(t.TempDir(), "metrics")); !errors.Is(err, expected) {
			t.Fatalf("expected gathering error, got %v", err)
		}
	})
	t.Run("When the output cannot be created, it should return the error", func(t *testing.T) {
		if err := writeTestMetrics(diagnosticGatherer{}, t.TempDir()); err == nil {
			t.Fatal("expected output error")
		}
	})
}
