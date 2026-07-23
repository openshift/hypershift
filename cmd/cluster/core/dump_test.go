package core

import (
	"context"
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

func TestDumpGuestClusterOption(t *testing.T) {
	tests := []struct {
		name               string
		args               []string
		isExpectingAnError bool
		isExpectingADump   bool
		expectedPolicies   []DumpGuestClusterPolicy
	}{
		{
			name:               "do not dump guest cluster",
			args:               []string{"--artifact-dir", "test"},
			isExpectingAnError: false,
			isExpectingADump:   false,
			expectedPolicies:   []DumpGuestClusterPolicy{},
		},
		{
			name:               "dump guest cluster with no policy specified",
			args:               []string{"--dump-guest-cluster", "--artifact-dir", "test"},
			isExpectingAnError: false,
			isExpectingADump:   true,
			expectedPolicies:   []DumpGuestClusterPolicy{},
		},
		{
			name:               "dump guest cluster with true as a value",
			args:               []string{"--dump-guest-cluster=true", "--artifact-dir", "test"},
			isExpectingAnError: false,
			isExpectingADump:   true,
			expectedPolicies:   []DumpGuestClusterPolicy{},
		},
		{
			name:               "dump guest cluster with false as a value",
			args:               []string{"--dump-guest-cluster=false", "--artifact-dir", "test"},
			isExpectingAnError: false,
			isExpectingADump:   false,
			expectedPolicies:   []DumpGuestClusterPolicy{},
		},
		{
			name:               "dump guest cluster with deprecated flag",
			args:               []string{"--dump-guest-cluster-through-kube-service", "--artifact-dir", "test"},
			isExpectingAnError: false,
			isExpectingADump:   true,
			expectedPolicies:   []DumpGuestClusterPolicy{directKubeApiServiceAccess},
		},
		{
			name:               "dump guest cluster with direct-kube-api-service-access policy",
			args:               []string{"--dump-guest-cluster=direct-kube-api-service-access", "--artifact-dir", "test"},
			isExpectingAnError: false,
			isExpectingADump:   true,
			expectedPolicies:   []DumpGuestClusterPolicy{directKubeApiServiceAccess},
		},
		{
			name:               "dump guest cluster with all policies",
			args:               []string{"--dump-guest-cluster=direct-kube-api-service-access,fail-on-error", "--artifact-dir", "test"},
			isExpectingAnError: false,
			isExpectingADump:   true,
			expectedPolicies:   []DumpGuestClusterPolicy{directKubeApiServiceAccess, failOnError},
		},
		{
			name:               "dump guest cluster with an invalid policy",
			args:               []string{"--dump-guest-cluster=direct-kube-api-service-access,invalid-policy", "--artifact-dir", "test"},
			isExpectingAnError: true,
			isExpectingADump:   false,
			expectedPolicies:   []DumpGuestClusterPolicy{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var capturedOpts *DumpOptions
			cmd := NewDumpCommand(func(ctx context.Context, opts *DumpOptions) error {
				capturedOpts = opts
				return nil
			})
			cmd.SetArgs(test.args)
			err := cmd.Execute()

			if test.isExpectingAnError {
				if err == nil {
					t.Fatal("expected an error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("did not expect an error but got: %v", err)
			}

			if capturedOpts == nil {
				t.Fatal("expected dump callback to be called but it wasn't")
			}

			if capturedOpts.IsDumpingGuestCluster != test.isExpectingADump {
				t.Fatalf("expected IsDumpingGuestCluster to be %v but got %v", test.isExpectingADump, capturedOpts.IsDumpingGuestCluster)
			}

			if len(capturedOpts.DumpGuestClusterPolicies) != len(test.expectedPolicies) {
				t.Fatalf("expected DumpGuestClusterPolicies to have length %d but got %d", len(test.expectedPolicies), len(capturedOpts.DumpGuestClusterPolicies))
			}

			for _, policy := range test.expectedPolicies {
				if _, exists := capturedOpts.DumpGuestClusterPolicies[policy]; !exists {
					t.Fatalf("expected DumpGuestClusterPolicies to contain policy %s but it did not", policy)
				}
			}
		})
	}
}
