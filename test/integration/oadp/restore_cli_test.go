//go:build integration
// +build integration

package oadp

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/openshift/hypershift/cmd/oadp"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"
)


// TestRestoreManifestValidation validates that generated restore manifests are valid according to Velero CRD
func TestRestoreManifestValidation(t *testing.T) {
	g := NewWithT(t)

	tests := []struct {
		name string
		opts *oadp.CreateOptions
	}{
		{
			"restore from backup",
			&oadp.CreateOptions{
				HCName:                 "test-cluster",
				HCNamespace:            "test-cluster-ns",
				BackupName:             "test-backup-123",
				OADPNamespace:          "openshift-adp",
				ExistingResourcePolicy: "update",
				RestorePVs:             ptr.To(true),
				PreserveNodePorts:      ptr.To(true),
			},
		},
		{
			"restore from schedule",
			&oadp.CreateOptions{
				HCName:                 "prod-cluster",
				HCNamespace:            "prod-cluster-ns",
				ScheduleName:           "daily-backup",
				OADPNamespace:          "openshift-adp",
				ExistingResourcePolicy: "none",
				RestorePVs:             ptr.To(false),
				PreserveNodePorts:      ptr.To(false),
			},
		},
		{
			"restore with custom namespaces",
			&oadp.CreateOptions{
				HCName:                 "custom-cluster",
				HCNamespace:            "custom-cluster-ns",
				BackupName:             "custom-backup",
				OADPNamespace:          "custom-adp",
				ExistingResourcePolicy: "update",
				IncludeNamespaces:      []string{"custom-ns1", "custom-ns2"},
				RestorePVs:             ptr.To(true),
				PreserveNodePorts:      ptr.To(false),
			},
		},
		{
			"restore with custom name",
			&oadp.CreateOptions{
				HCName:                 "named-cluster",
				HCNamespace:            "named-cluster-ns",
				BackupName:             "named-backup",
				RestoreName:            "custom-restore-name",
				OADPNamespace:          "openshift-adp",
				ExistingResourcePolicy: "none",
			},
		},
	}

	// Download Velero Restore CRD once
	t.Log("Downloading Velero Restore CRD...")
	restoreCRD, err := downloadRestoreCRD()
	g.Expect(err).ToNot(HaveOccurred(), "Failed to download Velero Restore CRD")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Generate restore manifest
			restore, restoreName, err := tt.opts.GenerateRestoreObject()
			g.Expect(err).ToNot(HaveOccurred(), "Failed to generate restore object")

			// Convert to YAML for validation
			yamlBytes, err := yaml.Marshal(restore.Object)
			g.Expect(err).ToNot(HaveOccurred(), "Failed to marshal restore to YAML")

			// Validate against CRD schema
			err = validateRestoreAgainstCRD(restore.Object, restoreCRD)
			g.Expect(err).ToNot(HaveOccurred(), "Restore manifest failed CRD validation for %s", tt.name)

			// Additional specific validations
			t.Run("required_fields", func(t *testing.T) {
				validateRestoreRequiredFields(t, restore.Object)
			})

			t.Run("restore_configuration", func(t *testing.T) {
				validateRestoreConfiguration(t, restore.Object, tt.opts, restoreName)
			})

			t.Run("namespace_configuration", func(t *testing.T) {
				validateRestoreNamespaces(t, restore.Object, tt.opts)
			})

			t.Logf("✅ %s restore manifest validated successfully", tt.name)
			t.Logf("Generated YAML:\n%s", string(yamlBytes))
		})
	}
}

