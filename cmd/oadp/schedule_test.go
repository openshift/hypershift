package oadp

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/log"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestCreateScheduleOptionsDefaults verifies that the default values for CreateOptions
// are set correctly through the flag system in NewCreateScheduleCommand.
func TestCreateScheduleOptionsDefaults(t *testing.T) {
	// Create the command which should set up default values via flags
	cmd := NewCreateScheduleCommand()

	// Parse args with required flags to trigger default values
	cmd.SetArgs([]string{"--hc-name", "test", "--hc-namespace", "test", "--schedule", "0 2 * * *"})
	err := cmd.ParseFlags([]string{"--hc-name", "test", "--hc-namespace", "test", "--schedule", "0 2 * * *"})
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

	if snapshotMoveData {
		t.Errorf("Expected default SnapshotMoveData to be false")
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

// TestGenerateScheduleObject validates the basic structure and metadata of generated schedule objects.
// This test focuses on the fundamental properties like APIVersion, Kind, ObjectMeta, and template settings.
// It serves as a structural validation test for the core schedule object generation functionality.
func TestGenerateScheduleObject(t *testing.T) {
	opts := &CreateOptions{
		HCName:                   "test-cluster",
		HCNamespace:              "test-cluster-ns",
		OADPNamespace:            "openshift-adp",
		StorageLocation:          "default",
		TTL:                      2 * time.Hour,
		SnapshotMoveData:         false,
		DefaultVolumesToFsBackup: false,
		Schedule:                 "0 2 * * *",
		Paused:                   false,
		UseOwnerReferences:       false,
		SkipImmediately:          false,
	}

	schedule, _, err := opts.GenerateScheduleObject("AWS")
	if err != nil {
		t.Errorf("GenerateScheduleObject() error = %v", err)
		return
	}

	// Check schedule name format - should be auto-generated since no custom name was provided
	// Format should be: {hcName}-{hcNamespace}-{randomSuffix}
	scheduleName := schedule.GetName()
	expectedPattern := "test-cluster-test-cluster-ns-"
	if !strings.HasPrefix(scheduleName, expectedPattern) {
		t.Errorf("Expected schedule name to start with '%s', got '%s'", expectedPattern, scheduleName)
	}
	// Check that the name has the random suffix (should be 6 characters)
	if len(scheduleName) != len(expectedPattern)+6 {
		t.Errorf("Expected schedule name length to be %d, got %d", len(expectedPattern)+6, len(scheduleName))
	}

	// Check schedule object structure
	if schedule.GetAPIVersion() != "velero.io/v1" {
		t.Errorf("Expected API version 'velero.io/v1', got %s", schedule.GetAPIVersion())
	}

	if schedule.GetKind() != "Schedule" {
		t.Errorf("Expected kind 'Schedule', got %s", schedule.GetKind())
	}

	if schedule.GetNamespace() != opts.OADPNamespace {
		t.Errorf("Expected schedule namespace %s, got %s", opts.OADPNamespace, schedule.GetNamespace())
	}

	// Check schedule spec fields
	cronSchedule, found, err := unstructured.NestedString(schedule.Object, "spec", "schedule")
	if err != nil || !found {
		t.Errorf("Expected to find spec.schedule field in schedule object")
	} else if cronSchedule != opts.Schedule {
		t.Errorf("Expected schedule to be '%s', got %s", opts.Schedule, cronSchedule)
	}

	paused, found, err := unstructured.NestedBool(schedule.Object, "spec", "paused")
	if err != nil || !found {
		t.Errorf("Expected to find spec.paused field in schedule object")
	} else if paused != opts.Paused {
		t.Errorf("Expected paused to be %v, got %v", opts.Paused, paused)
	}

	useOwnerReferences, found, err := unstructured.NestedBool(schedule.Object, "spec", "useOwnerReferencesInBackup")
	if err != nil || !found {
		t.Errorf("Expected to find spec.useOwnerReferencesInBackup field in schedule object")
	} else if useOwnerReferences != opts.UseOwnerReferences {
		t.Errorf("Expected useOwnerReferencesInBackup to be %v, got %v", opts.UseOwnerReferences, useOwnerReferences)
	}

	skipImmediately, found, err := unstructured.NestedBool(schedule.Object, "spec", "skipImmediately")
	if err != nil || !found {
		t.Errorf("Expected to find spec.skipImmediately field in schedule object")
	} else if skipImmediately != opts.SkipImmediately {
		t.Errorf("Expected skipImmediately to be %v, got %v", opts.SkipImmediately, skipImmediately)
	}

	// Check included namespaces in template
	expectedNamespaces := []string{opts.HCNamespace, fmt.Sprintf("%s-%s", opts.HCNamespace, opts.HCName)}
	namespacesInterface, found, err := unstructured.NestedFieldNoCopy(schedule.Object, "spec", "template", "includedNamespaces")
	if err != nil || !found {
		t.Errorf("Expected to find spec.template.includedNamespaces field in schedule object")
		return
	}

	// Convert to []string for comparison
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

// TestGenerateScheduleObjectComprehensive provides comprehensive testing of schedule object generation
// across multiple scenarios including:
// - Custom resource selection (user-defined IncludedResources)
// - Default resource selection with platform-specific resources
// - Multi-platform support (AWS, Agent, KubeVirt, OpenStack, Azure)
// - Schedule-specific options (paused, useOwnerReferences, skipImmediately)
func TestGenerateScheduleObjectComprehensive(t *testing.T) {
	type testCase struct {
		name                     string
		platform                 string
		includedResources        []string
		useEtcdSnapshot          bool
		paused                   bool
		useOwnerReferences       bool
		skipImmediately          bool
		schedule                 string
		expectedMinResources     int
		expectedBaseResources    []string
		expectedPlatformSpecific []string
		expectedAbsentResources  []string // resources that must NOT be in the list
		customResourcesExact     bool     // if true, expect exact match for includedResources
	}

	// Use global platform resource mappings from types.go
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
		// Test cases for custom resources and schedule options
		{
			name:                  "When includedResources and schedule options are set it should use custom resources and respect options",
			platform:              "AWS",
			includedResources:     []string{"configmaps", "secrets", "pods"},
			paused:                true,
			useOwnerReferences:    true,
			skipImmediately:       true,
			schedule:              "0 1 * * 0", // Weekly on Sunday at 1 AM
			expectedMinResources:  3,
			expectedBaseResources: []string{"configmaps", "secrets", "pods"},
			customResourcesExact:  true,
		},
		{
			name:                  "When includedResources is set for KubeVirt daily schedule it should use custom resources",
			platform:              "KUBEVIRT",
			includedResources:     []string{"hostedclusters.hypershift.openshift.io", "nodepools.hypershift.openshift.io", "secrets"},
			paused:                false,
			useOwnerReferences:    false,
			skipImmediately:       false,
			schedule:              "0 2 * * *", // Daily at 2 AM
			expectedMinResources:  3,
			expectedBaseResources: []string{"hostedclusters.hypershift.openshift.io", "nodepools.hypershift.openshift.io", "secrets"},
			customResourcesExact:  true,
		},
		// Test cases for default resources with different platforms and schedules
		{
			name:                     "When using default mode for AWS with hourly schedule it should include base and platform resources",
			platform:                 "AWS",
			includedResources:        nil,
			paused:                   false,
			useOwnerReferences:       false,
			skipImmediately:          false,
			schedule:                 "0 * * * *", // Every hour
			expectedMinResources:     10,
			expectedBaseResources:    expectedBaseResources,
			expectedPlatformSpecific: testPlatformResources["AWS"],
			expectedAbsentResources:  []string{"namespaces"},
			customResourcesExact:     false,
		},
		{
			name:                     "When using default mode for Agent with weekly schedule it should include base and platform resources",
			platform:                 "AGENT",
			includedResources:        nil,
			paused:                   true, // Start paused
			useOwnerReferences:       true,
			skipImmediately:          true,
			schedule:                 "0 3 * * 0", // Weekly on Sunday at 3 AM
			expectedMinResources:     10,
			expectedBaseResources:    expectedBaseResources,
			expectedPlatformSpecific: testPlatformResources["AGENT"],
			expectedAbsentResources:  []string{"namespaces"},
			customResourcesExact:     false,
		},
		{
			name:                     "When using default mode for Azure with monthly schedule it should include base and platform resources",
			platform:                 "AZURE",
			includedResources:        nil,
			paused:                   false,
			useOwnerReferences:       false,
			skipImmediately:          true,
			schedule:                 "0 2 1 * *", // Monthly on the 1st at 2 AM
			expectedMinResources:     10,
			expectedBaseResources:    expectedBaseResources,
			expectedPlatformSpecific: testPlatformResources["AZURE"],
			expectedAbsentResources:  []string{"namespaces"},
			customResourcesExact:     false,
		},
		// Etcd snapshot mode test cases
		{
			name:                     "When UseEtcdSnapshot is true it should exclude PV resources and add namespaces for AWS schedule",
			platform:                 "AWS",
			useEtcdSnapshot:          true,
			includedResources:        nil,
			schedule:                 "0 2 * * *",
			expectedMinResources:     10,
			expectedBaseResources:    []string{"serviceaccounts", "hostedclusters.hypershift.openshift.io", "nodepools.hypershift.openshift.io", "namespaces", "secrets"},
			expectedPlatformSpecific: testPlatformResources["AWS"],
			expectedAbsentResources:  []string{"persistentvolumeclaims", "persistentvolumes", "deployments", "statefulsets"},
			customResourcesExact:     false,
		},
		{
			name:                     "When UseEtcdSnapshot is true it should exclude PV resources and add namespaces for Azure schedule",
			platform:                 "AZURE",
			useEtcdSnapshot:          true,
			includedResources:        nil,
			schedule:                 "0 3 * * 0",
			expectedMinResources:     10,
			expectedBaseResources:    []string{"serviceaccounts", "hostedclusters.hypershift.openshift.io", "nodepools.hypershift.openshift.io", "namespaces", "secrets"},
			expectedPlatformSpecific: testPlatformResources["AZURE"],
			expectedAbsentResources:  []string{"persistentvolumeclaims", "persistentvolumes", "deployments", "statefulsets"},
			customResourcesExact:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			opts := &CreateOptions{
				HCName:                   "test-cluster",
				HCNamespace:              "test-cluster-ns",
				OADPNamespace:            "openshift-adp",
				StorageLocation:          "default",
				TTL:                      2 * time.Hour,
				SnapshotMoveData:         false,
				DefaultVolumesToFsBackup: false,
				IncludedResources:        tt.includedResources,
				UseEtcdSnapshot:          tt.useEtcdSnapshot,
				Schedule:                 tt.schedule,
				Paused:                   tt.paused,
				UseOwnerReferences:       tt.useOwnerReferences,
				SkipImmediately:          tt.skipImmediately,
			}

			schedule, _, err := opts.GenerateScheduleObject(tt.platform)
			g.Expect(err).NotTo(HaveOccurred())

			// Basic validation
			g.Expect(schedule.GetName()).NotTo(BeEmpty())
			g.Expect(schedule.GetAPIVersion()).To(Equal("velero.io/v1"))
			g.Expect(schedule.GetKind()).To(Equal("Schedule"))

			// Schedule-specific fields
			cronSchedule, found, _ := unstructured.NestedString(schedule.Object, "spec", "schedule")
			g.Expect(found).To(BeTrue())
			g.Expect(cronSchedule).To(Equal(tt.schedule))

			paused, found, _ := unstructured.NestedBool(schedule.Object, "spec", "paused")
			g.Expect(found).To(BeTrue())
			g.Expect(paused).To(Equal(tt.paused))

			useOwnerReferences, found, _ := unstructured.NestedBool(schedule.Object, "spec", "useOwnerReferencesInBackup")
			g.Expect(found).To(BeTrue())
			g.Expect(useOwnerReferences).To(Equal(tt.useOwnerReferences))

			skipImmediately, found, _ := unstructured.NestedBool(schedule.Object, "spec", "skipImmediately")
			g.Expect(found).To(BeTrue())
			g.Expect(skipImmediately).To(Equal(tt.skipImmediately))

			// Etcd snapshot mode template spec validations
			if tt.useEtcdSnapshot {
				_, dmFound, _ := unstructured.NestedString(schedule.Object, "spec", "template", "dataMover")
				g.Expect(dmFound).To(BeFalse(), "dataMover should not be present in etcd snapshot mode")

				sv, svFound, _ := unstructured.NestedBool(schedule.Object, "spec", "template", "snapshotVolumes")
				g.Expect(svFound).To(BeTrue(), "snapshotVolumes should be present in etcd snapshot mode")
				g.Expect(sv).To(BeFalse(), "snapshotVolumes should be false in etcd snapshot mode")

				smd, smdFound, _ := unstructured.NestedBool(schedule.Object, "spec", "template", "snapshotMoveData")
				g.Expect(smdFound).To(BeTrue(), "snapshotMoveData should be present in etcd snapshot mode")
				g.Expect(smd).To(BeFalse(), "snapshotMoveData should be false in etcd snapshot mode")

				iot, iotFound, _ := unstructured.NestedString(schedule.Object, "spec", "template", "itemOperationTimeout")
				g.Expect(iotFound).To(BeTrue(), "itemOperationTimeout should be present")
				g.Expect(iot).To(Equal("4h0m0s"))
			} else if !tt.customResourcesExact {
				dm, dmFound, _ := unstructured.NestedString(schedule.Object, "spec", "template", "dataMover")
				g.Expect(dmFound).To(BeTrue(), "dataMover should be present in default mode")
				g.Expect(dm).To(Equal("velero"))

				sv, _, _ := unstructured.NestedBool(schedule.Object, "spec", "template", "snapshotVolumes")
				g.Expect(sv).To(BeTrue(), "snapshotVolumes should be true in default mode")
			}

			// Get included resources from spec template
			includedResourcesInterface, found, _ := unstructured.NestedFieldNoCopy(schedule.Object, "spec", "template", "includedResources")
			g.Expect(found).To(BeTrue(), "includedResources should be present")

			var includedResources []string
			if resourcesSlice, ok := includedResourcesInterface.([]string); ok {
				includedResources = resourcesSlice
			} else if resourcesInterfaceSlice, ok := includedResourcesInterface.([]interface{}); ok {
				for _, res := range resourcesInterfaceSlice {
					includedResources = append(includedResources, res.(string))
				}
			}

			g.Expect(len(includedResources)).To(BeNumerically(">=", tt.expectedMinResources))

			// For custom resources, check exact match
			if tt.customResourcesExact {
				g.Expect(includedResources).To(HaveLen(len(tt.expectedBaseResources)))
				for i, expected := range tt.expectedBaseResources {
					if i < len(includedResources) {
						g.Expect(includedResources[i]).To(Equal(expected))
					}
				}
				return
			}

			// For default resources, check contains
			for _, expected := range tt.expectedBaseResources {
				g.Expect(includedResources).To(ContainElement(expected), "should contain base resource '%s'", expected)
			}
			for _, expected := range tt.expectedPlatformSpecific {
				g.Expect(includedResources).To(ContainElement(expected), "should contain platform resource '%s'", expected)
			}

			// Verify resources that must NOT be present
			for _, absent := range tt.expectedAbsentResources {
				g.Expect(includedResources).NotTo(ContainElement(absent), "should NOT contain resource '%s'", absent)
			}
		})
	}
}

// TestGenerateScheduleObjectWithKubeVirtResourcePolicy verifies that for KubeVirt platform,
// the schedule template contains a resourcePolicy referencing a ConfigMap, and a ConfigMap is returned.
// For non-KubeVirt platforms, no resourcePolicy should be set and no ConfigMap should be returned.
func TestGenerateScheduleObjectWithKubeVirtResourcePolicy(t *testing.T) {
	tests := []struct {
		name                 string
		platform             string
		expectResourcePolicy bool
	}{
		{
			name:                 "When platform is KubeVirt it should have resourcePolicy in template",
			platform:             "KUBEVIRT",
			expectResourcePolicy: true,
		},
		{
			name:                 "When platform is lowercase kubevirt it should have resourcePolicy in template",
			platform:             "kubevirt",
			expectResourcePolicy: true,
		},
		{
			name:                 "When platform is AWS it should not have resourcePolicy in template",
			platform:             "AWS",
			expectResourcePolicy: false,
		},
		{
			name:                 "When platform is AGENT it should not have resourcePolicy in template",
			platform:             "AGENT",
			expectResourcePolicy: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &CreateOptions{
				HCName:          "test-cluster",
				HCNamespace:     "test-ns",
				OADPNamespace:   "openshift-adp",
				StorageLocation: "default",
				TTL:             2 * time.Hour,
				Schedule:        "0 2 * * *",
			}

			schedule, resourcePolicyCM, err := opts.GenerateScheduleObject(tt.platform)
			if err != nil {
				t.Fatalf("GenerateScheduleObject() failed: %v", err)
			}

			// Check resourcePolicy in schedule spec.template
			rpInterface, rpFound, _ := unstructured.NestedFieldNoCopy(schedule.Object, "spec", "template", "resourcePolicy")

			if tt.expectResourcePolicy {
				if !rpFound {
					t.Fatal("Expected spec.template.resourcePolicy to be set for KubeVirt platform")
				}
				rpMap, ok := rpInterface.(map[string]interface{})
				if !ok {
					t.Fatalf("Expected resourcePolicy to be a map, got %T", rpInterface)
				}
				if rpMap["kind"] != "configmap" {
					t.Errorf("Expected resourcePolicy.kind to be 'configmap', got %v", rpMap["kind"])
				}
				if rpMap["name"] == nil || rpMap["name"] == "" {
					t.Error("Expected resourcePolicy.name to be set")
				}

				// Verify ConfigMap is returned
				if resourcePolicyCM == nil {
					t.Fatal("Expected resource policy ConfigMap to be returned for KubeVirt platform")
				}
				if resourcePolicyCM.GetKind() != "ConfigMap" {
					t.Errorf("Expected ConfigMap kind, got %s", resourcePolicyCM.GetKind())
				}
				// Verify the ConfigMap name matches the schedule template's resourcePolicy name
				if rpMap["name"] != resourcePolicyCM.GetName() {
					t.Errorf("Expected resourcePolicy.name '%v' to match ConfigMap name '%s'", rpMap["name"], resourcePolicyCM.GetName())
				}
			} else {
				if rpFound {
					t.Errorf("Expected no resourcePolicy for platform %s, but found %v", tt.platform, rpInterface)
				}
				if resourcePolicyCM != nil {
					t.Errorf("Expected no ConfigMap for platform %s, but got one", tt.platform)
				}
			}
		})
	}
}

