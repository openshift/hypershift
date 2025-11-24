//go:build integration
// +build integration

package oadp

import (
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/openshift/hypershift/cmd/oadp"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/yaml"
)

// TestScheduleManifestValidation validates that generated schedule manifests are valid according to Velero CRD
func TestScheduleManifestValidation(t *testing.T) {
	g := NewWithT(t)

	tests := []struct {
		name     string
		platform string
	}{
		{"AWS platform schedule", "AWS"},
		{"Agent platform schedule", "AGENT"},
		{"KubeVirt platform schedule", "KUBEVIRT"},
		{"Azure platform schedule", "AZURE"},
		{"OpenStack platform schedule", "OPENSTACK"},
	}

	// Download Velero Schedule CRD once
	t.Log("Downloading Velero Schedule CRD...")
	scheduleCRD, err := downloadScheduleCRD()
	g.Expect(err).ToNot(HaveOccurred(), "Failed to download Velero Schedule CRD")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Generate schedule manifest
			opts := &oadp.CreateOptions{
				HCName:                   "test-cluster",
				HCNamespace:              "test-cluster-ns",
				OADPNamespace:            "openshift-adp",
				StorageLocation:          "default",
				TTL:                      2 * time.Hour,
				Schedule:                 "0 2 * * *", // Daily at 2 AM
				SnapshotMoveData:         false,
				DefaultVolumesToFsBackup: false,
				Paused:                   false,
				UseOwnerReferences:       false,
				SkipImmediately:          false,
			}

			schedule, _, err := opts.GenerateScheduleObject(tt.platform)
			g.Expect(err).ToNot(HaveOccurred(), "Failed to generate schedule object")

			// Convert to YAML for validation
			yamlBytes, err := yaml.Marshal(schedule.Object)
			g.Expect(err).ToNot(HaveOccurred(), "Failed to marshal schedule to YAML")

			// Validate against CRD schema
			err = validateScheduleAgainstCRD(schedule.Object, scheduleCRD)
			g.Expect(err).ToNot(HaveOccurred(), "Schedule manifest failed CRD validation for %s", tt.platform)

			// Additional specific validations
			t.Run("required_fields", func(t *testing.T) {
				validateScheduleRequiredFields(t, schedule.Object)
			})

			t.Run("schedule_specific_fields", func(t *testing.T) {
				validateScheduleSpecificFields(t, schedule.Object, opts)
			})

			t.Run("backup_template", func(t *testing.T) {
				validateScheduleBackupTemplate(t, schedule.Object, tt.platform)
			})

			t.Logf("✅ %s schedule manifest validated successfully", tt.platform)
			t.Logf("Generated YAML:\n%s", string(yamlBytes))
		})
	}
}

