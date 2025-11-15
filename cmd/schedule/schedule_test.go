package schedule

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openshift/hypershift/support/oadp"
)

// TestCreateOptionsDefaults verifies that the default values for CreateOptions
// are set correctly through the flag system in NewCreateCommand.
func TestCreateOptionsDefaults(t *testing.T) {
	// Create the command which should set up default values via flags
	cmd := NewCreateCommand()

	// Parse empty args to trigger default values
	cmd.SetArgs([]string{"--hc-name", "test", "--hc-namespace", "test", "--schedule", "daily"})
	err := cmd.ParseFlags([]string{"--hc-name", "test", "--hc-namespace", "test", "--schedule", "daily"})
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
	paused, _ := cmd.Flags().GetBool("paused")
	useOwnerReferences, _ := cmd.Flags().GetBool("use-owner-references")
	skipImmediately, _ := cmd.Flags().GetBool("skip-immediately")

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

	if paused {
		t.Errorf("Expected default Paused to be false")
	}

	if useOwnerReferences {
		t.Errorf("Expected default UseOwnerReferences to be false")
	}

	if skipImmediately {
		t.Errorf("Expected default SkipImmediately to be false")
	}
}

// TestScheduleNameGeneration tests the naming pattern for schedule objects using the real production code.
// Verifies that schedule names follow the expected format: {hc-name}-{hc-namespace}-schedule-{random-hash}.
func TestScheduleNameGeneration(t *testing.T) {
	hcName := "test-cluster"
	hcNamespace := "clusters"

	// Create a deterministic random string generator for testing
	deterministicRandomGen := func(length int) string {
		return "abc123"
	}

	expectedName := "test-cluster-clusters-schedule-abc123"

	// Test the actual production code path with deterministic input
	actualName := generateScheduleName(hcName, hcNamespace, deterministicRandomGen)

	if actualName != expectedName {
		t.Errorf("Expected schedule name %s, got %s", expectedName, actualName)
	}

	// Test with different inputs to ensure the pattern is correct
	testCases := []struct {
		hcName      string
		hcNamespace string
		expected    string
	}{
		{"prod", "default", "prod-default-schedule-abc123"},
		{"my-cluster", "hcp01", "my-cluster-hcp01-schedule-abc123"},
		{"test", "ns", "test-ns-schedule-abc123"},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s-%s", tc.hcName, tc.hcNamespace), func(t *testing.T) {
			actual := generateScheduleName(tc.hcName, tc.hcNamespace, deterministicRandomGen)
			if actual != tc.expected {
				t.Errorf("Expected schedule name %s, got %s", tc.expected, actual)
			}
		})
	}
}

// TestValidateAndResolveSchedule tests the schedule validation and preset resolution logic.
func TestValidateAndResolveSchedule(t *testing.T) {
	tests := []struct {
		name            string
		inputSchedule   string
		expectedSchedule string
		expectError     bool
		errorContains   string
	}{
		{
			name:            "daily preset",
			inputSchedule:   "daily",
			expectedSchedule: "0 2 * * *",
			expectError:     false,
		},
		{
			name:            "weekly preset",
			inputSchedule:   "weekly",
			expectedSchedule: "0 2 * * 0",
			expectError:     false,
		},
		{
			name:            "monthly preset",
			inputSchedule:   "monthly",
			expectedSchedule: "0 2 1 * *",
			expectError:     false,
		},
		{
			name:            "hourly preset",
			inputSchedule:   "hourly",
			expectedSchedule: "0 * * * *",
			expectError:     false,
		},
		{
			name:            "case insensitive preset",
			inputSchedule:   "DAILY",
			expectedSchedule: "0 2 * * *",
			expectError:     false,
		},
		{
			name:            "valid custom cron",
			inputSchedule:   "0 */6 * * *",
			expectedSchedule: "0 */6 * * *",
			expectError:     false,
		},
		{
			name:            "another valid custom cron",
			inputSchedule:   "30 1 * * 1-5",
			expectedSchedule: "30 1 * * 1-5",
			expectError:     false,
		},
		{
			name:          "invalid cron - too few fields",
			inputSchedule: "0 2 *",
			expectError:   true,
			errorContains: "invalid cron schedule",
		},
		{
			name:          "invalid cron - too many fields",
			inputSchedule: "0 2 * * * * extra",
			expectError:   true,
			errorContains: "invalid cron schedule",
		},
		{
			name:          "empty schedule",
			inputSchedule: "",
			expectError:   true,
			errorContains: "invalid cron schedule",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &CreateOptions{
				Schedule: tt.inputSchedule,
			}

			err := opts.validateAndResolveSchedule()

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error for schedule '%s' but got none", tt.inputSchedule)
				} else if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("Expected error to contain '%s', got: %s", tt.errorContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for schedule '%s': %v", tt.inputSchedule, err)
				} else if opts.Schedule != tt.expectedSchedule {
					t.Errorf("Expected resolved schedule '%s', got '%s'", tt.expectedSchedule, opts.Schedule)
				}
			}
		})
	}
}