// TestValidateSchedulePace verifies that the cron schedule validation works correctly.
// This test ensures that:
// - Valid cron expressions are accepted
// - Invalid cron expressions are rejected with appropriate error messages
// - Empty schedule expressions are rejected
func TestValidateSchedulePace(t *testing.T) {
	tests := []struct {
		name      string
		schedule  string
		expectErr bool
		errMsg    string
	}{
		// Valid cron expressions
		{
			name:      "Valid daily schedule",
			schedule:  "0 2 * * *",
			expectErr: false,
		},
		{
			name:      "Valid weekly schedule",
			schedule:  "0 1 * * 0",
			expectErr: false,
		},
		{
			name:      "Valid monthly schedule",
			schedule:  "0 3 1 * *",
			expectErr: false,
		},
		{
			name:      "Valid hourly schedule",
			schedule:  "0 * * * *",
			expectErr: false,
		},
		{
			name:      "Valid specific weekday schedule",
			schedule:  "30 14 * * 1-5", // Monday to Friday at 2:30 PM
			expectErr: false,
		},
		// Valid Velero verb schedules
		{
			name:      "Valid daily verb",
			schedule:  "daily",
			expectErr: false,
		},
		{
			name:      "Valid weekly verb",
			schedule:  "weekly",
			expectErr: false,
		},
		{
			name:      "Valid monthly verb",
			schedule:  "monthly",
			expectErr: false,
		},
		{
			name:      "Valid @daily verb",
			schedule:  "@daily",
			expectErr: false,
		},
		{
			name:      "Valid @weekly verb",
			schedule:  "@weekly",
			expectErr: false,
		},
		{
			name:      "Valid @monthly verb",
			schedule:  "@monthly",
			expectErr: false,
		},
		{
			name:      "Valid yearly verb",
			schedule:  "yearly",
			expectErr: false,
		},
		{
			name:      "Valid hourly verb",
			schedule:  "hourly",
			expectErr: false,
		},
		{
			name:      "Valid daily-2am verb",
			schedule:  "daily-2am",
			expectErr: false,
		},
		{
			name:      "Valid weekly-friday verb",
			schedule:  "weekly-friday",
			expectErr: false,
		},
		{
			name:      "Valid case-insensitive DAILY",
			schedule:  "DAILY",
			expectErr: false,
		},
		{
			name:      "Valid case-insensitive @Weekly",
			schedule:  "@Weekly",
			expectErr: false,
		},
		// Invalid cron expressions
		{
			name:      "Empty schedule",
			schedule:  "",
			expectErr: true,
			errMsg:    "schedule expression is required",
		},
		{
			name:      "Too few fields",
			schedule:  "0 2 *",
			expectErr: true,
			errMsg:    "invalid cron schedule",
		},
		{
			name:      "Too many fields",
			schedule:  "0 2 * * * *",
			expectErr: true,
			errMsg:    "invalid cron schedule",
		},
		{
			name:      "Too few fields with spaces",
			schedule:  "0  * * *",
			expectErr: true,
			errMsg:    "invalid cron schedule",
		},
		{
			name:      "Too few fields with trailing space",
			schedule:  "0 2 * * ",
			expectErr: true,
			errMsg:    "invalid cron schedule",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &CreateOptions{
				Schedule: tt.schedule,
			}

			err := opts.ValidateSchedulePace()

			if tt.expectErr {
				if err == nil {
					t.Errorf("Expected error for schedule '%s', but got none", tt.schedule)
					return
				}
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("Expected error message to contain '%s', got '%s'", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error for schedule '%s', but got: %v", tt.schedule, err)
				}
			}
		})
	}
}

