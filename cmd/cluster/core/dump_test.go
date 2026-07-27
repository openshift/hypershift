package core

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	clientgotesting "k8s.io/client-go/testing"

	"github.com/go-logr/logr"
)

func TestDumpClusterWithRetry(t *testing.T) {
	t.Run("When context is already canceled, it should return an error without retrying", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cancel() // cancel immediately so the ctx.Done() path is taken

		// Clear PATH so DumpCluster cannot find the oc binary and returns
		// an error on every attempt, which lets us exercise the ctx.Done()
		// select branch in the retry loop.
		t.Setenv("PATH", "")

		opts := &DumpOptions{
			Namespace:   "test-ns",
			Name:        "test-cluster",
			ArtifactDir: t.TempDir(),
			Log:         logr.Discard(),
		}

		err := DumpClusterWithRetry(ctx, opts)
		if err == nil {
			t.Fatal("expected an error when context is canceled, got nil")
		}
	})
}

func TestIsResourceRegistered(t *testing.T) {
	dummyGroup := "dummy.group.io"
	dummyVersion := "v2beta3"
	dummyKind := "machinedeployment"

	fakeDiscoveryClient := &fakediscovery.FakeDiscovery{
		Fake: &clientgotesting.Fake{
			Resources: []*metav1.APIResourceList{
				{
					GroupVersion: fmt.Sprintf("%s/%s", dummyGroup, dummyVersion),
					APIResources: []metav1.APIResource{
						{
							Kind: dummyKind,
						},
					},
				},
			},
		},
	}

	tests := []struct {
		name        string
		gvk         schema.GroupVersionKind
		expected    bool
		expectError bool
	}{
		{
			name:        "When group version is not found, it should return false",
			gvk:         schema.GroupVersionKind{Group: "non.existing.group.io", Version: dummyVersion, Kind: dummyKind},
			expected:    false,
			expectError: false,
		},
		{
			name:        "When group version is found but kind is not found, it should return false",
			gvk:         schema.GroupVersionKind{Group: dummyGroup, Version: dummyVersion, Kind: "non-existing-kind"},
			expected:    false,
			expectError: false,
		},
		{
			name:        "When group version kind is found, it should return true",
			gvk:         schema.GroupVersionKind{Group: dummyGroup, Version: dummyVersion, Kind: dummyKind},
			expected:    true,
			expectError: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := isResourceRegistered(fakeDiscoveryClient, test.gvk)
			if result != test.expected {
				t.Errorf("expected %v, got %v", test.expected, result)
			}
			if (err != nil) != test.expectError {
				t.Errorf("expected error: %v, got error: %v", test.expectError, err)
			}
		})
	}
}
