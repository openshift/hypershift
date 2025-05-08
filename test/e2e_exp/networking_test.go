//go:build e2e_batch
// +build e2e_batch

package e2e_exp

import (
    "testing"
    "math/rand"
    "time"
)

func TestNetworking(t *testing.T) {
    t.Parallel()
    t.Log("Running TestNetworking")

    tests := []struct {
        name string
        duration time.Duration
    }{
        {"DNSResolution", time.Duration(rand.Intn(2000)) * time.Millisecond},
        {"LoadBalancers", time.Duration(rand.Intn(3000)) * time.Millisecond},
        {"ServiceConnectivity", time.Duration(rand.Intn(1500)) * time.Millisecond},
    }

    for _, tt := range tests {
        tt := tt
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            time.Sleep(tt.duration)
            if rand.Float64() < FailureProb() {
                t.Fatalf("Random failure in networking test: %s", tt.name)
            }
        })
    }
}