// TestNormalizeScheduleExpression verifies that schedule verb normalization works correctly.
// This test ensures all supported Velero schedule verbs are properly converted to cron expressions.
func TestNormalizeScheduleExpression(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Standard @ verbs (commonly used in Velero)
		{"@yearly verb", "@yearly", "0 0 1 1 *"},
		{"@annually verb", "@annually", "0 0 1 1 *"},
		{"@monthly verb", "@monthly", "0 0 1 * *"},
		{"@weekly verb", "@weekly", "0 0 * * 0"},
		{"@daily verb", "@daily", "0 0 * * *"},
		{"@midnight verb", "@midnight", "0 0 * * *"},
		{"@hourly verb", "@hourly", "0 * * * *"},

		// Simple verbs (user-friendly)
		{"yearly verb", "yearly", "0 0 1 1 *"},
		{"annually verb", "annually", "0 0 1 1 *"},
		{"monthly verb", "monthly", "0 0 1 * *"},
		{"weekly verb", "weekly", "0 0 * * 0"},
		{"daily verb", "daily", "0 0 * * *"},
		{"hourly verb", "hourly", "0 * * * *"},

		// Alternative daily schedules with different times
		{"daily-1am verb", "daily-1am", "0 1 * * *"},
		{"daily-2am verb", "daily-2am", "0 2 * * *"},
		{"daily-3am verb", "daily-3am", "0 3 * * *"},
		{"daily-6am verb", "daily-6am", "0 6 * * *"},
		{"daily-noon verb", "daily-noon", "0 12 * * *"},

		// Alternative weekly schedules
		{"weekly-sunday verb", "weekly-sunday", "0 0 * * 0"},
		{"weekly-monday verb", "weekly-monday", "0 0 * * 1"},
		{"weekly-friday verb", "weekly-friday", "0 0 * * 5"},
		{"weekly-saturday verb", "weekly-saturday", "0 0 * * 6"},

		// Case insensitive tests
		{"DAILY uppercase", "DAILY", "0 0 * * *"},
		{"Weekly mixed case", "Weekly", "0 0 * * 0"},
		{"@MONTHLY uppercase", "@MONTHLY", "0 0 1 * *"},
		{"DAILY-2AM mixed case", "Daily-2AM", "0 2 * * *"},

		// Whitespace handling
		{"daily with spaces", " daily ", "0 0 * * *"},
		{"@weekly with spaces", "  @weekly  ", "0 0 * * 0"},

		// Non-verb expressions (should pass through unchanged)
		{"cron expression", "0 2 * * *", "0 2 * * *"},
		{"complex cron", "30 14 * * 1-5", "30 14 * * 1-5"},
		{"custom expression", "15 */2 * * *", "15 */2 * * *"},
		{"unknown verb", "unknown", "unknown"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := normalizeScheduleExpression(tt.input)
			if actual != tt.expected {
				t.Errorf("normalizeScheduleExpression(%q) = %q, expected %q", tt.input, actual, tt.expected)
			}
		})
	}
}

