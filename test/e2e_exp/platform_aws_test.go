//go:build e2e_batch
// +build e2e_batch

package e2e_exp

import (
    "testing"
    "math/rand"
    "time"
)

func TestPlatformAWS(t *testing.T) {
    t.Parallel()
    t.Log("Running TestPlatformAWS")

    tests := []struct {
        name string
        duration time.Duration
    }{
        {"LoadBalancerIntegration", time.Duration(rand.Intn(3000)) * time.Millisecond},
        {"IAMConfiguration", time.Duration(rand.Intn(2000)) * time.Millisecond},
        {"StorageIntegration", time.Duration(rand.Intn(2500)) * time.Millisecond},
    }

    for _, tt := range tests {
        tt := tt
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            time.Sleep(tt.duration)
            if rand.Float64() < FailureProb() {
                t.Fatalf("Random failure in AWS platform test: %s", tt.name)
            }
        })
    }
}