// TestRestoreCLIConfiguration validates restore CLI configuration and defaults
func TestRestoreCLIConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		opts     *oadp.CreateOptions
		wantErr  bool
		validate func(g Gomega, restore map[string]interface{}, restoreName string)
	}{
		{
			name: "default configuration from backup",
			opts: &oadp.CreateOptions{
				HCName:      "test-cluster",
				HCNamespace: "test-cluster-ns",
				BackupName:  "test-backup",
			},
			wantErr: false,
			validate: func(g Gomega, restore map[string]interface{}, restoreName string) {
				spec := restore["spec"].(map[string]interface{})
				metadata := restore["metadata"].(map[string]interface{})
				g.Expect(spec["existingResourcePolicy"]).To(Equal("update"), "Default resource policy should be 'update'")
				g.Expect(spec["restorePVs"]).To(Equal(true), "Default RestorePVs is true (Go zero value)")
				g.Expect(spec["preserveNodePorts"]).To(Equal(true), "Default PreserveNodePorts is true (Go zero value)")
				g.Expect(metadata["namespace"]).To(Equal("openshift-adp"), "Default OADP namespace should be openshift-adp")
			},
		},
		{
			name: "custom configuration from schedule",
			opts: &oadp.CreateOptions{
				HCName:                 "custom-cluster",
				HCNamespace:            "custom-ns",
				ScheduleName:           "weekly-backup",
				OADPNamespace:          "custom-oadp",
				ExistingResourcePolicy: "none",
				RestorePVs:             ptr.To(false),
				PreserveNodePorts:      ptr.To(false),
			},
			wantErr: false,
			validate: func(g Gomega, restore map[string]interface{}, restoreName string) {
				spec := restore["spec"].(map[string]interface{})
				metadata := restore["metadata"].(map[string]interface{})
				g.Expect(spec["scheduleName"]).To(Equal("weekly-backup"), "Schedule name should be custom")
				g.Expect(spec["existingResourcePolicy"]).To(Equal("none"), "Resource policy should be none")
				g.Expect(spec["restorePVs"]).To(Equal(false), "RestorePVs should be false")
				g.Expect(spec["preserveNodePorts"]).To(Equal(false), "PreserveNodePorts should be false")
				g.Expect(metadata["namespace"]).To(Equal("custom-oadp"), "OADP namespace should be custom")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			restore, restoreName, err := tt.opts.GenerateRestoreObject()
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			if tt.validate != nil {
				tt.validate(g, restore.Object, restoreName)
			}
		})
	}
}

// TestRestoreValidationRules validates restore business rules and validation logic
func TestRestoreValidationRules(t *testing.T) {
	tests := []struct {
		name    string
		opts    *oadp.CreateOptions
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid backup source",
			opts: &oadp.CreateOptions{
				HCName:      "test-cluster",
				HCNamespace: "test-cluster-ns",
				BackupName:  "test-backup",
			},
			wantErr: false,
		},
		{
			name: "valid schedule source",
			opts: &oadp.CreateOptions{
				HCName:       "test-cluster",
				HCNamespace:  "test-cluster-ns",
				ScheduleName: "daily-schedule",
			},
			wantErr: false,
		},
		{
			name: "invalid no source",
			opts: &oadp.CreateOptions{
				HCName:      "test-cluster",
				HCNamespace: "test-cluster-ns",
			},
			wantErr: true,
			errMsg:  "either --from-backup or --from-schedule must be specified",
		},
		{
			name: "invalid both sources",
			opts: &oadp.CreateOptions{
				HCName:       "test-cluster",
				HCNamespace:  "test-cluster-ns",
				BackupName:   "test-backup",
				ScheduleName: "daily-schedule",
			},
			wantErr: true,
			errMsg:  "mutually exclusive",
		},
		{
			name: "invalid resource policy",
			opts: &oadp.CreateOptions{
				HCName:                 "test-cluster",
				HCNamespace:            "test-cluster-ns",
				BackupName:             "test-backup",
				ExistingResourcePolicy: "invalid-policy",
			},
			wantErr: true,
			errMsg:  "invalid existing-resource-policy",
		},
		{
			name: "valid resource policy update",
			opts: &oadp.CreateOptions{
				HCName:                 "test-cluster",
				HCNamespace:            "test-cluster-ns",
				BackupName:             "test-backup",
				ExistingResourcePolicy: "update",
			},
			wantErr: false,
		},
		{
			name: "valid resource policy none",
			opts: &oadp.CreateOptions{
				HCName:                 "test-cluster",
				HCNamespace:            "test-cluster-ns",
				BackupName:             "test-backup",
				ExistingResourcePolicy: "none",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Test validation functions directly
			if tt.opts.BackupName == "" && tt.opts.ScheduleName == "" ||
				tt.opts.BackupName != "" && tt.opts.ScheduleName != "" {
				err := tt.opts.RunRestore(nil) // This would fail due to validation
				if tt.wantErr {
					g.Expect(err).To(HaveOccurred())
					if tt.errMsg != "" {
						g.Expect(err.Error()).To(ContainSubstring(tt.errMsg))
					}
				}
			} else {
				// Test manifest generation and validation for valid backup/schedule combinations
				if tt.opts.ExistingResourcePolicy != "" && (tt.opts.ExistingResourcePolicy != "none" && tt.opts.ExistingResourcePolicy != "update") {
					// Test validation for invalid resource policy
					err := tt.opts.RunRestore(nil)
					if tt.wantErr {
						g.Expect(err).To(HaveOccurred())
						if tt.errMsg != "" {
							g.Expect(err.Error()).To(ContainSubstring(tt.errMsg))
						}
					}
				} else {
					// Test manifest generation for valid configs
					_, _, err := tt.opts.GenerateRestoreObject()
					if tt.wantErr {
						g.Expect(err).To(HaveOccurred())
						if tt.errMsg != "" {
							g.Expect(err.Error()).To(ContainSubstring(tt.errMsg))
						}
					} else {
						g.Expect(err).ToNot(HaveOccurred())
					}
				}
			}
		})
	}
}