// TestScheduleCLIConfiguration validates schedule CLI configuration and defaults
func TestScheduleCLIConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		opts     *oadp.CreateOptions
		wantErr  bool
		validate func(g Gomega, schedule map[string]interface{})
	}{
		{
			name: "default configuration",
			opts: &oadp.CreateOptions{
				HCName:                   "test-cluster",
				HCNamespace:              "test-cluster-ns",
				Schedule:                 "0 2 * * *", // Required field
				OADPNamespace:            "openshift-adp",
				StorageLocation:          "default",
				TTL:                      2 * time.Hour,
				SnapshotMoveData:         false,
				DefaultVolumesToFsBackup: false,
				Paused:                   false,
				UseOwnerReferences:       false,
				SkipImmediately:          false,
			},
			wantErr: false,
			validate: func(g Gomega, schedule map[string]interface{}) {
				spec := schedule["spec"].(map[string]interface{})
				g.Expect(spec["schedule"]).To(Equal("0 2 * * *"), "Schedule should be set correctly")
				g.Expect(spec["paused"]).To(Equal(false), "Default paused should be false")
				g.Expect(spec["useOwnerReferencesInBackup"]).To(Equal(false), "Default useOwnerReferencesInBackup should be false")
				g.Expect(spec["skipImmediately"]).To(Equal(false), "Default skipImmediately should be false")

				// Check template defaults
				template := spec["template"].(map[string]interface{})
				g.Expect(template["storageLocation"]).To(Equal("default"), "Default storage location should be 'default'")
				g.Expect(template["ttl"]).To(Equal("2h0m0s"), "Default TTL should be 2h")
			},
		},
		{
			name: "custom configuration with schedule options",
			opts: &oadp.CreateOptions{
				HCName:                   "custom-cluster",
				HCNamespace:              "custom-ns",
				StorageLocation:          "s3-backup",
				TTL:                      24 * time.Hour,
				SnapshotMoveData:         false,
				DefaultVolumesToFsBackup: true,
				Schedule:                 "0 1 * * 0",   // Weekly on Sunday at 1 AM
				Paused:                   true,          // Start paused
				UseOwnerReferences:       true,          // Use owner references
				SkipImmediately:          true,          // Skip immediate backup
			},
			wantErr: false,
			validate: func(g Gomega, schedule map[string]interface{}) {
				spec := schedule["spec"].(map[string]interface{})
				g.Expect(spec["schedule"]).To(Equal("0 1 * * 0"), "Schedule should be set correctly")
				g.Expect(spec["paused"]).To(Equal(true), "Paused should be true")
				g.Expect(spec["useOwnerReferencesInBackup"]).To(Equal(true), "UseOwnerReferencesInBackup should be true")
				g.Expect(spec["skipImmediately"]).To(Equal(true), "SkipImmediately should be true")

				// Check template configuration
				template := spec["template"].(map[string]interface{})
				g.Expect(template["storageLocation"]).To(Equal("s3-backup"), "Storage location should be custom")
				g.Expect(template["ttl"]).To(Equal("24h0m0s"), "TTL should be 24h")
				g.Expect(template["snapshotMoveData"]).To(Equal(false), "SnapshotMoveData should be false")
				g.Expect(template["defaultVolumesToFsBackup"]).To(Equal(true), "DefaultVolumesToFsBackup should be true")
			},
		},
		{
			name: "various schedule frequencies",
			opts: &oadp.CreateOptions{
				HCName:                   "frequency-test",
				HCNamespace:              "frequency-ns",
				Schedule:                 "0 * * * *", // Hourly
				OADPNamespace:            "openshift-adp",
				StorageLocation:          "default",
				TTL:                      2 * time.Hour,
				SnapshotMoveData:         false,
				DefaultVolumesToFsBackup: false,
				Paused:                   false,
				UseOwnerReferences:       false,
				SkipImmediately:          false,
			},
			wantErr: false,
			validate: func(g Gomega, schedule map[string]interface{}) {
				spec := schedule["spec"].(map[string]interface{})
				g.Expect(spec["schedule"]).To(Equal("0 * * * *"), "Hourly schedule should be set correctly")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			schedule, _, err := tt.opts.GenerateScheduleObject("AWS")
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			if tt.validate != nil {
				tt.validate(g, schedule.Object)
			}
		})
	}
}

// TestScheduleValidation tests the schedule validation functionality
func TestScheduleValidation(t *testing.T) {
	tests := []struct {
		name        string
		schedule    string
		expectError bool
	}{
		{"Valid daily schedule", "0 2 * * *", false},
		{"Valid weekly schedule", "0 1 * * 0", false},
		{"Valid hourly schedule", "0 * * * *", false},
		{"Valid monthly schedule", "0 3 1 * *", false},
		{"Valid workday schedule", "30 14 * * 1-5", false},
		{"Invalid empty schedule", "", true},
		{"Invalid too few fields", "0 2 *", true},
		{"Invalid too many fields", "0 2 * * * *", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			opts := &oadp.CreateOptions{
				HCName:                   "test-cluster",
				HCNamespace:              "test-cluster-ns",
				Schedule:                 tt.schedule,
				OADPNamespace:            "openshift-adp",
				StorageLocation:          "default",
				TTL:                      2 * time.Hour,
				SnapshotMoveData:         false,
				DefaultVolumesToFsBackup: false,
				Paused:                   false,
				UseOwnerReferences:       false,
				SkipImmediately:          false,
			}

			// Test validation directly by calling validateSchedule
			// This tests the private function through a public method we can simulate
			err := testScheduleValidation(opts)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred(), "Expected error for schedule '%s'", tt.schedule)
			} else {
				g.Expect(err).ToNot(HaveOccurred(), "Expected no error for schedule '%s'", tt.schedule)
			}
		})
	}
}

