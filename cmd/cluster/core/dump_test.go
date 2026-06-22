package core

import (
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	clientgotesting "k8s.io/client-go/testing"
)

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
			name:        "group version not found",
			gvk:         schema.GroupVersionKind{Group: "non.existing.group.io", Version: dummyVersion, Kind: dummyKind},
			expected:    false,
			expectError: false,
		},
		{
			name:        "group version found but kind not found",
			gvk:         schema.GroupVersionKind{Group: dummyGroup, Version: dummyVersion, Kind: "non-existing-kind"},
			expected:    false,
			expectError: false,
		},
		{
			name:        "group version kind found",
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
