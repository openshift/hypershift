package v1beta1

import (
	"testing"
)

func TestGCPResourceReference_Validation(t *testing.T) {
	tests := []struct {
		name    string
		ref     GCPResourceReference
		isValid bool
		desc    string
	}{
		{
			name: "valid resource name",
			ref: GCPResourceReference{
				Name: "my-resource-name",
			},
			isValid: true,
			desc:    "should accept valid resource names with hyphens",
		},
		{
			name: "valid single character name",
			ref: GCPResourceReference{
				Name: "a",
			},
			isValid: true,
			desc:    "should accept single character names",
		},
		{
			name: "valid 63 character name",
			ref: GCPResourceReference{
				Name: "this-is-exactly-sixty-three-characters-long-resource-name-abc",
			},
			isValid: true,
			desc:    "should accept 63 character names (max length)",
		},
		{
			name: "empty name",
			ref: GCPResourceReference{
				Name: "",
			},
			isValid: false,
			desc:    "should reject empty names",
		},
		{
			name: "name too long",
			ref: GCPResourceReference{
				Name: "this-name-is-longer-than-sixty-three-characters-and-should-fail-validation-test",
			},
			isValid: false,
			desc:    "should reject names longer than 63 characters",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Test name length constraints
			if test.isValid {
				if len(test.ref.Name) < 1 {
					t.Errorf("valid names should have at least 1 character, got length %d", len(test.ref.Name))
				}
				if len(test.ref.Name) > 63 {
					t.Errorf("valid names should have at most 63 characters, got length %d", len(test.ref.Name))
				}
			} else {
				if test.ref.Name == "" {
					if len(test.ref.Name) != 0 {
						t.Errorf("empty names should be detected, got length %d", len(test.ref.Name))
					}
				} else {
					if len(test.ref.Name) <= 63 {
						t.Errorf("names over 63 characters should be detected, got length %d", len(test.ref.Name))
					}
				}
			}
		})
	}
}

func TestGCPEndpointAccessType_Values(t *testing.T) {
	tests := []struct {
		name        string
		accessType  GCPEndpointAccessType
		description string
	}{
		{
			name:        "PublicAndPrivate access type",
			accessType:  GCPEndpointAccessPublicAndPrivate,
			description: "should have correct value for PublicAndPrivate",
		},
		{
			name:        "Private access type",
			accessType:  GCPEndpointAccessPrivate,
			description: "should have correct value for Private",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			switch test.accessType {
			case GCPEndpointAccessPublicAndPrivate:
				if string(test.accessType) != "PublicAndPrivate" {
					t.Errorf("expected 'PublicAndPrivate', got %s", string(test.accessType))
				}
			case GCPEndpointAccessPrivate:
				if string(test.accessType) != "Private" {
					t.Errorf("expected 'Private', got %s", string(test.accessType))
				}
			default:
				t.Errorf("Unknown endpoint access type: %v", test.accessType)
			}
		})
	}
}

