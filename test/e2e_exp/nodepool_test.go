//go:build e2e_batch
// +build e2e_batch

package e2e_exp

import (
    "testing"
    "math/rand"
    "time"
)

func TestNodepool(t *testing.T) {
    t.Parallel()
    t.Log("Running TestNodepool")

    tests := []struct {
        name string
        duration time.Duration
    }{
        {"NodepoolCreation", time.Duration(rand.Intn(3000)) * time.Millisecond},
        {"NodepoolScaling", time.Duration(rand.Intn(4000)) * time.Millisecond},
        {"NodepoolUpdates", time.Duration(rand.Intn(2500)) * time.Millisecond},
    }

    for _, tt := range tests {
        tt := tt
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            time.Sleep(tt.duration)
            if rand.Float64() < FailureProb() {
                t.Fatalf("Random failure in nodepool test: %s", tt.name)
            }
        })
    }
}