// TestGenerateScheduleObjectWithIncludedNamespaces verifies that the --include-additional-namespaces flag
// works correctly in schedule generation, adding additional namespaces to the default HC and HCP namespaces.
func TestGenerateScheduleObjectWithIncludedNamespaces(t *testing.T) {
	tests := []struct {
		name               string
		hcName             string
		hcNamespace        string
		includeNamespaces  []string
		expectedNamespaces []string
	}{
		{
			name:               "Default namespaces when none specified",
			hcName:             "test-cluster",
			hcNamespace:        "clusters-ns",
			includeNamespaces:  nil,
			expectedNamespaces: []string{"clusters-ns", "clusters-ns-test-cluster"},
		},
		{
			name:               "Default namespaces when empty slice specified",
			hcName:             "prod-cluster",
			hcNamespace:        "production",
			includeNamespaces:  []string{},
			expectedNamespaces: []string{"production", "production-prod-cluster"},
		},
		{
			name:               "Additional namespaces added to defaults",
			hcName:             "dev-cluster",
			hcNamespace:        "development",
			includeNamespaces:  []string{"custom-ns1", "custom-ns2"},
			expectedNamespaces: []string{"development", "development-dev-cluster", "custom-ns1", "custom-ns2"},
		},
		{
			name:               "Single additional namespace",
			hcName:             "test-cluster",
			hcNamespace:        "test",
			includeNamespaces:  []string{"only-namespace"},
			expectedNamespaces: []string{"test", "test-test-cluster", "only-namespace"},
		},
		{
			name:               "Multiple additional namespaces",
			hcName:             "multi-cluster",
			hcNamespace:        "multi",
			includeNamespaces:  []string{"ns1", "ns2", "ns3", "ns4"},
			expectedNamespaces: []string{"multi", "multi-multi-cluster", "ns1", "ns2", "ns3", "ns4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &CreateOptions{
				HCName:            tt.hcName,
				HCNamespace:       tt.hcNamespace,
				ScheduleName:      "test-schedule",
				Schedule:          "0 2 * * *", // Daily at 2 AM
				OADPNamespace:     "openshift-adp",
				StorageLocation:   "default",
				TTL:               2 * time.Hour,
				IncludeNamespaces: tt.includeNamespaces,
			}

			schedule, _, err := opts.GenerateScheduleObject("AWS")
			if err != nil {
				t.Fatalf("GenerateScheduleObject() failed: %v", err)
			}

			// Extract included namespaces from the generated schedule
			includedNamespaces, found, err := unstructured.NestedStringSlice(schedule.Object, "spec", "template", "includedNamespaces")
			if err != nil {
				t.Errorf("Failed to get includedNamespaces: %v", err)
			}
			if !found {
				t.Error("includedNamespaces not found in schedule spec")
			}

			// Verify the namespaces match expected
			if len(includedNamespaces) != len(tt.expectedNamespaces) {
				t.Errorf("Expected %d namespaces, got %d", len(tt.expectedNamespaces), len(includedNamespaces))
				return
			}

			for i, expected := range tt.expectedNamespaces {
				if i >= len(includedNamespaces) || includedNamespaces[i] != expected {
					t.Errorf("Expected namespace %d to be '%s', got '%s'", i, expected, includedNamespaces[i])
				}
			}
		})
	}
}