// validateRestoreAgainstCRD validates the restore object against the CRD schema
func validateRestoreAgainstCRD(restoreObj map[string]interface{}, crd *apiextensionsv1.CustomResourceDefinition) error {
	var schema *apiextensionsv1.JSONSchemaProps
	for _, version := range crd.Spec.Versions {
		if version.Name == "v1" && version.Schema != nil {
			schema = version.Schema.OpenAPIV3Schema
			break
		}
	}

	if schema == nil {
		return fmt.Errorf("no v1 schema found in restore CRD")
	}

	return validateObjectAgainstSchema(restoreObj, schema, field.NewPath(""))
}

// validateRestoreRequiredFields validates that required restore fields are present
func validateRestoreRequiredFields(t *testing.T, obj map[string]interface{}) {
	g := NewWithT(t)

	apiVersion, exists := obj["apiVersion"]
	g.Expect(exists).To(BeTrue(), "apiVersion should be present")
	g.Expect(apiVersion).To(Equal("velero.io/v1"), "apiVersion should be velero.io/v1")

	kind, exists := obj["kind"]
	g.Expect(exists).To(BeTrue(), "kind should be present")
	g.Expect(kind).To(Equal("Restore"), "kind should be Restore")

	metadata, exists := obj["metadata"]
	g.Expect(exists).To(BeTrue(), "metadata should be present")

	if metaMap, ok := metadata.(map[string]interface{}); ok {
		g.Expect(metaMap["name"]).ToNot(BeEmpty(), "metadata.name should not be empty")
		g.Expect(metaMap["namespace"]).ToNot(BeEmpty(), "metadata.namespace should not be empty")
	}

	spec, exists := obj["spec"]
	g.Expect(exists).To(BeTrue(), "spec should be present")
	g.Expect(spec).ToNot(BeNil(), "spec should not be nil")
}

