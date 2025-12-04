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

// TestBackupManifestValidation validates that generated backup manifests are valid according to Velero CRD
func TestBackupManifestValidation(t *testing.T) {
	g := NewWithT(t)

	tests := []struct {
		name     string
		platform string
	}{
		{"AWS platform backup", "AWS"},
		{"Agent platform backup", "AGENT"},
		{"KubeVirt platform backup", "KUBEVIRT"},
		{"Azure platform backup", "AZURE"},
		{"OpenStack platform backup", "OPENSTACK"},
	}

	// Download Velero Backup CRD once
	t.Log("Downloading Velero Backup CRD...")
	backupCRD, err := downloadBackupCRD()
	g.Expect(err).ToNot(HaveOccurred(), "Failed to download Velero Backup CRD")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Generate backup manifest
			opts := &oadp.CreateOptions{
				HCName:          "test-cluster",
				HCNamespace:     "test-cluster-ns",
				OADPNamespace:   "openshift-adp",
				StorageLocation: "default",
				TTL:             2 * time.Hour,
			}

			backup, _, err := opts.GenerateBackupObjectWithPlatform(tt.platform)
			g.Expect(err).ToNot(HaveOccurred(), "Failed to generate backup object")

			// Convert to YAML for validation
			yamlBytes, err := yaml.Marshal(backup.Object)
			g.Expect(err).ToNot(HaveOccurred(), "Failed to marshal backup to YAML")

			// Validate against CRD schema
			err = validateBackupAgainstCRD(backup.Object, backupCRD)
			g.Expect(err).ToNot(HaveOccurred(), "Backup manifest failed CRD validation for %s", tt.platform)

			// Additional specific validations
			t.Run("required_fields", func(t *testing.T) {
				validateBackupRequiredFields(t, backup.Object)
			})

			t.Run("platform_resources", func(t *testing.T) {
				validateBackupPlatformResources(t, backup.Object, tt.platform)
			})

			t.Logf("✅ %s backup manifest validated successfully", tt.platform)
			t.Logf("Generated YAML:\n%s", string(yamlBytes))
		})
	}
}

// TestBackupCLIConfiguration validates backup CLI configuration and defaults
func TestBackupCLIConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		opts     *oadp.CreateOptions
		wantErr  bool
		validate func(g Gomega, backup map[string]interface{})
	}{
		{
			name: "default configuration",
			opts: &oadp.CreateOptions{
				HCName:      "test-cluster",
				HCNamespace: "test-cluster-ns",
			},
			wantErr: false,
			validate: func(g Gomega, backup map[string]interface{}) {
				spec := backup["spec"].(map[string]interface{})
				g.Expect(spec["storageLocation"]).To(Equal("default"), "Default storage location should be 'default'")
				g.Expect(spec["ttl"]).To(Equal("2h0m0s"), "Default TTL should be 2h")
			},
		},
		{
			name: "custom configuration",
			opts: &oadp.CreateOptions{
				HCName:                   "custom-cluster",
				HCNamespace:              "custom-ns",
				StorageLocation:          "s3-backup",
				TTL:                      24 * time.Hour,
				SnapshotMoveData:         false,
				DefaultVolumesToFsBackup: true,
			},
			wantErr: false,
			validate: func(g Gomega, backup map[string]interface{}) {
				spec := backup["spec"].(map[string]interface{})
				g.Expect(spec["storageLocation"]).To(Equal("s3-backup"), "Storage location should be custom")
				g.Expect(spec["ttl"]).To(Equal("24h0m0s"), "TTL should be 24h")
				g.Expect(spec["snapshotMoveData"]).To(Equal(false), "SnapshotMoveData should be false")
				g.Expect(spec["defaultVolumesToFsBackup"]).To(Equal(true), "DefaultVolumesToFsBackup should be true")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			backup, _, err := tt.opts.GenerateBackupObjectWithPlatform("AWS")
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			if tt.validate != nil {
				tt.validate(g, backup.Object)
			}
		})
	}
}