// TestScheduleCommandIncludedNamespacesFlag verifies that the --include-additional-namespaces flag
// is properly configured in the CLI command and accessible for testing.
func TestScheduleCommandIncludedNamespacesFlag(t *testing.T) {
	cmd := NewCreateScheduleCommand()

	// Test that the flag exists and has proper configuration
	flag := cmd.Flags().Lookup("include-additional-namespaces")
	if flag == nil {
		t.Fatal("--include-additional-namespaces flag not found")
	}

	// Test flag default value (should be nil/empty)
	defaultValue, err := cmd.Flags().GetStringSlice("include-additional-namespaces")
	if err != nil {
		t.Fatalf("Failed to get default value: %v", err)
	}
	if len(defaultValue) > 0 {
		t.Errorf("Expected default value to be nil/empty, got %v", defaultValue)
	}

	// Test setting the flag value
	testNamespaces := []string{"ns1", "ns2", "ns3"}
	args := append([]string{"--hc-name", "test", "--hc-namespace", "test", "--schedule", "daily"},
		"--include-additional-namespaces", strings.Join(testNamespaces, ","))
	cmd.SetArgs(args)

	err = cmd.ParseFlags(args)
	if err != nil {
		t.Fatalf("Failed to parse flags: %v", err)
	}

	// Verify the parsed value
	parsedNamespaces, err := cmd.Flags().GetStringSlice("include-additional-namespaces")
	if err != nil {
		t.Fatalf("Failed to get parsed value: %v", err)
	}

	if len(parsedNamespaces) != len(testNamespaces) {
		t.Errorf("Expected %d namespaces, got %d", len(testNamespaces), len(parsedNamespaces))
		return
	}

	for i, expected := range testNamespaces {
		if parsedNamespaces[i] != expected {
			t.Errorf("Expected namespace %d to be '%s', got '%s'", i, expected, parsedNamespaces[i])
		}
	}
}

