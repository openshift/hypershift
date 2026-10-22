//go:build e2ev2

package util

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

type podTimingRecorder struct {
	durations *prometheus.CounterVec
	recorded  sets.Set[string]
}

func (recorder *podTimingRecorder) observe(pod *corev1.Pod, now time.Time) {
	if pod.CreationTimestamp.IsZero() || now.Before(pod.CreationTimestamp.Time) {
		return
	}
	ready := false
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			ready = condition.Status == corev1.ConditionTrue
			break
		}
	}
	identity := pod.Namespace + "/" + pod.Name + "/" + string(pod.UID)
	if !ready || recorder.recorded.Has(identity) {
		return
	}
	recorder.durations.WithLabelValues(pod.Namespace, pod.Name, string(pod.UID)).Add(now.Sub(pod.CreationTimestamp.Time).Seconds())
	recorder.recorded.Insert(identity)
}

// StartPodTimingObserver records the first observed Ready duration for pods in
// the selected control plane namespaces. Its idempotent stop function joins the
// observer and writes Prometheus text metrics, returning artifact errors. List
// failures are diagnostic-only and logged without changing test outcomes.
func StartPodTimingObserver(ctx context.Context, client crclient.Client, namespaces []string, outputPath string) (func() error, error) {
	if client == nil || len(namespaces) == 0 || outputPath == "" {
		return nil, fmt.Errorf("pod timing observation requires a client, namespaces and artifact path")
	}
	for _, namespace := range namespaces {
		if namespace == "" {
			return nil, fmt.Errorf("pod timing namespace must not be empty")
		}
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return nil, fmt.Errorf("creating pod timing artifact directory: %w", err)
	}
	recorder := &podTimingRecorder{
		durations: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "e2e_pod_becomes_ready_duration_seconds"}, []string{"namespace", "name", "uid"}),
		recorded:  sets.New[string](),
	}
	registry := prometheus.NewRegistry()
	if err := registry.Register(recorder.durations); err != nil {
		return nil, err
	}
	observerContext, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			for _, namespace := range namespaces {
				pods := &corev1.PodList{}
				if err := client.List(observerContext, pods, crclient.InNamespace(namespace)); err != nil {
					if observerContext.Err() == nil {
						log.Printf("Pod timing observation in %s failed: %v", namespace, err)
					}
					continue
				}
				for index := range pods.Items {
					recorder.observe(&pods.Items[index], time.Now())
				}
			}
			select {
			case <-observerContext.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	var stopOnce sync.Once
	var stopError error
	return func() error {
		stopOnce.Do(func() {
			cancel()
			<-done
			stopError = writeTestMetrics(prometheus.Gatherers{prometheus.DefaultGatherer, registry}, outputPath)
		})
		return stopError
	}, nil
}

func writeTestMetrics(gatherer prometheus.Gatherer, outputPath string) error {
	families, err := gatherer.Gather()
	if err != nil {
		return fmt.Errorf("gathering test metrics: %w", err)
	}
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	for _, family := range families {
		if _, err := expfmt.MetricFamilyToText(file, family); err != nil {
			return errors.Join(err, file.Close())
		}
	}
	return file.Close()
}