// validateBackupAgainstCRD validates the backup object against the CRD schema
func validateBackupAgainstCRD(backupObj map[string]interface{}, crd *apiextensionsv1.CustomResourceDefinition) error {
	var schema *apiextensionsv1.JSONSchemaProps
	for _, version := range crd.Spec.Versions {
		if version.Name == "v1" && version.Schema != nil {
			schema = version.Schema.OpenAPIV3Schema
			break
		}
	}

	if schema == nil {
		return fmt.Errorf("no v1 schema found in backup CRD")
	}

	return validateObjectAgainstSchema(backupObj, schema, field.NewPath(""))
}

// validateObjectAgainstSchema performs basic schema validation
func validateObjectAgainstSchema(obj map[string]interface{}, schema *apiextensionsv1.JSONSchemaProps, path *field.Path) error {
	if len(schema.Required) > 0 {
		for _, req := range schema.Required {
			if _, exists := obj[req]; !exists {
				return field.Required(path.Child(req), fmt.Sprintf("required field %s is missing", req))
			}
		}
	}

	if schema.Properties != nil {
		for key, value := range obj {
			if propSchema, exists := schema.Properties[key]; exists {
				if propSchema.Type == "object" && propSchema.Properties != nil {
					if nestedObj, ok := value.(map[string]interface{}); ok {
						if err := validateObjectAgainstSchema(nestedObj, &propSchema, path.Child(key)); err != nil {
							return err
						}
					}
				}
			}
		}
	}

	return nil
}

// validateBackupRequiredFields validates that required backup fields are present
func validateBackupRequiredFields(t *testing.T, obj map[string]interface{}) {
	g := NewWithT(t)

	apiVersion, exists := obj["apiVersion"]
	g.Expect(exists).To(BeTrue(), "apiVersion should be present")
	g.Expect(apiVersion).To(Equal("velero.io/v1"), "apiVersion should be velero.io/v1")

	kind, exists := obj["kind"]
	g.Expect(exists).To(BeTrue(), "kind should be present")
	g.Expect(kind).To(Equal("Backup"), "kind should be Backup")

	metadata, exists := obj["metadata"]
	g.Expect(exists).To(BeTrue(), "metadata should be present")

	if metaMap, ok := metadata.(map[string]interface{}); ok {
		g.Expect(metaMap["name"]).ToNot(BeEmpty(), "metadata.name should not be empty")
		g.Expect(metaMap["namespace"]).ToNot(BeEmpty(), "metadata.namespace should not be empty")
	}

	spec, exists := obj["spec"]
	g.Expect(exists).To(BeTrue(), "spec should be present")

	if specMap, ok := spec.(map[string]interface{}); ok {
		g.Expect(specMap["includedNamespaces"]).ToNot(BeEmpty(), "spec.includedNamespaces should not be empty")
		g.Expect(specMap["includedResources"]).ToNot(BeEmpty(), "spec.includedResources should not be empty")
	}
}

// validateBackupPlatformResources validates that platform-specific backup resources are included
func validateBackupPlatformResources(t *testing.T, obj map[string]interface{}, platform string) {
	g := NewWithT(t)

	spec, exists := obj["spec"]
	g.Expect(exists).To(BeTrue(), "spec should exist")

	specMap, ok := spec.(map[string]interface{})
	g.Expect(ok).To(BeTrue(), "spec should be a map")

	includedResourcesInterface, exists := specMap["includedResources"]
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
}

