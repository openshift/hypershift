//go:build e2ev2

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestCleanupRunnerSharedOIDC(t *testing.T) {
	for _, test := range []struct {
		name                                string
		canceled, clientError, cleanupError bool
	}{
		{name: "When suites complete normally, it should invoke the guarded shared cleanup consumer"},
		{name: "When the runner is interrupted, it should clean with an independent bounded context", canceled: true},
		{name: "When the management client is unavailable, it should preserve the failure without invoking cleanup", clientError: true},
		{name: "When guarded cleanup fails, it should return the exact cleanup error", cleanupError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.canceled {
				cancel()
			}
			failure := errors.New("cleanup failure")
			called := false
			err := cleanupRunnerSharedOIDC(ctx, "/fixtures/shared", func() (crclient.Client, error) {
				if test.clientError {
					return nil, failure
				}
				return nil, nil
			}, func(cleanupCtx context.Context, directory string, client crclient.Client) error {
				called = true
				deadline, bounded := cleanupCtx.Deadline()
				if cleanupCtx.Err() != nil || !bounded || time.Until(deadline) > 5*time.Minute || directory != "/fixtures/shared" || client != nil {
					t.Fatal("runner cleanup context or arguments differ")
				}
				if test.cleanupError {
					return failure
				}
				return nil
			})
			if test.clientError || test.cleanupError {
				if !errors.Is(err, failure) {
					t.Fatalf("lost cleanup error: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if called == test.clientError {
				t.Fatal("unexpected cleanup invocation")
			}
		})
	}
}