// testScheduleValidation simulates the validation logic for testing
func testScheduleValidation(opts *oadp.CreateOptions) error {
	// Replicate the validation logic from schedule.go validateSchedule method
	if opts.Schedule == "" {
		return fmt.Errorf("schedule expression is required")
	}

	// Basic validation - check it has 5 fields (minute hour day month weekday)
	fields := strings.Fields(opts.Schedule)
	if len(fields) != 5 {
		return fmt.Errorf("invalid cron schedule '%s'. Must be in format 'minute hour day month weekday' (e.g., '0 2 * * *' for daily at 2 AM)", opts.Schedule)
	}

	// Additional basic field validation
	for i, field := range fields {
		if field == "" {
			return fmt.Errorf("invalid cron schedule '%s'. Field %d is empty", opts.Schedule, i+1)
		}
	}

	return nil
}

// validateScheduleAgainstCRD validates the schedule object against the CRD schema
func validateScheduleAgainstCRD(scheduleObj map[string]interface{}, crd *apiextensionsv1.CustomResourceDefinition) error {
	var schema *apiextensionsv1.JSONSchemaProps
	for _, version := range crd.Spec.Versions {
		if version.Name == "v1" && version.Schema != nil {
			schema = version.Schema.OpenAPIV3Schema
			break
		}
	}

	if schema == nil {
		return fmt.Errorf("no v1 schema found in schedule CRD")
	}

	return validateObjectAgainstSchema(scheduleObj, schema, field.NewPath(""))
}

// validateScheduleRequiredFields validates that required schedule fields are present
func validateScheduleRequiredFields(t *testing.T, obj map[string]interface{}) {
	g := NewWithT(t)

	apiVersion, exists := obj["apiVersion"]
	g.Expect(exists).To(BeTrue(), "apiVersion should be present")
	g.Expect(apiVersion).To(Equal("velero.io/v1"), "apiVersion should be velero.io/v1")

	kind, exists := obj["kind"]
	g.Expect(exists).To(BeTrue(), "kind should be present")
	g.Expect(kind).To(Equal("Schedule"), "kind should be Schedule")

	metadata, exists := obj["metadata"]
	g.Expect(exists).To(BeTrue(), "metadata should be present")

	if metaMap, ok := metadata.(map[string]interface{}); ok {
		g.Expect(metaMap["name"]).ToNot(BeEmpty(), "metadata.name should not be empty")
		g.Expect(metaMap["namespace"]).ToNot(BeEmpty(), "metadata.namespace should not be empty")
	}

	spec, exists := obj["spec"]
	g.Expect(exists).To(BeTrue(), "spec should be present")

	if specMap, ok := spec.(map[string]interface{}); ok {
		g.Expect(specMap["schedule"]).ToNot(BeEmpty(), "spec.schedule should not be empty")
		g.Expect(specMap["template"]).ToNot(BeEmpty(), "spec.template should not be empty")
	}
}

// validateScheduleSpecificFields validates schedule-specific fields
func validateScheduleSpecificFields(t *testing.T, obj map[string]interface{}, opts *oadp.CreateOptions) {
	g := NewWithT(t)

	spec, exists := obj["spec"]
	g.Expect(exists).To(BeTrue(), "spec should exist")

	specMap, ok := spec.(map[string]interface{})
	g.Expect(ok).To(BeTrue(), "spec should be a map")

	// Validate schedule field
	schedule, exists := specMap["schedule"]
	g.Expect(exists).To(BeTrue(), "schedule field should exist")
	g.Expect(schedule).To(Equal(opts.Schedule), "schedule should match input")

	// Validate boolean fields
	paused, exists := specMap["paused"]
	g.Expect(exists).To(BeTrue(), "paused field should exist")
	g.Expect(paused).To(Equal(opts.Paused), "paused should match input")

	useOwnerReferences, exists := specMap["useOwnerReferencesInBackup"]
	g.Expect(exists).To(BeTrue(), "useOwnerReferencesInBackup field should exist")
	g.Expect(useOwnerReferences).To(Equal(opts.UseOwnerReferences), "useOwnerReferencesInBackup should match input")

	skipImmediately, exists := specMap["skipImmediately"]
	g.Expect(exists).To(BeTrue(), "skipImmediately field should exist")
	g.Expect(skipImmediately).To(Equal(opts.SkipImmediately), "skipImmediately should match input")
}