// TestBuildIncludedNamespacesForSchedule verifies that the buildIncludedNamespaces function
// works correctly when called from schedule generation context.
func TestBuildIncludedNamespacesForSchedule(t *testing.T) {
	// This test specifically verifies that the common function is used correctly in schedule context
	tests := []struct {
		name               string
		hcNamespace        string
		hcName             string
		customNamespaces   []string
		expectedNamespaces []string
	}{
		{
			name:               "Schedule default namespaces",
			hcNamespace:        "schedule-ns",
			hcName:             "schedule-cluster",
			customNamespaces:   nil,
			expectedNamespaces: []string{"schedule-ns", "schedule-ns-schedule-cluster"},
		},
		{
			name:               "Schedule additional namespaces",
			hcNamespace:        "schedule-ns",
			hcName:             "schedule-cluster",
			customNamespaces:   []string{"custom-schedule-ns1", "custom-schedule-ns2"},
			expectedNamespaces: []string{"schedule-ns", "schedule-ns-schedule-cluster", "custom-schedule-ns1", "custom-schedule-ns2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildIncludedNamespaces(tt.hcNamespace, tt.hcName, tt.customNamespaces)

			if len(result) != len(tt.expectedNamespaces) {
				t.Errorf("Expected %d namespaces, got %d", len(tt.expectedNamespaces), len(result))
				return
			}

			for i, expected := range tt.expectedNamespaces {
				if result[i] != expected {
					t.Errorf("Expected namespace %d to be '%s', got '%s'", i, expected, result[i])
				}
			}
		})
	}
}

