package oadp

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestCreateOptionsDefaults verifies that the default values for CreateOptions
// are set correctly through the flag system in NewCreateBackupCommand.
func TestCreateOptionsDefaults(t *testing.T) {
	// Create the command which should set up default values via flags
	cmd := NewCreateBackupCommand()

	// Parse empty args to trigger default values
	cmd.SetArgs([]string{"--hc-name", "test", "--hc-namespace", "test"})
	err := cmd.ParseFlags([]string{"--hc-name", "test", "--hc-namespace", "test"})
	if err != nil {
		t.Fatalf("Failed to parse flags: %v", err)
	}

	// Extract the CreateOptions from the command context
	// We need to access the bound variables that were set by the flag parsing
	oadpNamespace, _ := cmd.Flags().GetString("oadp-namespace")
	storageLocation, _ := cmd.Flags().GetString("storage-location")
	ttl, _ := cmd.Flags().GetDuration("ttl")
	snapshotMoveData, _ := cmd.Flags().GetBool("snapshot-move-data")
	defaultVolumesToFsBackup, _ := cmd.Flags().GetBool("default-volumes-to-fs-backup")
	render, _ := cmd.Flags().GetBool("render")
	includedResources, _ := cmd.Flags().GetStringSlice("included-resources")

	// Verify the default values
	if oadpNamespace != "openshift-adp" {
		t.Errorf("Expected default OADP namespace to be 'openshift-adp', got %s", oadpNamespace)
	}

	if storageLocation != "default" {
		t.Errorf("Expected default storage location to be 'default', got %s", storageLocation)
	}

	if ttl != 2*time.Hour {
		t.Errorf("Expected default TTL to be 2h, got %v", ttl)
	}

	if !snapshotMoveData {
		t.Errorf("Expected default SnapshotMoveData to be true")
	}

	if defaultVolumesToFsBackup {
		t.Errorf("Expected default DefaultVolumesToFsBackup to be false")
	}

	if render {
		t.Errorf("Expected default Render to be false")
	}

	if len(includedResources) != 0 {
		t.Errorf("Expected default IncludedResources to be empty, got %v", includedResources)
	}
}

// TestBackupNameGeneration tests the naming pattern for backup objects using the real production code.
// Verifies that backup names follow the expected format: {hc-name}-{hc-namespace}-{random-hash}.
func TestBackupNameGeneration(t *testing.T) {
	hcName := "test-cluster"
	hcNamespace := "test-cluster-ns"

	// Create a deterministic random string generator for testing
	deterministicRandomGen := func(length int) string {
		return "abc123"
	}

	expectedName := "test-cluster-test-cluster-ns-abc123"

	// Test the actual production code path with deterministic input
	actualName := generateBackupName(hcName, hcNamespace, deterministicRandomGen)

	if actualName != expectedName {
		t.Errorf("Expected backup name %s, got %s", expectedName, actualName)
	}

	// Test with different inputs to ensure the pattern is correct
	testCases := []struct {
		hcName      string
		hcNamespace string
		expected    string
	}{
		{"prod-cluster", "prod-cluster-ns", "prod-cluster-prod-cluster-ns-abc123"},
		{"my-cluster", "my-cluster-ns", "my-cluster-my-cluster-ns-abc123"},
		{"dev", "dev-ns", "dev-dev-ns-abc123"},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s-%s", tc.hcName, tc.hcNamespace), func(t *testing.T) {
			actual := generateBackupName(tc.hcName, tc.hcNamespace, deterministicRandomGen)
			if actual != tc.expected {
				t.Errorf("Expected backup name %s, got %s", tc.expected, actual)
			}
		})
	}
}

