//go:build e2e_batch
// +build e2e_batch

package e2e_exp

import (
    "testing"
    "math/rand"
    "time"
)

func TestMonitoring(t *testing.T) {
    t.Parallel()
    t.Log("Running TestMonitoring")

    tests := []struct {
        name string
        duration time.Duration
    }{
        {"PrometheusDeployment", time.Duration(rand.Intn(2500)) * time.Millisecond},
        {"AlertmanagerConfig", time.Duration(rand.Intn(2000)) * time.Millisecond},
        {"MetricsAvailability", time.Duration(rand.Intn(3000)) * time.Millisecond},
    }

    for _, tt := range tests {
        tt := tt
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            time.Sleep(tt.duration)
            if rand.Float64() < FailureProb() {
                t.Fatalf("Random failure in monitoring test: %s", tt.name)
            }
        })
    }
}