// TestRunSchedule verifies RunSchedule behavior across different client
// availability and render/non-render scenarios.
func TestRunSchedule(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(t *testing.T, opts *CreateOptions)
		expect func(g Gomega, err error)
	}{
		{
			name: "When client is unavailable in render mode it should fallback to AWS and render",
			setup: func(t *testing.T, opts *CreateOptions) {
				t.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
				t.Setenv("KUBERNETES_SERVICE_HOST", "")
				t.Setenv("KUBERNETES_SERVICE_PORT", "")
				opts.Render = true
			},
			expect: func(g Gomega, err error) {
				g.Expect(err).NotTo(HaveOccurred())
			},
		},
		{
			name: "When client is available in render mode it should render with validation warnings",
			setup: func(t *testing.T, opts *CreateOptions) {
				opts.Render = true
				scheme := runtime.NewScheme()
				opts.Client = fake.NewClientBuilder().WithScheme(scheme).Build()
			},
			expect: func(g Gomega, err error) {
				g.Expect(err).NotTo(HaveOccurred())
			},
		},
		{
			name: "When client has valid resources in non-render mode it should create the schedule",
			setup: func(t *testing.T, opts *CreateOptions) {
				opts.Render = false

				scheme := runtime.NewScheme()
				_ = hypershiftv1beta1.AddToScheme(scheme)
				_ = appsv1.AddToScheme(scheme)

				hc := &hypershiftv1beta1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cluster",
						Namespace: "test-ns",
					},
					Spec: hypershiftv1beta1.HostedClusterSpec{
						Platform: hypershiftv1beta1.PlatformSpec{
							Type: hypershiftv1beta1.AWSPlatform,
						},
					},
				}

				oadpDeploy := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "openshift-adp-controller-manager",
						Namespace: "openshift-adp",
					},
					Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
				}

				veleroDeploy := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "velero",
						Namespace: "openshift-adp",
					},
					Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
				}

				dpa := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "oadp.openshift.io/v1alpha1",
						"kind":       "DataProtectionApplication",
						"metadata": map[string]interface{}{
							"name":      "test-dpa",
							"namespace": "openshift-adp",
						},
						"status": map[string]interface{}{
							"conditions": []interface{}{
								map[string]interface{}{
									"type":   "Reconciled",
									"status": "True",
								},
							},
						},
					},
				}
				dpa.SetGroupVersionKind(schema.GroupVersionKind{
					Group:   "oadp.openshift.io",
					Version: "v1alpha1",
					Kind:    "DataProtectionApplication",
				})

				opts.Client = fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(hc, oadpDeploy, veleroDeploy, dpa).
					Build()
			},
			expect: func(g Gomega, err error) {
				g.Expect(err).NotTo(HaveOccurred())
			},
		},
		{
			name: "When client is unavailable in non-render mode it should return an error",
			setup: func(t *testing.T, opts *CreateOptions) {
				t.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
				t.Setenv("KUBERNETES_SERVICE_HOST", "")
				t.Setenv("KUBERNETES_SERVICE_PORT", "")
				opts.Render = false
			},
			expect: func(g Gomega, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("failed to create kubernetes client"))
			},
		},
		{
			name: "When HostedCluster is not found in non-render mode it should return platform detection error",
			setup: func(t *testing.T, opts *CreateOptions) {
				opts.Render = false
				scheme := runtime.NewScheme()
				_ = hypershiftv1beta1.AddToScheme(scheme)
				opts.Client = fake.NewClientBuilder().WithScheme(scheme).Build()
			},
			expect: func(g Gomega, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("platform detection failed"))
			},
		},
		{
			name: "When OADP components are missing in non-render mode it should return OADP validation error",
			setup: func(t *testing.T, opts *CreateOptions) {
				opts.Render = false
				scheme := runtime.NewScheme()
				_ = hypershiftv1beta1.AddToScheme(scheme)
				_ = appsv1.AddToScheme(scheme)

				hc := &hypershiftv1beta1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "test-ns"},
					Spec: hypershiftv1beta1.HostedClusterSpec{
						Platform: hypershiftv1beta1.PlatformSpec{Type: hypershiftv1beta1.AWSPlatform},
					},
				}
				opts.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc).Build()
			},
			expect: func(g Gomega, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("OADP validation failed"))
			},
		},
		{
			name: "When DPA is missing in non-render mode it should return DPA verification error",
			setup: func(t *testing.T, opts *CreateOptions) {
				opts.Render = false
				scheme := runtime.NewScheme()
				_ = hypershiftv1beta1.AddToScheme(scheme)
				_ = appsv1.AddToScheme(scheme)

				hc := &hypershiftv1beta1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "test-ns"},
					Spec: hypershiftv1beta1.HostedClusterSpec{
						Platform: hypershiftv1beta1.PlatformSpec{Type: hypershiftv1beta1.AWSPlatform},
					},
				}
				oadpDeploy := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{Name: "openshift-adp-controller-manager", Namespace: "openshift-adp"},
					Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
				}
				veleroDeploy := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{Name: "velero", Namespace: "openshift-adp"},
					Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
				}
				opts.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc, oadpDeploy, veleroDeploy).Build()
			},
			expect: func(g Gomega, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("DPA verification failed"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			opts := &CreateOptions{
				HCName:          "test-cluster",
				HCNamespace:     "test-ns",
				OADPNamespace:   "openshift-adp",
				StorageLocation: "default",
				TTL:             2 * time.Hour,
				Schedule:        "0 2 * * *",
				Log:             log.Log,
			}

			tt.setup(t, opts)
			err := opts.RunSchedule(context.Background())
			tt.expect(g, err)
		})
	}
}