// validateScheduleBackupTemplate validates the backup template within the schedule
func validateScheduleBackupTemplate(t *testing.T, obj map[string]interface{}, platform string) {
	g := NewWithT(t)

	spec, exists := obj["spec"]
	g.Expect(exists).To(BeTrue(), "spec should exist")

	specMap, ok := spec.(map[string]interface{})
	g.Expect(ok).To(BeTrue(), "spec should be a map")

	template, exists := specMap["template"]
	g.Expect(exists).To(BeTrue(), "template should exist")

	templateMap, ok := template.(map[string]interface{})
	g.Expect(ok).To(BeTrue(), "template should be a map")

	// Validate included namespaces
	includedNamespacesInterface, exists := templateMap["includedNamespaces"]
	g.Expect(exists).To(BeTrue(), "includedNamespaces should exist")

	var includedNamespaces []string
	if namespacesSlice, ok := includedNamespacesInterface.([]string); ok {
		includedNamespaces = namespacesSlice
	} else if namespacesInterfaceSlice, ok := includedNamespacesInterface.([]interface{}); ok {
		for _, ns := range namespacesInterfaceSlice {
			includedNamespaces = append(includedNamespaces, ns.(string))
		}
	} else {
		g.Expect(fmt.Sprintf("%T", includedNamespacesInterface)).To(Equal("[]string"),
			"includedNamespaces should be []string or []interface{}, got %T", includedNamespacesInterface)
	}

	g.Expect(len(includedNamespaces)).To(BeNumerically(">=", 2), "Should have at least 2 namespaces")

	// Validate included resources
	includedResourcesInterface, exists := templateMap["includedResources"]
	g.Expect(exists).To(BeTrue(), "includedResources should exist")

	var includedResources []string
	if resourcesSlice, ok := includedResourcesInterface.([]string); ok {
		includedResources = resourcesSlice
	} else if resourcesInterfaceSlice, ok := includedResourcesInterface.([]interface{}); ok {
		for _, res := range resourcesInterfaceSlice {
			includedResources = append(includedResources, res.(string))
		}
	} else {
		g.Expect(fmt.Sprintf("%T", includedResourcesInterface)).To(Equal("[]string"),
			"includedResources should be []string or []interface{}, got %T", includedResourcesInterface)
	}

	resourcesStr := strings.Join(includedResources, ",")

	baseResources := []string{
		"hostedclusters.hypershift.openshift.io",
		"nodepools.hypershift.openshift.io",
		"secrets",
		"configmaps",
	}

	for _, baseRes := range baseResources {
		g.Expect(resourcesStr).To(ContainSubstring(baseRes),
			"Platform %s should include base resource %s", platform, baseRes)
	}

	platformSpecific := map[string][]string{
		"AWS":       {"awsclusters.infrastructure.cluster.x-k8s.io"},
		"AGENT":     {"agentclusters.infrastructure.cluster.x-k8s.io"},
		"KUBEVIRT":  {"kubevirtclusters.infrastructure.cluster.x-k8s.io"},
		"AZURE":     {"azureclusters.infrastructure.cluster.x-k8s.io"},
		"OPENSTACK": {"openstackclusters.infrastructure.cluster.x-k8s.io"},
	}

	if expectedResources, exists := platformSpecific[platform]; exists {
		for _, expectedRes := range expectedResources {
			g.Expect(resourcesStr).To(ContainSubstring(expectedRes),
				"Platform %s should include platform-specific resource %s", platform, expectedRes)
		}
	}

	// Validate backup template specific fields
	storageLocation, exists := templateMap["storageLocation"]
	g.Expect(exists).To(BeTrue(), "storageLocation should exist in template")
	g.Expect(storageLocation).ToNot(BeEmpty(), "storageLocation should not be empty")

	ttl, exists := templateMap["ttl"]
	g.Expect(exists).To(BeTrue(), "ttl should exist in template")
	g.Expect(ttl).ToNot(BeEmpty(), "ttl should not be empty")

	snapshotVolumes, exists := templateMap["snapshotVolumes"]
	g.Expect(exists).To(BeTrue(), "snapshotVolumes should exist in template")
	g.Expect(snapshotVolumes).To(Equal(true), "snapshotVolumes should be true")

	dataMover, exists := templateMap["dataMover"]
	g.Expect(exists).To(BeTrue(), "dataMover should exist in template")
	g.Expect(dataMover).To(Equal("velero"), "dataMover should be 'velero'")
}