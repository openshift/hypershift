//go:build e2e_batch
// +build e2e_batch

package e2e_exp

import (
    "testing"
    "math/rand"
    "time"
)

func TestCreateCluster(t *testing.T) {
    t.Parallel()
    t.Log("Running TestCreateCluster")

    tests := []struct {
        name string
        duration time.Duration
    }{
        {"ValidateControlPlane", time.Duration(rand.Intn(3000)) * time.Millisecond},
        {"ValidateNodes", time.Duration(rand.Intn(2000)) * time.Millisecond},
        {"ValidateOperators", time.Duration(rand.Intn(4000)) * time.Millisecond},
    }

    for _, tt := range tests {
        tt := tt
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            time.Sleep(tt.duration)
            if rand.Float64() < FailureProb() {
                t.Fatalf("Random failure in cluster creation test: %s", tt.name)
            }
        })
    }
}