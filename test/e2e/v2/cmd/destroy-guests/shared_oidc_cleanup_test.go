//go:build e2ev2

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
)

func TestCleanupSharedOIDCAfterDestroy(t *testing.T) {
	for _, testCase := range []struct {
		name                                 string
		owned, destroyFailed, cleanupFailure bool
	}{
		{name: "When normal HC destruction succeeds, it should clean owned OIDC with an independent bounded budget", owned: true},
		{name: "When any HC destruction fails, it should preserve owned OIDC resources", owned: true, destroyFailed: true},
		{name: "When OIDC inputs are external, it should not obtain a client or invoke cleanup"},
		{name: "When cleanup fails after destruction, it should retain that error", owned: true, cleanupFailure: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			directory := t.TempDir()
			if testCase.owned {
				if err := os.WriteFile(filepath.Join(directory, lifecycle.SharedOIDCStateFileName), []byte("owned-state-placeholder"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			original := errors.New("OIDC cleanup failure")
			calls := 0
			err := cleanupSharedOIDCAfterDestroy(directory, testCase.destroyFailed, func(ctx context.Context) error {
				calls++
				deadline, bounded := ctx.Deadline()
				if ctx.Err() != nil || !bounded || time.Until(deadline) > 5*time.Minute {
					t.Fatal("cleanup context is canceled or unbounded")
				}
				if testCase.cleanupFailure {
					return original
				}
				return nil
			})
			wantCalls := 0
			if testCase.owned && !testCase.destroyFailed {
				wantCalls = 1
			}
			if calls != wantCalls || (err != nil) != testCase.cleanupFailure || testCase.cleanupFailure && !errors.Is(err, original) {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
		})
	}
}