// validateRestoreConfiguration validates restore-specific configuration
func validateRestoreConfiguration(t *testing.T, obj map[string]interface{}, opts *oadp.CreateOptions, restoreName string) {
	g := NewWithT(t)

	spec, exists := obj["spec"]
	g.Expect(exists).To(BeTrue(), "spec should exist")

	specMap, ok := spec.(map[string]interface{})
	g.Expect(ok).To(BeTrue(), "spec should be a map")

	// Validate source (backup or schedule)
	if opts.BackupName != "" {
		backupName, exists := specMap["backupName"]
		g.Expect(exists).To(BeTrue(), "backupName should exist when BackupName is set")
		g.Expect(backupName).To(Equal(opts.BackupName), "backupName should match opts")

		// scheduleName should not exist
		_, exists = specMap["scheduleName"]
		g.Expect(exists).To(BeFalse(), "scheduleName should not exist when BackupName is set")
	}

	if opts.ScheduleName != "" {
		scheduleName, exists := specMap["scheduleName"]
		g.Expect(exists).To(BeTrue(), "scheduleName should exist when ScheduleName is set")
		g.Expect(scheduleName).To(Equal(opts.ScheduleName), "scheduleName should match opts")

		// backupName should not exist
		_, exists = specMap["backupName"]
		g.Expect(exists).To(BeFalse(), "backupName should not exist when ScheduleName is set")
	}

	// Validate resource policy
	if opts.ExistingResourcePolicy != "" {
		policy, exists := specMap["existingResourcePolicy"]
		g.Expect(exists).To(BeTrue(), "existingResourcePolicy should exist")
		g.Expect(policy).To(Equal(opts.ExistingResourcePolicy), "existingResourcePolicy should match opts")
	}

	// Validate PV restore setting
	restorePVs, exists := specMap["restorePVs"]
	g.Expect(exists).To(BeTrue(), "restorePVs should exist")
	g.Expect(restorePVs).To(Equal(*opts.RestorePVs), "restorePVs should match opts")

	// Validate node ports setting
	preserveNodePorts, exists := specMap["preserveNodePorts"]
	g.Expect(exists).To(BeTrue(), "preserveNodePorts should exist")
	g.Expect(preserveNodePorts).To(Equal(*opts.PreserveNodePorts), "preserveNodePorts should match opts")

	// Validate excluded resources
	excludedResources, exists := specMap["excludedResources"]
	g.Expect(exists).To(BeTrue(), "excludedResources should exist")

	if excludedResourcesSlice, ok := excludedResources.([]interface{}); ok {
		excludedResourcesStr := make([]string, len(excludedResourcesSlice))
		for i, res := range excludedResourcesSlice {
			excludedResourcesStr[i] = res.(string)
		}

		// Verify standard excluded resources
		expectedExcluded := []string{"nodes", "events", "backups.velero.io", "restores.velero.io"}
		for _, expected := range expectedExcluded {
			g.Expect(excludedResourcesStr).To(ContainElement(expected),
				"Should exclude standard resource %s", expected)
		}
	}

	// Validate restore name
	metadata := obj["metadata"].(map[string]interface{})
	actualName := metadata["name"].(string)
	g.Expect(actualName).To(Equal(restoreName), "Restore object name should match returned name")
}

// validateRestoreNamespaces validates restore namespace configuration
func validateRestoreNamespaces(t *testing.T, obj map[string]interface{}, opts *oadp.CreateOptions) {
	g := NewWithT(t)
	spec := obj["spec"].(map[string]interface{})

	includedNamespaces, exists := spec["includedNamespaces"]
	g.Expect(exists).To(BeTrue(), "includedNamespaces should exist")

	var namespaces []string
	if nsSlice, ok := includedNamespaces.([]interface{}); ok {
		for _, ns := range nsSlice {
			namespaces = append(namespaces, ns.(string))
		}
	} else {
		t.Fatalf("includedNamespaces should be []interface{}, got %T", includedNamespaces)
	}

	if len(opts.IncludeNamespaces) > 0 {
		// Custom namespaces should match exactly
		g.Expect(namespaces).To(ConsistOf(opts.IncludeNamespaces),
			"Custom included namespaces should match opts")
	} else {
		// Default namespaces: hc-namespace and hc-namespace-hc-name
		expectedDefault := []string{
			opts.HCNamespace,
			fmt.Sprintf("%s-%s", opts.HCNamespace, opts.HCName),
		}
		g.Expect(namespaces).To(ConsistOf(expectedDefault),
			"Default included namespaces should follow 1NS=1HC pattern")
	}

	// Ensure namespaces are not empty
	g.Expect(namespaces).ToNot(BeEmpty(), "includedNamespaces should not be empty")
	for _, ns := range namespaces {
		g.Expect(ns).ToNot(BeEmpty(), "namespace name should not be empty")
	}
}