// TestGenerateBackupObject validates the basic structure and metadata of generated backup objects.
// This test focuses on the fundamental properties like APIVersion, Kind, ObjectMeta, and IncludedNamespaces.
// It serves as a structural validation test for the core backup object generation functionality.
func TestGenerateBackupObject(t *testing.T) {
	opts := &CreateOptions{
		HCName:                   "test-cluster",
		HCNamespace:              "test-cluster-ns",
		OADPNamespace:            "openshift-adp",
		StorageLocation:          "default",
		TTL:                      2 * time.Hour,
		SnapshotMoveData:         true,
		DefaultVolumesToFsBackup: false,
	}

	backup, backupName, err := opts.generateBackupObjectWithPlatform("AWS")
	if err != nil {
		t.Errorf("generateBackupObject() error = %v", err)
		return
	}

	// Check backup name format
	if len(backupName) == 0 {
		t.Errorf("Expected backup name to be generated, got empty string")
	}

	// Check that backup name contains hc-name and hc-namespace
	if !strings.Contains(backupName, opts.HCName) {
		t.Errorf("Expected backup name to contain hc-name '%s', got %s", opts.HCName, backupName)
	}

	if !strings.Contains(backupName, opts.HCNamespace) {
		t.Errorf("Expected backup name to contain hc-namespace '%s', got %s", opts.HCNamespace, backupName)
	}

	// Check backup object structure
	if backup.GetAPIVersion() != "velero.io/v1" {
		t.Errorf("Expected API version 'velero.io/v1', got %s", backup.GetAPIVersion())
	}

	if backup.GetKind() != "Backup" {
		t.Errorf("Expected kind 'Backup', got %s", backup.GetKind())
	}

	if backup.GetName() != backupName {
		t.Errorf("Expected backup name %s, got %s", backupName, backup.GetName())
	}

	if backup.GetNamespace() != opts.OADPNamespace {
		t.Errorf("Expected backup namespace %s, got %s", opts.OADPNamespace, backup.GetNamespace())
	}

	// Check included namespaces
	expectedNamespaces := []string{opts.HCNamespace, fmt.Sprintf("%s-%s", opts.HCNamespace, opts.HCName)}
	namespacesInterface, found, err := unstructured.NestedFieldNoCopy(backup.Object, "spec", "includedNamespaces")
	if err != nil || !found {
		t.Errorf("Expected to find spec.includedNamespaces field in backup object")
		return
	}
	// Try to cast to []string first, if that fails try []interface{}
	var includedNamespaces []string
	if namespacesSlice, ok := namespacesInterface.([]string); ok {
		includedNamespaces = namespacesSlice
	} else if namespacesInterfaceSlice, ok := namespacesInterface.([]interface{}); ok {
		for _, ns := range namespacesInterfaceSlice {
			includedNamespaces = append(includedNamespaces, ns.(string))
		}
	} else {
		t.Errorf("Expected includedNamespaces to be []string or []interface{}, got %T", namespacesInterface)
		return
	}

	if len(includedNamespaces) != len(expectedNamespaces) {
		t.Errorf("Expected %d included namespaces, got %d", len(expectedNamespaces), len(includedNamespaces))
	}

	// Check that the correct namespaces are included
	for i, expected := range expectedNamespaces {
		if i < len(includedNamespaces) && includedNamespaces[i] != expected {
			t.Errorf("Expected namespace[%d] to be '%s', got '%s'", i, expected, includedNamespaces[i])
		}
	}
}

