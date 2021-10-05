//go:build e2e
// +build e2e

package podtimingcontroller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

func SetupWithManager(mgr ctrl.Manager, log logr.Logger, artifactDir string) error {
	r := &podTimingReconciler{
		client: mgr.GetClient(),
		podBecomesReadyDurationSeconds: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "e2e_pod_becomes_ready_duration_seconds",
		}, []string{"namespace", "name", "uid"}),
		alreadyRecorded: sets.String{},
	}
	if err := prometheus.Register(r.podBecomesReadyDurationSeconds); err != nil {
		return fmt.Errorf("failed to register e2e_pod_becomes_ready_duration_seconds metric: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 10}).
		Complete(r)
}

type podTimingReconciler struct {
	client                         client.Client
	podBecomesReadyDurationSeconds *prometheus.CounterVec
	alreadyRecorded                sets.String
	alreadyRecordedLock            sync.RWMutex
}

func (r *podTimingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, r.reconcile(ctx, req)
}

func (r *podTimingReconciler) reconcile(ctx context.Context, req ctrl.Request) error {
	if !strings.HasPrefix(req.Namespace, "e2e-clusters-") {
		return nil
	}
	var pod corev1.Pod
	if err := r.client.Get(ctx, req.NamespacedName, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	if !isPodReady(&pod) {
		return nil
	}

	podIdentifier := pod.Namespace + pod.Name + string(pod.UID)
	alreadyRecorded := func() bool {
		r.alreadyRecordedLock.RLock()
		defer r.alreadyRecordedLock.RUnlock()

		return r.alreadyRecorded.Has(podIdentifier)
	}()
	if alreadyRecorded {
		return nil
	}

	becomesReadyDuration := time.Since(pod.CreationTimestamp.Time).Seconds()
	m, err := r.podBecomesReadyDurationSeconds.GetMetricWithLabelValues(pod.Namespace, pod.Name, string(pod.UID))
	if err != nil {
		return fmt.Errorf("failed to get metric: %w", err)
	}
	m.Add(becomesReadyDuration)

	// This looks like a possible race (We set the key after other worker did the alreadyRecorded check) but isn't, because the
	// workqueue de-duplicates.
	r.alreadyRecordedLock.Lock()
	defer r.alreadyRecordedLock.Unlock()
	r.alreadyRecorded.Insert(podIdentifier)

	return nil
}

func isPodReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}

	return false
}