// TestGenerateScheduleObject validates the basic structure and metadata of generated schedule objects.
// This test focuses on the fundamental properties like APIVersion, Kind, ObjectMeta, and template spec.
func TestGenerateScheduleObject(t *testing.T) {
	opts := &CreateOptions{
		HCName:                   "test-cluster",
		HCNamespace:              "clusters",
		Schedule:                 "0 2 * * *",
		OADPNamespace:            "openshift-adp",
		StorageLocation:          "default",
		TTL:                      2 * time.Hour,
		SnapshotMoveData:         true,
		DefaultVolumesToFsBackup: false,
		Paused:                   false,
		UseOwnerReferences:       false,
		SkipImmediately:          false,
	}

	schedule, scheduleName, err := opts.generateScheduleObjectWithPlatform("AWS")
	if err != nil {
		t.Errorf("generateScheduleObjectWithPlatform() error = %v", err)
		return
	}

	// Check schedule name format
	if len(scheduleName) == 0 {
		t.Errorf("Expected schedule name to be generated, got empty string")
	}

	// Check that schedule name contains hc-name and hc-namespace
	if !strings.Contains(scheduleName, opts.HCName) {
		t.Errorf("Expected schedule name to contain hc-name '%s', got %s", opts.HCName, scheduleName)
	}

	if !strings.Contains(scheduleName, opts.HCNamespace) {
		t.Errorf("Expected schedule name to contain hc-namespace '%s', got %s", opts.HCNamespace, scheduleName)
	}

	if !strings.Contains(scheduleName, "schedule") {
		t.Errorf("Expected schedule name to contain 'schedule', got %s", scheduleName)
	}

	// Check schedule object structure
	if schedule.APIVersion != "velero.io/v1" {
		t.Errorf("Expected API version 'velero.io/v1', got %s", schedule.APIVersion)
	}

	if schedule.Kind != "Schedule" {
		t.Errorf("Expected kind 'Schedule', got %s", schedule.Kind)
	}

	if schedule.Name != scheduleName {
		t.Errorf("Expected schedule name %s, got %s", scheduleName, schedule.Name)
	}

	if schedule.Namespace != opts.OADPNamespace {
		t.Errorf("Expected schedule namespace %s, got %s", opts.OADPNamespace, schedule.Namespace)
	}

	// Check schedule spec
	if schedule.Spec.Schedule != opts.Schedule {
		t.Errorf("Expected schedule cron '%s', got '%s'", opts.Schedule, schedule.Spec.Schedule)
	}

	if schedule.Spec.Paused != opts.Paused {
		t.Errorf("Expected paused %v, got %v", opts.Paused, schedule.Spec.Paused)
	}

	if schedule.Spec.UseOwnerReferencesInBackup == nil || *schedule.Spec.UseOwnerReferencesInBackup != opts.UseOwnerReferences {
		t.Errorf("Expected UseOwnerReferencesInBackup %v, got %v", opts.UseOwnerReferences, schedule.Spec.UseOwnerReferencesInBackup)
	}

	if schedule.Spec.SkipImmediately == nil || *schedule.Spec.SkipImmediately != opts.SkipImmediately {
		t.Errorf("Expected SkipImmediately %v, got %v", opts.SkipImmediately, schedule.Spec.SkipImmediately)
	}

	// Check backup template included namespaces
	expectedNamespaces := []string{opts.HCNamespace, fmt.Sprintf("%s-%s", opts.HCNamespace, opts.HCName)}
	if len(schedule.Spec.Template.IncludedNamespaces) != len(expectedNamespaces) {
		t.Errorf("Expected %d included namespaces, got %d", len(expectedNamespaces), len(schedule.Spec.Template.IncludedNamespaces))
	}

	// Check that the correct namespaces are included
	for i, expected := range expectedNamespaces {
		if i < len(schedule.Spec.Template.IncludedNamespaces) && schedule.Spec.Template.IncludedNamespaces[i] != expected {
			t.Errorf("Expected namespace[%d] to be '%s', got '%s'", i, expected, schedule.Spec.Template.IncludedNamespaces[i])
		}
	}

	// Check labels
	expectedLabels := map[string]string{
		"velero.io/storage-location":                        opts.StorageLocation,
		"hypershift.openshift.io/hosted-cluster":           opts.HCName,
		"hypershift.openshift.io/hosted-cluster-namespace": opts.HCNamespace,
	}

	for key, expectedValue := range expectedLabels {
		if actualValue, exists := schedule.Labels[key]; !exists || actualValue != expectedValue {
			t.Errorf("Expected label '%s' to be '%s', got '%s'", key, expectedValue, actualValue)
		}
	}
}