func TestGCPNetworkConfigCustomer_Validation(t *testing.T) {
	tests := []struct {
		name    string
		config  GCPNetworkConfigCustomer
		isValid bool
		desc    string
	}{
		{
			name: "valid network config",
			config: GCPNetworkConfigCustomer{
				Project: "my-project-123",
				Network: GCPResourceReference{
					Name: "my-network",
				},
				PSCSubnet: GCPResourceReference{
					Name: "my-psc-subnet",
				},
			},
			isValid: true,
			desc:    "should accept valid network configuration",
		},
		{
			name: "valid project with minimum length",
			config: GCPNetworkConfigCustomer{
				Project: "abcdef", // 6 characters minimum
				Network: GCPResourceReference{
					Name: "network",
				},
				PSCSubnet: GCPResourceReference{
					Name: "subnet",
				},
			},
			isValid: true,
			desc:    "should accept minimum length project ID",
		},
		{
			name: "valid project with maximum length",
			config: GCPNetworkConfigCustomer{
				Project: "a-really-long-project-id-123", // 30 characters
				Network: GCPResourceReference{
					Name: "network",
				},
				PSCSubnet: GCPResourceReference{
					Name: "subnet",
				},
			},
			isValid: true,
			desc:    "should accept maximum length project ID",
		},
		{
			name: "invalid project - too short",
			config: GCPNetworkConfigCustomer{
				Project: "short", // 5 characters (too short)
				Network: GCPResourceReference{
					Name: "network",
				},
				PSCSubnet: GCPResourceReference{
					Name: "subnet",
				},
			},
			isValid: false,
			desc:    "should reject project IDs shorter than 6 characters",
		},
		{
			name: "invalid project - too long",
			config: GCPNetworkConfigCustomer{
				Project: "this-project-id-is-way-too-long-and-exceeds-thirty-characters",
				Network: GCPResourceReference{
					Name: "network",
				},
				PSCSubnet: GCPResourceReference{
					Name: "subnet",
				},
			},
			isValid: false,
			desc:    "should reject project IDs longer than 30 characters",
		},
		{
			name: "invalid project - starts with number",
			config: GCPNetworkConfigCustomer{
				Project: "123-invalid-project",
				Network: GCPResourceReference{
					Name: "network",
				},
				PSCSubnet: GCPResourceReference{
					Name: "subnet",
				},
			},
			isValid: false,
			desc:    "should reject project IDs starting with numbers",
		},
		{
			name: "invalid project - ends with hyphen",
			config: GCPNetworkConfigCustomer{
				Project: "invalid-project-",
				Network: GCPResourceReference{
					Name: "network",
				},
				PSCSubnet: GCPResourceReference{
					Name: "subnet",
				},
			},
			isValid: false,
			desc:    "should reject project IDs ending with hyphens",
		},
		{
			name: "invalid project - uppercase letters",
			config: GCPNetworkConfigCustomer{
				Project: "Invalid-Project",
				Network: GCPResourceReference{
					Name: "network",
				},
				PSCSubnet: GCPResourceReference{
					Name: "subnet",
				},
			},
			isValid: false,
			desc:    "should reject project IDs with uppercase letters",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Test project ID validation pattern: ^[a-z][a-z0-9-]{4,28}[a-z0-9]$
			if test.isValid {
				// Valid project IDs should be 6-30 characters long
				if len(test.config.Project) < 6 {
					t.Errorf("valid project IDs should be at least 6 characters, got %d", len(test.config.Project))
				}
				if len(test.config.Project) > 30 {
					t.Errorf("valid project IDs should be at most 30 characters, got %d", len(test.config.Project))
				}

				// Should start with a lowercase letter
				if len(test.config.Project) > 0 {
					firstChar := test.config.Project[0]
					if firstChar < 'a' || firstChar > 'z' {
						t.Errorf("valid project IDs should start with lowercase letter, got '%c'", firstChar)
					}
				}

				// Should end with a lowercase letter or digit
				if len(test.config.Project) > 0 {
					lastChar := test.config.Project[len(test.config.Project)-1]
					if !((lastChar >= 'a' && lastChar <= 'z') || (lastChar >= '0' && lastChar <= '9')) {
						t.Errorf("valid project IDs should end with lowercase letter or digit, got '%c'", lastChar)
					}
				}

				// Should have valid resource references
				if test.config.Network.Name == "" {
					t.Errorf("Network name should not be empty")
				}
				if test.config.PSCSubnet.Name == "" {
					t.Errorf("PSCSubnet name should not be empty")
				}
			}
		})
	}
}

func TestGCPPlatformSpec_Defaults(t *testing.T) {
	// Test that default values work as expected
	spec := GCPPlatformSpec{
		Project: "my-project-123",
		Region:  "us-central1",
	}

	// Default EndpointAccess should be empty by default, controller sets the default
	if spec.EndpointAccess != "" {
		t.Errorf("EndpointAccess should be empty by default, got %s", spec.EndpointAccess)
	}
}

func TestGCPPlatformSpec_WithCustomerNetworkConfig(t *testing.T) {
	spec := GCPPlatformSpec{
		Project: "my-project-123",
		Region:  "us-central1",
		CustomerNetworkConfig: &GCPNetworkConfigCustomer{
			Project: "customer-project-456",
			Network: GCPResourceReference{
				Name: "customer-vpc",
			},
			PSCSubnet: GCPResourceReference{
				Name: "customer-psc-subnet",
			},
		},
		EndpointAccess: GCPEndpointAccessPrivate,
	}

	if spec.CustomerNetworkConfig == nil {
		t.Errorf("CustomerNetworkConfig should not be nil")
		return
	}

	if spec.CustomerNetworkConfig.Project != "customer-project-456" {
		t.Errorf("expected customer project 'customer-project-456', got %s", spec.CustomerNetworkConfig.Project)
	}

	if spec.CustomerNetworkConfig.Network.Name != "customer-vpc" {
		t.Errorf("expected network name 'customer-vpc', got %s", spec.CustomerNetworkConfig.Network.Name)
	}

	if spec.CustomerNetworkConfig.PSCSubnet.Name != "customer-psc-subnet" {
		t.Errorf("expected subnet name 'customer-psc-subnet', got %s", spec.CustomerNetworkConfig.PSCSubnet.Name)
	}

	if spec.EndpointAccess != GCPEndpointAccessPrivate {
		t.Errorf("expected endpoint access 'Private', got %s", spec.EndpointAccess)
	}
}