// TestBackupCustomNameIntegration validates that the --name flag works correctly with validation
func TestBackupCustomNameIntegration(t *testing.T) {
	tests := []struct {
		name                  string
		customName            string
		expectValidBackup     bool
		expectValidationError bool
	}{
		{
			name:                  "Valid custom name",
			customName:            "my-production-backup",
			expectValidBackup:     true,
			expectValidationError: false,
		},
		{
			name:                  "Custom name with numbers",
			customName:            "backup-cluster-123",
			expectValidBackup:     true,
			expectValidationError: false,
		},
		{
			name:                  "Name too long (over 63 chars)",
			customName:            "this-is-a-very-long-backup-name-that-exceeds-the-kubernetes-limit",
			expectValidBackup:     false,
			expectValidationError: true,
		},
		{
			name:                  "Name with invalid characters",
			customName:            "backup_with_underscores",
			expectValidBackup:     false,
			expectValidationError: true,
		},
		{
			name:                  "Empty name (auto-generated)",
			customName:            "",
			expectValidBackup:     true,
			expectValidationError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			opts := &oadp.CreateOptions{
				HCName:           "test-cluster",
				HCNamespace:      "test-cluster-ns",
				BackupCustomName: tt.customName,
				OADPNamespace:    "openshift-adp",
				StorageLocation:  "default",
				TTL:              2 * time.Hour,
			}

			// Test validation
			err := opts.ValidateBackupName()
			if tt.expectValidationError {
				g.Expect(err).To(HaveOccurred(), "Expected validation error for name '%s'", tt.customName)
				return
			}
			g.Expect(err).ToNot(HaveOccurred(), "Unexpected validation error for name '%s'", tt.customName)

			// Test backup generation
			if tt.expectValidBackup {
				backup, backupName, err := opts.GenerateBackupObjectWithPlatform("AWS")
				g.Expect(err).ToNot(HaveOccurred(), "Failed to generate backup object")

				if tt.customName != "" {
					g.Expect(backupName).To(Equal(tt.customName), "Custom backup name should be used")
					g.Expect(backup.GetName()).To(Equal(tt.customName), "Backup object name should match custom name")
				} else {
					expectedPrefix := "test-cluster-test-cluster-ns-"
					g.Expect(backupName).To(HavePrefix(expectedPrefix), "Should use auto-generated name pattern")
					g.Expect(len(backupName)).To(Equal(len(expectedPrefix) + 6), "Auto-generated name should have 6-char random suffix")
					g.Expect(backup.GetName()).To(Equal(backupName), "Backup object name should match generated name")
				}
			}
		})
	}
}

// TestBackupIncludedNamespacesIntegration validates that the --include-additional-namespaces flag works correctly
func TestBackupIncludedNamespacesIntegration(t *testing.T) {
	tests := []struct {
		name               string
		hcName             string
		hcNamespace        string
		includeNamespaces  []string
		expectedNamespaces []string
	}{
		{
			name:               "Default namespaces",
			hcName:             "prod-cluster",
			hcNamespace:        "production-ns",
			includeNamespaces:  nil,
			expectedNamespaces: []string{"production-ns", "production-ns-prod-cluster"},
		},
		{
			name:               "Additional namespaces added to defaults",
			hcName:             "test-cluster",
			hcNamespace:        "test-ns",
			includeNamespaces:  []string{"custom-app-ns", "custom-data-ns"},
			expectedNamespaces: []string{"test-ns", "test-ns-test-cluster", "custom-app-ns", "custom-data-ns"},
		},
		{
			name:               "Single additional namespace",
			hcName:             "demo-cluster",
			hcNamespace:        "demo-ns",
			includeNamespaces:  []string{"demo-app"},
			expectedNamespaces: []string{"demo-ns", "demo-ns-demo-cluster", "demo-app"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			opts := &oadp.CreateOptions{
				HCName:            tt.hcName,
				HCNamespace:       tt.hcNamespace,
				IncludeNamespaces: tt.includeNamespaces,
				OADPNamespace:     "openshift-adp",
				StorageLocation:   "default",
				TTL:               2 * time.Hour,
			}

			backup, _, err := opts.GenerateBackupObjectWithPlatform("AWS")
			g.Expect(err).ToNot(HaveOccurred(), "Failed to generate backup object")

			// Extract included namespaces from backup spec
			spec, exists := backup.Object["spec"].(map[string]interface{})
			g.Expect(exists).To(BeTrue(), "Backup should have spec field")

			includedNamespacesInterface, exists := spec["includedNamespaces"]
			g.Expect(exists).To(BeTrue(), "Backup spec should have includedNamespaces field")

			// Convert to string slice
			var includedNamespaces []string
			if nsSlice, ok := includedNamespacesInterface.([]string); ok {
				includedNamespaces = nsSlice
			} else if nsInterfaceSlice, ok := includedNamespacesInterface.([]interface{}); ok {
				for _, ns := range nsInterfaceSlice {
					includedNamespaces = append(includedNamespaces, ns.(string))
				}
			} else {
				t.Fatalf("includedNamespaces has unexpected type: %T", includedNamespacesInterface)
			}

			// Verify namespaces match expected
			g.Expect(includedNamespaces).To(Equal(tt.expectedNamespaces),
				"Included namespaces should match expected for test case: %s", tt.name)

			t.Logf("✅ Backup included namespaces validated: %v", includedNamespaces)
		})
	}
}