// TestGenerateBackupObjectComprehensive provides comprehensive testing of backup object generation
// across multiple scenarios including:
// - Custom resource selection (user-defined IncludedResources)
// - Default resource selection with platform-specific resources
// - Multi-platform support (AWS, Agent, KubeVirt, OpenStack, Azure)
// This test ensures that the backup generation logic correctly handles different platforms
// and resource selection strategies.
func TestGenerateBackupObjectComprehensive(t *testing.T) {
	type testCase struct {
		name                     string
		platform                 string
		includedResources        []string
		expectedMinResources     int
		expectedBaseResources    []string
		expectedPlatformSpecific []string
		customResourcesExact     bool // if true, expect exact match for includedResources
	}

	// Use global platform resource mappings from create.go
	testPlatformResources := map[string][]string{
		"AWS":       awsResources,
		"AGENT":     agentResources,
		"KUBEVIRT":  kubevirtResources,
		"OPENSTACK": openstackResources,
		"AZURE":     azureResources,
	}

	// Base resources expected in all default configurations
	expectedBaseResources := []string{"hostedclusters.hypershift.openshift.io", "nodepools.hypershift.openshift.io", "secrets", "configmaps"}

	tests := []testCase{
		// Test cases for custom resources
		{
			name:                  "Custom resources - minimal set",
			platform:              "AWS",
			includedResources:     []string{"configmaps", "secrets", "pods"},
			expectedMinResources:  3,
			expectedBaseResources: []string{"configmaps", "secrets", "pods"},
			customResourcesExact:  true,
		},
		{
			name:                  "Custom resources - specific selection",
			platform:              "KUBEVIRT",
			includedResources:     []string{"hostedclusters.hypershift.openshift.io", "nodepools.hypershift.openshift.io", "secrets"},
			expectedMinResources:  3,
			expectedBaseResources: []string{"hostedclusters.hypershift.openshift.io", "nodepools.hypershift.openshift.io", "secrets"},
			customResourcesExact:  true,
		},
		// Test cases for default resources with different platforms
		{
			name:                     "Default resources - AWS platform",
			platform:                 "AWS",
			includedResources:        nil,
			expectedMinResources:     10,
			expectedBaseResources:    expectedBaseResources,
			expectedPlatformSpecific: testPlatformResources["AWS"],
			customResourcesExact:     false,
		},
		{
			name:                     "Default resources - Agent platform",
			platform:                 "AGENT",
			includedResources:        nil,
			expectedMinResources:     10,
			expectedBaseResources:    expectedBaseResources,
			expectedPlatformSpecific: testPlatformResources["AGENT"],
			customResourcesExact:     false,
		},
		{
			name:                     "Default resources - KubeVirt platform",
			platform:                 "KUBEVIRT",
			includedResources:        nil,
			expectedMinResources:     10,
			expectedBaseResources:    expectedBaseResources,
			expectedPlatformSpecific: testPlatformResources["KUBEVIRT"],
			customResourcesExact:     false,
		},
		{
			name:                     "Default resources - OpenStack platform",
			platform:                 "OPENSTACK",
			includedResources:        nil,
			expectedMinResources:     10,
			expectedBaseResources:    expectedBaseResources,
			expectedPlatformSpecific: testPlatformResources["OPENSTACK"],
			customResourcesExact:     false,
		},
		{
			name:                     "Default resources - Azure platform",
			platform:                 "AZURE",
			includedResources:        nil,
			expectedMinResources:     10,
			expectedBaseResources:    expectedBaseResources,
			expectedPlatformSpecific: testPlatformResources["AZURE"],
			customResourcesExact:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &CreateOptions{
				HCName:                   "test-cluster",
				HCNamespace:              "test-cluster-ns",
				OADPNamespace:            "openshift-adp",
				StorageLocation:          "default",
				TTL:                      2 * time.Hour,
				SnapshotMoveData:         true,
				DefaultVolumesToFsBackup: false,
				IncludedResources:        tt.includedResources,
			}

			backup, backupName, err := opts.generateBackupObjectWithPlatform(tt.platform)
			if err != nil {
				t.Errorf("generateBackupObjectWithPlatform() error = %v", err)
				return
			}

			// Basic validation
			if len(backupName) == 0 {
				t.Errorf("Expected backup name to be generated, got empty string")
			}

			if backup.GetAPIVersion() != "velero.io/v1" {
				t.Errorf("Expected API version 'velero.io/v1', got %s", backup.GetAPIVersion())
			}

			if backup.GetKind() != "Backup" {
				t.Errorf("Expected kind 'Backup', got %s", backup.GetKind())
			}

			// Get included resources from spec
			includedResourcesInterface, found, err := unstructured.NestedFieldNoCopy(backup.Object, "spec", "includedResources")
			if err != nil || !found {
				t.Errorf("Expected to find spec.includedResources field in backup object")
				return
			}
			// Try to cast to []string first, if that fails try []interface{}
			var includedResources []string
			if resourcesSlice, ok := includedResourcesInterface.([]string); ok {
				includedResources = resourcesSlice
			} else if resourcesInterfaceSlice, ok := includedResourcesInterface.([]interface{}); ok {
				for _, res := range resourcesInterfaceSlice {
					includedResources = append(includedResources, res.(string))
				}
			} else {
				t.Errorf("Expected includedResources to be []string or []interface{}, got %T", includedResourcesInterface)
				return
			}

			// Check minimum number of resources
			if len(includedResources) < tt.expectedMinResources {
				t.Errorf("Expected at least %d resources, got %d", tt.expectedMinResources, len(includedResources))
			}

			// For custom resources, check exact match
			if tt.customResourcesExact {
				if len(includedResources) != len(tt.expectedBaseResources) {
					t.Errorf("Expected exactly %d resources, got %d", len(tt.expectedBaseResources), len(includedResources))
				}
				for i, expected := range tt.expectedBaseResources {
					if i < len(includedResources) && includedResources[i] != expected {
						t.Errorf("Expected resource[%d] to be '%s', got '%s'", i, expected, includedResources[i])
					}
				}
				return // Skip platform-specific checks for custom resources
			}

			// For default resources, check contains
			resourcesStr := fmt.Sprintf("%v", includedResources)

			// Check base resources are included
			for _, expected := range tt.expectedBaseResources {
				if !strings.Contains(resourcesStr, expected) {
					t.Errorf("Expected %s backup to contain base resource '%s'", tt.platform, expected)
				}
			}

			// Check platform-specific resources are included
			for _, expected := range tt.expectedPlatformSpecific {
				if !strings.Contains(resourcesStr, expected) {
					t.Errorf("Expected %s backup to contain platform-specific resource '%s'", tt.platform, expected)
				}
			}
		})
	}
}

