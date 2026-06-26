package reencryption

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	migrationStateGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hypershift_encryption_migration_state",
		Help: "Current rotation state per hosted cluster. Label 'state' maps to history[0].state or 'idle' when no rotation is in progress.",
	}, []string{"namespace", "name", "state"})

	migrationDurationHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hypershift_encryption_migration_duration_seconds",
		Help:    "Duration of completed rotations from startedTime to completionTime.",
		Buckets: prometheus.ExponentialBuckets(30, 2, 10),
	}, []string{"namespace", "name"})

	migrationFailuresCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hypershift_encryption_migration_failures_total",
		Help: "Total StorageVersionMigration CR failures per hosted cluster.",
	}, []string{"namespace", "name"})
)

func init() {
	metrics.Registry.MustRegister(migrationStateGauge, migrationDurationHistogram, migrationFailuresCounter)
}

var allStates = []string{
	string(hyperv1.EncryptionMigrationStateReadOnlyDeploy),
	string(hyperv1.EncryptionMigrationStateWritePromote),
	string(hyperv1.EncryptionMigrationStateMigrating),
	string(hyperv1.EncryptionMigrationStateCompleted),
	string(hyperv1.EncryptionMigrationStateInterrupted),
	"idle",
}

func recordMigrationState(namespace, name string, status hyperv1.SecretEncryptionStatus) {
	state := "idle"
	if len(status.History) > 0 {
		h := status.History[0]
		if h.State != hyperv1.EncryptionMigrationStateCompleted && h.State != hyperv1.EncryptionMigrationStateInterrupted {
			state = string(h.State)
		}
	}
	for _, s := range allStates {
		val := float64(0)
		if s == state {
			val = 1
		}
		migrationStateGauge.WithLabelValues(namespace, name, s).Set(val)
	}
}

func recordMigrationDuration(namespace, name string, status hyperv1.SecretEncryptionStatus) {
	if len(status.History) == 0 {
		return
	}
	h := status.History[0]
	if h.State == hyperv1.EncryptionMigrationStateCompleted && !h.CompletionTime.IsZero() {
		duration := h.CompletionTime.Sub(h.StartedTime.Time).Seconds()
		migrationDurationHistogram.WithLabelValues(namespace, name).Observe(duration)
	}
}

func recordMigrationFailure(namespace, name string) {
	migrationFailuresCounter.WithLabelValues(namespace, name).Inc()
}
