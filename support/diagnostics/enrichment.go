package diagnostics

import (
	"os"
	"strconv"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	// EnvDisableDiagnosticEnrichment is the environment variable that disables
	// diagnostic enrichment of condition messages. Parsed with strconv.ParseBool:
	// "1", "t", "true" disable enrichment; "0", "f", "false" leave it enabled;
	// any other non-empty value is treated as true (disabled, fail-safe).
	// No restart is needed after setting it; the next reconciliation loop picks
	// up the change.
	EnvDisableDiagnosticEnrichment = "HYPERSHIFT_DISABLE_DIAGNOSTIC_ENRICHMENT"
)

// DiagnosticFallbackCounter tracks cases where enrichment was requested but
// the enrichment data (e.g. role ARN, endpoint URL) was unavailable.
var DiagnosticFallbackCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "hypershift_diagnostic_enrichment_fallback_total",
		Help: "Number of times diagnostic enrichment fell back to the original message because enrichment data was unavailable.",
	},
	[]string{"platform", "condition", "case"},
)

var registerOnce sync.Once

// MustRegister registers the diagnostic fallback counter in the given registry.
// It is safe to call from multiple init() functions or goroutines; only the
// first call performs the registration.
func MustRegister(reg prometheus.Registerer) {
	registerOnce.Do(func() {
		reg.MustRegister(DiagnosticFallbackCounter)
	})
}

// IsEnrichmentDisabled returns true when diagnostic enrichment is disabled via
// the HYPERSHIFT_DISABLE_DIAGNOSTIC_ENRICHMENT environment variable.
// The value is read on every call so that toggling the env var takes effect on
// the next reconciliation loop without a process restart.
// Values "true" and "1" disable enrichment; "false", "0", and unset leave it
// enabled. Any other non-empty value is treated as true (disabled).
func IsEnrichmentDisabled() bool {
	v := os.Getenv(EnvDisableDiagnosticEnrichment)
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		// Non-empty, non-parseable value: treat as disabled (fail safe).
		return true
	}
	return b
}