// TestGetDefaultResourcesForPlatform verifies that the getDefaultResourcesForPlatform function
// returns the correct set of resources for each supported platform type.
// This test ensures that:
// - Base resources are always included regardless of platform
// - Platform-specific resources are correctly added based on the platform type
// - Platform name normalization works (lowercase -> uppercase)
// - Unknown platforms default to AWS resources
func TestGetDefaultResourcesForPlatform(t *testing.T) {
	tests := []struct {
		name                     string
		platform                 string
		expectedPlatformSpecific []string
	}{
		{
			name:                     "AWS platform",
			platform:                 "AWS",
			expectedPlatformSpecific: awsResources,
		},
		{
			name:                     "Agent platform",
			platform:                 "AGENT",
			expectedPlatformSpecific: agentResources,
		},
		{
			name:                     "KubeVirt platform",
			platform:                 "KUBEVIRT",
			expectedPlatformSpecific: kubevirtResources,
		},
		{
			name:                     "OpenStack platform",
			platform:                 "OPENSTACK",
			expectedPlatformSpecific: openstackResources,
		},
		{
			name:                     "Azure platform",
			platform:                 "AZURE",
			expectedPlatformSpecific: azureResources,
		},
		{
			name:                     "Unknown platform defaults to AWS",
			platform:                 "UNKNOWN",
			expectedPlatformSpecific: awsResources,
		},
		{
			name:                     "Lowercase platform should work",
			platform:                 "aws",
			expectedPlatformSpecific: awsResources,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources := getDefaultResourcesForPlatform(tt.platform)

			// Check that we have a reasonable number of resources
			if len(resources) < 15 {
				t.Errorf("Expected at least 15 resources, got %d", len(resources))
			}

			// Convert to string slice for easier checking
			resourcesStr := fmt.Sprintf("%v", resources)

			// Check base resources are always included
			for _, expected := range baseResources {
				if !strings.Contains(resourcesStr, expected) {
					t.Errorf("Expected base resources to contain '%s'", expected)
				}
			}

			// Check platform-specific resources
			for _, expected := range tt.expectedPlatformSpecific {
				if !strings.Contains(resourcesStr, expected) {
					t.Errorf("Expected platform-specific resources for %s to contain '%s'", tt.platform, expected)
				}
			}
		})
	}
}