// TestScheduleNameValidation verifies that schedule name validation works correctly
// for custom names (--name flag), including 63-character limit and Kubernetes naming rules.
func TestScheduleNameValidation(t *testing.T) {
	tests := []struct {
		name         string
		scheduleName string
		expectError  bool
		errorMsg     string
	}{
		{
			name:         "Valid short name",
			scheduleName: "test-schedule",
			expectError:  false,
		},
		{
			name:         "Valid name with numbers",
			scheduleName: "test-schedule-123",
			expectError:  false,
		},
		{
			name:         "Valid 63 character name",
			scheduleName: "a1234567890123456789012345678901234567890123456789012345678901b",
			expectError:  false,
		},
		{
			name:         "Name too long (64 characters)",
			scheduleName: "a12345678901234567890123456789012345678901234567890123456789012b",
			expectError:  true,
			errorMsg:     "too long (64 characters)",
		},
		{
			name:         "Name with uppercase letters",
			scheduleName: "Test-schedule",
			expectError:  true,
			errorMsg:     "a lowercase RFC 1123 subdomain must consist of lower case alphanumeric characters",
		},
		{
			name:         "Name starting with hyphen",
			scheduleName: "-test-schedule",
			expectError:  true,
			errorMsg:     "a lowercase RFC 1123 subdomain must consist of lower case alphanumeric characters",
		},
		{
			name:         "Name ending with hyphen",
			scheduleName: "test-schedule-",
			expectError:  true,
			errorMsg:     "a lowercase RFC 1123 subdomain must consist of lower case alphanumeric characters",
		},
		{
			name:         "Name with invalid characters",
			scheduleName: "test_schedule",
			expectError:  true,
			errorMsg:     "a lowercase RFC 1123 subdomain must consist of lower case alphanumeric characters",
		},
		{
			name:         "Empty name should be valid",
			scheduleName: "",
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test custom name validation
			opts := &CreateOptions{
				ScheduleName: tt.scheduleName,
			}

			err := opts.ValidateScheduleName()

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error for schedule name '%s', but got none", tt.scheduleName)
					return
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error message to contain '%s', got '%s'", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error for schedule name '%s', but got: %v", tt.scheduleName, err)
				}
			}
		})
	}
}
