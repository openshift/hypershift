//go:build e2ev2

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"

	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func TestDestroyClusterHonorsDeadline(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "hypershift")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec sleep 2\n"), 0700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	err := destroyCluster(ctx, binary, lifecycle.ClusterEntry{Name: "guest"}, &lifecycle.AWSPlatformConfig{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected destroy to exceed its deadline, got %v", err)
	}
}

func TestWaitForNodeClaimDeletion(t *testing.T) {
	deleted := func(name string) watch.Event {
		return watch.Event{Type: watch.Deleted, Object: &karpenterv1.NodeClaim{ObjectMeta: metav1.ObjectMeta{Name: name}}}
	}
	for _, tc := range []struct {
		name    string
		events  []watch.Event
		wantErr string
	}{
		{
			name:   "records all deletions in any order",
			events: []watch.Event{deleted("second"), deleted("not-in-snapshot"), deleted("first")},
		},
		{
			name:    "closing the hosted API watch cannot count as deletion",
			events:  []watch.Event{deleted("first")},
			wantErr: "before observing deletion of [second]",
		},
		{
			name:    "watch errors fail verification",
			events:  []watch.Event{{Type: watch.Error, Object: &metav1.Status{Message: "resource version expired"}}},
			wantErr: "resource version expired",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := make(chan watch.Event, len(tc.events))
			for _, event := range tc.events {
				events <- event
			}
			close(events)

			err := waitForNodeClaimDeletion(t.Context(), events, sets.New("first", "second"))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestWriteKarpenterTeardownResult(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ARTIFACT_DIR", dir)
	if err := writeKarpenterTeardownResult(time.Second, errors.New("EC2 instance & VPC remain")); err != nil {
		t.Fatal(err)
	}
	report, err := os.ReadFile(filepath.Join(dir, "junit_karpenter_teardown.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), `failures="1"`) || !strings.Contains(string(report), "EC2 instance &amp; VPC remain") {
		t.Fatalf("missing JUnit failure or escaped diagnostic: %s", report)
	}
}