// TestGenerateScheduleObjectComprehensive provides comprehensive testing of schedule object generation
// across multiple scenarios including:
// - Custom resource selection (user-defined IncludedResources)
// - Default resource selection with platform-specific resources
// - Multi-platform support (AWS, Agent, KubeVirt, OpenStack, Azure)
// - Custom schedule names vs auto-generated names
// - Different schedule configurations
func TestGenerateScheduleObjectComprehensive(t *testing.T) {
	type testCase struct {
		name                     string
		platform                 string
		includedResources        []string
		scheduleName             string
		paused                   bool
		expectedMinResources     int
		expectedBaseResources    []string
		expectedPlatformSpecific []string
		customResourcesExact     bool // if true, expect exact match for includedResources
	}

	// Use global platform resource mappings from oadp package
	testPlatformResources := map[string][]string{
		"AWS":       oadp.AWSResources,
		"AGENT":     oadp.AgentResources,
		"KUBEVIRT":  oadp.KubevirtResources,
		"OPENSTACK": oadp.OpenstackResources,
		"AZURE":     oadp.AzureResources,
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
		// Test cases for paused schedules and custom names
		{
			name:                  "Paused schedule with custom name",
			platform:              "AWS",
			scheduleName:          "my-custom-schedule",
			paused:                true,
			includedResources:     []string{"secrets", "configmaps"},
			expectedMinResources:  2,
			expectedBaseResources: []string{"secrets", "configmaps"},
			customResourcesExact:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &CreateOptions{
				HCName:                   "test-cluster",
				HCNamespace:              "clusters",
				Schedule:                 "0 2 * * *",
				ScheduleName:             tt.scheduleName,
				OADPNamespace:            "openshift-adp",
				StorageLocation:          "default",
				TTL:                      2 * time.Hour,
				SnapshotMoveData:         true,
				DefaultVolumesToFsBackup: false,
				IncludedResources:        tt.includedResources,
				Paused:                   tt.paused,
				UseOwnerReferences:       false,
				SkipImmediately:          false,
			}

			schedule, scheduleName, err := opts.generateScheduleObjectWithPlatform(tt.platform)
			if err != nil {
				t.Errorf("generateScheduleObjectWithPlatform() error = %v", err)
				return
			}

			// Basic validation
			if len(scheduleName) == 0 {
				t.Errorf("Expected schedule name to be generated, got empty string")
			}

			if schedule.APIVersion != "velero.io/v1" {
				t.Errorf("Expected API version 'velero.io/v1', got %s", schedule.APIVersion)
			}

			if schedule.Kind != "Schedule" {
				t.Errorf("Expected kind 'Schedule', got %s", schedule.Kind)
			}

			// Check custom name vs auto-generated
			if tt.scheduleName != "" {
				if scheduleName != tt.scheduleName {
					t.Errorf("Expected custom schedule name '%s', got '%s'", tt.scheduleName, scheduleName)
				}
			} else {
				if !strings.Contains(scheduleName, opts.HCName) || !strings.Contains(scheduleName, "schedule") {
					t.Errorf("Expected auto-generated schedule name to contain hc-name and 'schedule', got '%s'", scheduleName)
				}
			}

			// Check paused state
			if schedule.Spec.Paused != tt.paused {
				t.Errorf("Expected paused %v, got %v", tt.paused, schedule.Spec.Paused)
			}

			// Check minimum number of resources in backup template
			if len(schedule.Spec.Template.IncludedResources) < tt.expectedMinResources {
				t.Errorf("Expected at least %d resources, got %d", tt.expectedMinResources, len(schedule.Spec.Template.IncludedResources))
			}

			// For custom resources, check exact match
			if tt.customResourcesExact {
				if len(schedule.Spec.Template.IncludedResources) != len(tt.expectedBaseResources) {
					t.Errorf("Expected exactly %d resources, got %d", len(tt.expectedBaseResources), len(schedule.Spec.Template.IncludedResources))
				}
				for i, expected := range tt.expectedBaseResources {
					if i < len(schedule.Spec.Template.IncludedResources) && schedule.Spec.Template.IncludedResources[i] != expected {
						t.Errorf("Expected resource[%d] to be '%s', got '%s'", i, expected, schedule.Spec.Template.IncludedResources[i])
					}
				}
				return // Skip platform-specific checks for custom resources
			}

			// For default resources, check contains
			resourcesStr := fmt.Sprintf("%v", schedule.Spec.Template.IncludedResources)

			// Check base resources are included
			for _, expected := range tt.expectedBaseResources {
				if !strings.Contains(resourcesStr, expected) {
					t.Errorf("Expected %s schedule to contain base resource '%s'", tt.platform, expected)
				}
			}

			// Check platform-specific resources are included
			for _, expected := range tt.expectedPlatformSpecific {
				if !strings.Contains(resourcesStr, expected) {
					t.Errorf("Expected %s schedule to contain platform-specific resource '%s'", tt.platform, expected)
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
			expectedPlatformSpecific: oadp.AWSResources,
		},
		{
			name:                     "Agent platform",
			platform:                 "AGENT",
			expectedPlatformSpecific: oadp.AgentResources,
		},
		{
			name:                     "KubeVirt platform",
			platform:                 "KUBEVIRT",
			expectedPlatformSpecific: oadp.KubevirtResources,
		},
		{
			name:                     "OpenStack platform",
			platform:                 "OPENSTACK",
			expectedPlatformSpecific: oadp.OpenstackResources,
		},
		{
			name:                     "Azure platform",
			platform:                 "AZURE",
			expectedPlatformSpecific: oadp.AzureResources,
		},
		{
			name:                     "Unknown platform defaults to AWS",
			platform:                 "UNKNOWN",
			expectedPlatformSpecific: oadp.AWSResources,
		},
		{
			name:                     "Lowercase platform should work",
			platform:                 "aws",
			expectedPlatformSpecific: oadp.AWSResources,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources := oadp.GetDefaultResourcesForPlatform(tt.platform)

			// Check that we have a reasonable number of resources
			if len(resources) < 15 {
				t.Errorf("Expected at least 15 resources, got %d", len(resources))
			}

			// Convert to string slice for easier checking
			resourcesStr := fmt.Sprintf("%v", resources)

			// Check base resources are always included
			for _, expected := range oadp.BaseResources {
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

// TestSchedulePresets verifies that the schedule presets are correctly defined.
func TestSchedulePresets(t *testing.T) {
	expectedPresets := map[string]string{
		"daily":   "0 2 * * *",
		"weekly":  "0 2 * * 0",
		"monthly": "0 2 1 * *",
		"hourly":  "0 * * * *",
	}

	if !reflect.DeepEqual(schedulePresets, expectedPresets) {
		t.Errorf("schedulePresets = %v, want %v", schedulePresets, expectedPresets)
	}

	// Verify each preset individually for clarity
	for presetName, expectedCron := range expectedPresets {
		if actualCron, exists := schedulePresets[presetName]; !exists {
			t.Errorf("Missing preset '%s'", presetName)
		} else if actualCron != expectedCron {
			t.Errorf("Preset '%s' = '%s', want '%s'", presetName, actualCron, expectedCron)
		}
	}
}