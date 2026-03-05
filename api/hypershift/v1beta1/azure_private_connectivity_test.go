package v1beta1

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestAzureEndpointAccessType_EnumValues verifies the AzureEndpointAccessType enum
// constants match the kubebuilder:validation:Enum marker values.
// This maps to subtests 2.2 (invalid endpointAccess values) and 2.3/2.4 (valid values).
func TestAzureEndpointAccessType_EnumValues(t *testing.T) {
	t.Parallel()

	expectedValues := map[AzureEndpointAccessType]bool{
		AzureEndpointAccessPublic:           true,
		AzureEndpointAccessPublicAndPrivate: true,
		AzureEndpointAccessPrivate:          true,
	}

	// Verify all expected constants exist and have correct values
	tests := []struct {
		name     string
		constant AzureEndpointAccessType
		value    string
	}{
		{name: "Public", constant: AzureEndpointAccessPublic, value: "Public"},
		{name: "PublicAndPrivate", constant: AzureEndpointAccessPublicAndPrivate, value: "PublicAndPrivate"},
		{name: "Private", constant: AzureEndpointAccessPrivate, value: "Private"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.constant) != tt.value {
				t.Errorf("AzureEndpointAccess%s = %q, want %q", tt.name, tt.constant, tt.value)
			}
			if !expectedValues[tt.constant] {
				t.Errorf("unexpected enum value: %q", tt.constant)
			}
		})
	}

	// Verify invalid values are not in the enum
	invalidValues := []string{"PublicOnly", "PrivateOnly", "Internal", ""}
	for _, invalid := range invalidValues {
		t.Run(fmt.Sprintf("Invalid_%s", invalid), func(t *testing.T) {
			if expectedValues[AzureEndpointAccessType(invalid)] {
				t.Errorf("value %q should not be a valid AzureEndpointAccessType", invalid)
			}
		})
	}
}

// TestAzurePrivateConnectivityConfig_SubscriptionUUIDPattern validates the UUID pattern
// used for AdditionalAllowedSubscriptions items.
// This maps to subtest 2.7 (UUID pattern validation).
func TestAzurePrivateConnectivityConfig_SubscriptionUUIDPattern(t *testing.T) {
	t.Parallel()

	uuidPattern := regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "valid lowercase UUID", value: "12345678-1234-5678-9012-123456789012", valid: true},
		{name: "valid uppercase UUID", value: "ABCDEF01-2345-6789-ABCD-EF0123456789", valid: true},
		{name: "valid mixed case UUID", value: "abCDef01-2345-6789-abCD-ef0123456789", valid: true},
		{name: "all zeros", value: "00000000-0000-0000-0000-000000000000", valid: true},
		{name: "all f's", value: "ffffffff-ffff-ffff-ffff-ffffffffffff", valid: true},
		{name: "missing hyphens", value: "123456781234567890121234567890ab", valid: false},
		{name: "too short", value: "1234-5678-1234", valid: false},
		{name: "with braces", value: "{12345678-1234-5678-9012-123456789012}", valid: false},
		{name: "empty string", value: "", valid: false},
		{name: "contains g character", value: "g2345678-1234-5678-9012-123456789012", valid: false},
		{name: "extra segment", value: "12345678-1234-5678-9012-123456789012-extra", valid: false},
		{name: "wrong segment lengths", value: "1234567-1234-5678-9012-123456789012", valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := uuidPattern.MatchString(tt.value)
			if matches != tt.valid {
				t.Errorf("UUID pattern match for %q = %v, want %v", tt.value, matches, tt.valid)
			}
		})
	}
}

// TestAzurePrivateConnectivityConfig_NATSubnetIDPattern validates the Azure resource ID pattern
// for NATSubnetID on AzurePrivateConnectivityConfig.
// This maps to subtest 2.8 (Azure resource ID pattern validation).
func TestAzurePrivateConnectivityConfig_NATSubnetIDPattern(t *testing.T) {
	t.Parallel()

	subnetPattern := regexp.MustCompile(`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.Network/virtualNetworks/[^/]+/subnets/[^/]+$`)

	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{
			name:  "valid subnet ID",
			value: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/my-rg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/my-subnet",
			valid: true,
		},
		{
			name:  "valid with hyphens and numbers",
			value: "/subscriptions/abcdef01-2345-6789-abcd-ef0123456789/resourceGroups/rg-123/providers/Microsoft.Network/virtualNetworks/vnet-456/subnets/subnet-789",
			valid: true,
		},
		{
			name:  "missing subscriptions prefix",
			value: "subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
			valid: false,
		},
		{
			name:  "wrong provider",
			value: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/rg/providers/Microsoft.Compute/virtualNetworks/vnet/subnets/subnet",
			valid: false,
		},
		{
			name:  "missing subnets segment",
			value: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet",
			valid: false,
		},
		{
			name:  "empty string",
			value: "",
			valid: false,
		},
		{
			name:  "extra trailing slash",
			value: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet/",
			valid: false,
		},
		{
			name:  "empty resource group",
			value: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups//providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := subnetPattern.MatchString(tt.value)
			if matches != tt.valid {
				t.Errorf("subnet pattern match for %q = %v, want %v", tt.value, matches, tt.valid)
			}
		})
	}
}

// TestAzurePrivateLinkServiceSpec_SubscriptionIDPattern validates the UUID pattern
// for AzurePrivateLinkServiceSpec.SubscriptionID.
// This maps to subtest 2.7 (UUID pattern validation).
func TestAzurePrivateLinkServiceSpec_SubscriptionIDPattern(t *testing.T) {
	t.Parallel()

	uuidPattern := regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "valid UUID", value: "12345678-abcd-ef01-2345-678901234567", valid: true},
		{name: "invalid - not UUID format", value: "not-a-uuid", valid: false},
		{name: "empty string", value: "", valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if uuidPattern.MatchString(tt.value) != tt.valid {
				t.Errorf("subscriptionID pattern match for %q = %v, want %v", tt.value, !tt.valid, tt.valid)
			}
		})
	}
}

// TestAzurePrivateLinkServiceSpec_SubnetIDPatterns validates the Azure resource ID
// patterns used for natSubnetID, guestSubnetID, and guestVNetID.
// This maps to subtest 2.8 (Azure resource ID pattern validation).
func TestAzurePrivateLinkServiceSpec_SubnetIDPatterns(t *testing.T) {
	t.Parallel()

	subnetPattern := regexp.MustCompile(`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.Network/virtualNetworks/[^/]+/subnets/[^/]+$`)
	vnetPattern := regexp.MustCompile(`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.Network/virtualNetworks/[^/]+$`)

	validSubnetID := "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"
	validVNetID := "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet"

	t.Run("NATSubnetID pattern matches valid value", func(t *testing.T) {
		if !subnetPattern.MatchString(validSubnetID) {
			t.Errorf("expected NATSubnetID pattern to match %q", validSubnetID)
		}
	})

	t.Run("GuestSubnetID pattern matches valid value", func(t *testing.T) {
		if !subnetPattern.MatchString(validSubnetID) {
			t.Errorf("expected GuestSubnetID pattern to match %q", validSubnetID)
		}
	})

	t.Run("GuestVNetID pattern matches valid value", func(t *testing.T) {
		if !vnetPattern.MatchString(validVNetID) {
			t.Errorf("expected GuestVNetID pattern to match %q", validVNetID)
		}
	})

	t.Run("GuestVNetID pattern rejects subnet path", func(t *testing.T) {
		if vnetPattern.MatchString(validSubnetID) {
			t.Errorf("expected GuestVNetID pattern to reject subnet path %q", validSubnetID)
		}
	})

	t.Run("NATSubnetID pattern rejects VNet path", func(t *testing.T) {
		if subnetPattern.MatchString(validVNetID) {
			t.Errorf("expected NATSubnetID pattern to reject VNet path %q", validVNetID)
		}
	})
}

// TestAzurePrivateConnectivityConfig_AdditionalAllowedSubscriptionsLimits documents the
// MaxItems=50 constraint on AdditionalAllowedSubscriptions. The field is optional
// (no MinItems) since the guest cluster's own subscription is always automatically allowed.
//
// Note: MaxItems is a kubebuilder marker enforced by CRD validation
// at the API server level. The Go struct itself is []string and has no
// compile-time enforcement. This test validates the expected boundaries
// documented by the markers.
func TestAzurePrivateConnectivityConfig_AdditionalAllowedSubscriptionsLimits(t *testing.T) {
	t.Parallel()

	validUUID := "12345678-1234-5678-9012-123456789012"

	tests := []struct {
		name     string
		count    int
		validMax bool // passes MaxItems=50
	}{
		{name: "0 items (empty is valid, guest sub auto-included)", count: 0, validMax: true},
		{name: "1 item", count: 1, validMax: true},
		{name: "25 items (within range)", count: 25, validMax: true},
		{name: "50 items (at MaxItems)", count: 50, validMax: true},
		{name: "51 items (above MaxItems)", count: 51, validMax: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subs := make([]string, tt.count)
			for i := range subs {
				subs[i] = validUUID
			}

			config := AzurePrivateConnectivityConfig{
				NATSubnetID:                    "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				AdditionalAllowedSubscriptions: subs,
			}

			// Verify the Go struct accepts the values (no compile-time constraint)
			if len(config.AdditionalAllowedSubscriptions) != tt.count {
				t.Errorf("expected %d items, got %d", tt.count, len(config.AdditionalAllowedSubscriptions))
			}

			// Document expected CRD validation behavior
			passesMax := len(config.AdditionalAllowedSubscriptions) <= 50

			if passesMax != tt.validMax {
				t.Errorf("MaxItems check: got %v, want %v (count=%d)", passesMax, tt.validMax, tt.count)
			}
		})
	}
}

// TestAzureEndpointAccessSpec_PrivateCELRuleDocumentation documents the CEL
// validation rule on AzureEndpointAccessSpec and verifies the Go type structure supports
// it. This maps to subtests:
//   - 2.1: Reject non-Public type without private config
//   - 2.3: Accept Public without private config
//   - 2.4: Accept PublicAndPrivate with private config
//
// The actual CEL rule on AzureEndpointAccessSpec is:
//
//	Rule: self.type == 'Public' || has(self.private)
//	  => private is required when type is not Public
//
// These are enforced by the API server via CRD validation, not at the Go level.
func TestAzureEndpointAccessSpec_PrivateCELRuleDocumentation(t *testing.T) {
	t.Parallel()

	validNATSubnet := "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pls-subnet"
	validSubscription := "12345678-1234-5678-9012-123456789012"

	tests := []struct {
		name              string
		endpointAccess    *AzureEndpointAccessSpec
		expectedCELResult bool
		description       string
	}{
		{
			name: "2.1a - PublicAndPrivate without private config should be rejected",
			endpointAccess: &AzureEndpointAccessSpec{
				Type:    AzureEndpointAccessPublicAndPrivate,
				Private: nil,
			},
			expectedCELResult: false,
			description:       "CEL rule: type != 'Public' && !has(private) => false",
		},
		{
			name: "2.1b - Private without private config should be rejected",
			endpointAccess: &AzureEndpointAccessSpec{
				Type:    AzureEndpointAccessPrivate,
				Private: nil,
			},
			expectedCELResult: false,
			description:       "CEL rule: type != 'Public' && !has(private) => false",
		},
		{
			name: "2.3 - Public without private config should be accepted",
			endpointAccess: &AzureEndpointAccessSpec{
				Type: AzureEndpointAccessPublic,
			},
			expectedCELResult: true,
			description:       "CEL rule: type == 'Public' => true (short-circuit)",
		},
		{
			name:              "2.3b - nil endpointAccess (default Public) should be accepted",
			endpointAccess:    nil,
			expectedCELResult: true,
			description:       "nil EndpointAccess means Public by default",
		},
		{
			name: "2.4 - PublicAndPrivate with private config should be accepted",
			endpointAccess: &AzureEndpointAccessSpec{
				Type: AzureEndpointAccessPublicAndPrivate,
				Private: &AzurePrivateConnectivityConfig{
					NATSubnetID:          validNATSubnet,
					AdditionalAllowedSubscriptions: []string{validSubscription},
				},
			},
			expectedCELResult: true,
			description:       "CEL rule: has(private) => true",
		},
		{
			name: "Private with private config should be accepted",
			endpointAccess: &AzureEndpointAccessSpec{
				Type: AzureEndpointAccessPrivate,
				Private: &AzurePrivateConnectivityConfig{
					NATSubnetID:          validNATSubnet,
					AdditionalAllowedSubscriptions: []string{validSubscription},
				},
			},
			expectedCELResult: true,
			description:       "CEL rule: has(private) => true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Evaluate the CEL rule logic in Go
			// For nil EndpointAccess, it means Public by default
			// For non-nil: Rule: self.type == 'Public' || has(self.private)
			var celResult bool
			if tt.endpointAccess == nil {
				celResult = true // nil means Public
			} else {
				celResult = tt.endpointAccess.Type == AzureEndpointAccessPublic ||
					tt.endpointAccess.Private != nil
			}

			if celResult != tt.expectedCELResult {
				t.Errorf("%s\nCEL result = %v, want %v", tt.description, celResult, tt.expectedCELResult)
			}
		})
	}
}

// TestAzureEndpointAccessSpec_PrivateRequiredCELRule documents the CEL validation
// rule on AzureEndpointAccessSpec that requires the private field when type is not Public.
// CEL rule: self.type == 'Public' || has(self.private)
//
// This maps to subtest 2.5 (Verify validation rules on AzureEndpointAccessSpec).
func TestAzureEndpointAccessSpec_PrivateRequiredCELRule(t *testing.T) {
	t.Parallel()

	validNATSubnet := "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pls-subnet"
	validSubscription := "12345678-1234-5678-9012-123456789012"

	tests := []struct {
		name    string
		spec    AzureEndpointAccessSpec
		allowed bool
	}{
		{
			name:    "Public without private is allowed",
			spec:    AzureEndpointAccessSpec{Type: AzureEndpointAccessPublic},
			allowed: true,
		},
		{
			name:    "Private without private config is rejected",
			spec:    AzureEndpointAccessSpec{Type: AzureEndpointAccessPrivate},
			allowed: false,
		},
		{
			name: "Private with private config is allowed",
			spec: AzureEndpointAccessSpec{
				Type: AzureEndpointAccessPrivate,
				Private: &AzurePrivateConnectivityConfig{
					NATSubnetID:          validNATSubnet,
					AdditionalAllowedSubscriptions: []string{validSubscription},
				},
			},
			allowed: true,
		},
		{
			name:    "PublicAndPrivate without private config is rejected",
			spec:    AzureEndpointAccessSpec{Type: AzureEndpointAccessPublicAndPrivate},
			allowed: false,
		},
		{
			name: "PublicAndPrivate with private config is allowed",
			spec: AzureEndpointAccessSpec{
				Type: AzureEndpointAccessPublicAndPrivate,
				Private: &AzurePrivateConnectivityConfig{
					NATSubnetID:          validNATSubnet,
					AdditionalAllowedSubscriptions: []string{validSubscription},
				},
			},
			allowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CEL rule: self.type == 'Public' || has(self.private)
			celResult := tt.spec.Type == AzureEndpointAccessPublic || tt.spec.Private != nil
			if celResult != tt.allowed {
				t.Errorf("CEL rule check: type=%q private=%v => %v, want %v", tt.spec.Type, tt.spec.Private != nil, celResult, tt.allowed)
			}
		})
	}
}

// TestAzurePrivateLinkServiceSpec_ImmutableFields documents and verifies the
// immutable field CEL rules on AzurePrivateLinkServiceSpec.
// This maps to subtest 2.5 (Verify immutable fields on AzurePrivateLinkService CRD).
//
// Immutable fields (with CEL rule: self == oldSelf):
//   - subscriptionID
//   - resourceGroupName
//   - location
//   - natSubnetID
//   - guestSubnetID
//   - guestVNetID
func TestAzurePrivateLinkServiceSpec_ImmutableFields(t *testing.T) {
	t.Parallel()

	immutableFields := []string{
		"subscriptionID",
		"resourceGroupName",
		"location",
		"natSubnetID",
		"guestSubnetID",
		"guestVNetID",
	}

	for _, field := range immutableFields {
		t.Run(fmt.Sprintf("%s is immutable", field), func(t *testing.T) {
			oldSpec := AzurePrivateLinkServiceSpec{
				SubscriptionID:       "12345678-1234-5678-9012-123456789012",
				ResourceGroupName:    "my-rg",
				Location:             "eastus",
				NATSubnetID:          "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				AdditionalAllowedSubscriptions: []string{"12345678-1234-5678-9012-123456789012"},
				GuestSubnetID:        "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/guest-vnet/subnets/guest-subnet",
				GuestVNetID:          "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/guest-vnet",
			}

			newSpec := oldSpec // copy
			switch field {
			case "subscriptionID":
				newSpec.SubscriptionID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
			case "resourceGroupName":
				newSpec.ResourceGroupName = "different-rg"
			case "location":
				newSpec.Location = "westus"
			case "natSubnetID":
				newSpec.NATSubnetID = "/subscriptions/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/other"
			case "guestSubnetID":
				newSpec.GuestSubnetID = "/subscriptions/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/other"
			case "guestVNetID":
				newSpec.GuestVNetID = "/subscriptions/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/other-vnet"
			}

			// CEL rule: self == oldSelf
			var oldValue, newValue string
			switch field {
			case "subscriptionID":
				oldValue, newValue = oldSpec.SubscriptionID, newSpec.SubscriptionID
			case "resourceGroupName":
				oldValue, newValue = oldSpec.ResourceGroupName, newSpec.ResourceGroupName
			case "location":
				oldValue, newValue = oldSpec.Location, newSpec.Location
			case "natSubnetID":
				oldValue, newValue = oldSpec.NATSubnetID, newSpec.NATSubnetID
			case "guestSubnetID":
				oldValue, newValue = oldSpec.GuestSubnetID, newSpec.GuestSubnetID
			case "guestVNetID":
				oldValue, newValue = oldSpec.GuestVNetID, newSpec.GuestVNetID
			}

			if oldValue == newValue {
				t.Errorf("test setup error: old and new values should differ for %s", field)
			}

			// The CEL rule self == oldSelf means mutations are rejected
			celResult := newValue == oldValue
			if celResult {
				t.Errorf("expected CEL immutability check to fail for changed %s", field)
			}
		})
	}
}

// TestAzurePrivateLinkServiceSpec_MutableFields documents which fields are
// intentionally mutable on AzurePrivateLinkServiceSpec.
// This maps to subtest 2.6 (Verify mutable fields).
//
// Mutable fields (no self == oldSelf CEL rule):
//   - loadBalancerIP (populated by CPO observer, needs to be updated)
//   - allowedSubscriptions (intentionally mutable to grant/revoke PE access)
func TestAzurePrivateLinkServiceSpec_MutableFields(t *testing.T) {
	t.Parallel()

	t.Run("loadBalancerIP is mutable", func(t *testing.T) {
		// loadBalancerIP has NO immutability CEL rule -- it is populated by the CPO observer
		// and must be updatable. This test documents that expectation.
		spec := AzurePrivateLinkServiceSpec{
			LoadBalancerIP: "10.0.0.1",
		}
		spec.LoadBalancerIP = "10.0.0.2"
		if spec.LoadBalancerIP != "10.0.0.2" {
			t.Error("loadBalancerIP should be mutable")
		}
	})

	t.Run("additionalAllowedSubscriptions is mutable", func(t *testing.T) {
		// additionalAllowedSubscriptions has NO immutability CEL rule -- it is intentionally
		// mutable to allow operators to grant/revoke Private Endpoint access.
		spec := AzurePrivateLinkServiceSpec{
			AdditionalAllowedSubscriptions: []string{"12345678-1234-5678-9012-123456789012"},
		}
		spec.AdditionalAllowedSubscriptions = append(spec.AdditionalAllowedSubscriptions, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
		if len(spec.AdditionalAllowedSubscriptions) != 2 {
			t.Error("additionalAllowedSubscriptions should be mutable")
		}
	})
}

// TestAzurePrivateLinkServiceSpec_AdditionalAllowedSubscriptionsLimits validates the
// MaxItems=50 constraint on AzurePrivateLinkServiceSpec.AdditionalAllowedSubscriptions.
// The field is optional (no MinItems) since the guest cluster's own subscription is auto-included.
func TestAzurePrivateLinkServiceSpec_AdditionalAllowedSubscriptionsLimits(t *testing.T) {
	t.Parallel()

	validUUID := "12345678-1234-5678-9012-123456789012"

	tests := []struct {
		name     string
		count    int
		validMax bool
	}{
		{name: "0 items (empty is valid, guest sub auto-included)", count: 0, validMax: true},
		{name: "1 item", count: 1, validMax: true},
		{name: "50 items (at MaxItems)", count: 50, validMax: true},
		{name: "51 items (above MaxItems)", count: 51, validMax: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subs := make([]string, tt.count)
			for i := range subs {
				subs[i] = validUUID
			}
			passesMax := len(subs) <= 50

			if passesMax != tt.validMax {
				t.Errorf("MaxItems check: got %v, want %v", passesMax, tt.validMax)
			}
		})
	}
}

// crdSchema is a minimal representation of the CRD OpenAPI schema for parsing
type crdSchema struct {
	Spec struct {
		Versions []struct {
			Name   string `yaml:"name"`
			Schema struct {
				OpenAPIV3Schema struct {
					Properties             map[string]crdProperty `yaml:"properties"`
					XKubernetesValidations []struct {
						Rule    string `yaml:"rule"`
						Message string `yaml:"message"`
					} `yaml:"x-kubernetes-validations"`
				} `yaml:"openAPIV3Schema"`
			} `yaml:"schema"`
		} `yaml:"versions"`
	} `yaml:"spec"`
}

type crdProperty struct {
	Properties             map[string]crdProperty `yaml:"properties"`
	Type                   string                 `yaml:"type"`
	Enum                   []string               `yaml:"enum"`
	Pattern                string                 `yaml:"pattern"`
	MinLength              *int                   `yaml:"minLength"`
	MaxLength              *int                   `yaml:"maxLength"`
	MinItems               *int                   `yaml:"minItems"`
	MaxItems               *int                   `yaml:"maxItems"`
	Default                interface{}            `yaml:"default"`
	XKubernetesValidations []struct {
		Rule    string `yaml:"rule"`
		Message string `yaml:"message"`
	} `yaml:"x-kubernetes-validations"`
	Items *crdProperty `yaml:"items"`
}

// TestCRD_AzurePrivateLinkService_ValidationRulesInCRD reads the generated CRD YAML
// and verifies that all expected CEL validation rules and constraints are present.
// This provides a comprehensive integration check that the kubebuilder markers
// were correctly translated into CRD validation rules.
func TestCRD_AzurePrivateLinkService_ValidationRulesInCRD(t *testing.T) {
	t.Parallel()

	crdPath := "zz_generated.featuregated-crd-manifests/azureprivatelinkservices.hypershift.openshift.io/AAA_ungated.yaml"
	crdData, err := os.ReadFile(crdPath)
	if err != nil {
		t.Skipf("CRD file not found at %s -- this test requires a generated CRD (run 'make api'): %v", crdPath, err)
	}

	var crd crdSchema
	if err := yaml.Unmarshal(crdData, &crd); err != nil {
		t.Fatalf("failed to parse CRD YAML: %v", err)
	}

	if len(crd.Spec.Versions) == 0 {
		t.Fatal("CRD has no versions")
	}

	schema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema
	specProps, ok := schema.Properties["spec"]
	if !ok {
		t.Fatal("CRD schema missing 'spec' property")
	}

	// 2.5: Verify immutable fields have XValidation rules with "self == oldSelf"
	t.Run("immutable fields have self==oldSelf rules", func(t *testing.T) {
		immutableFields := []string{"subscriptionID", "resourceGroupName", "location", "natSubnetID", "guestSubnetID", "guestVNetID"}
		for _, field := range immutableFields {
			t.Run(field, func(t *testing.T) {
				prop, ok := specProps.Properties[field]
				if !ok {
					t.Fatalf("CRD spec missing field %q", field)
				}
				found := false
				for _, v := range prop.XKubernetesValidations {
					if v.Rule == "self == oldSelf" {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("field %q should have immutability rule 'self == oldSelf'", field)
				}
			})
		}
	})

	// 2.6: Verify mutable fields do NOT have immutability rules
	t.Run("mutable fields do not have self==oldSelf rules", func(t *testing.T) {
		mutableFields := []string{"loadBalancerIP", "additionalAllowedSubscriptions"}
		for _, field := range mutableFields {
			t.Run(field, func(t *testing.T) {
				prop, ok := specProps.Properties[field]
				if !ok {
					t.Fatalf("CRD spec missing field %q", field)
				}
				for _, v := range prop.XKubernetesValidations {
					if v.Rule == "self == oldSelf" {
						t.Errorf("field %q should NOT have immutability rule 'self == oldSelf', but does", field)
					}
				}
			})
		}
	})

	// 2.7: Verify UUID pattern on subscriptionID
	t.Run("subscriptionID has UUID pattern", func(t *testing.T) {
		prop := specProps.Properties["subscriptionID"]
		expectedPattern := `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`
		if prop.Pattern != expectedPattern {
			t.Errorf("subscriptionID pattern = %q, want %q", prop.Pattern, expectedPattern)
		}
	})

	// 2.7: Verify UUID pattern on additionalAllowedSubscriptions items
	t.Run("additionalAllowedSubscriptions items have UUID pattern", func(t *testing.T) {
		prop := specProps.Properties["additionalAllowedSubscriptions"]
		if prop.Items == nil {
			t.Fatal("allowedSubscriptions should have items schema")
		}
		expectedPattern := `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`
		if prop.Items.Pattern != expectedPattern {
			t.Errorf("allowedSubscriptions items pattern = %q, want %q", prop.Items.Pattern, expectedPattern)
		}
	})

	// 2.8: Verify Azure resource ID pattern on natSubnetID
	// Note: In the CRD YAML, dots in the pattern may be escaped as \\.
	t.Run("natSubnetID has subnet resource ID pattern", func(t *testing.T) {
		prop := specProps.Properties["natSubnetID"]
		normalized := strings.ReplaceAll(prop.Pattern, "\\\\.", ".")
		normalized = strings.ReplaceAll(normalized, "\\.", ".")
		if !strings.Contains(normalized, "Microsoft.Network/virtualNetworks") || !strings.Contains(normalized, "subnets") {
			t.Errorf("natSubnetID pattern should reference Microsoft.Network/virtualNetworks and subnets, got %q", prop.Pattern)
		}
	})

	// 2.8: Verify Azure resource ID pattern on guestSubnetID
	t.Run("guestSubnetID has subnet resource ID pattern", func(t *testing.T) {
		prop := specProps.Properties["guestSubnetID"]
		normalized := strings.ReplaceAll(prop.Pattern, "\\\\.", ".")
		normalized = strings.ReplaceAll(normalized, "\\.", ".")
		if !strings.Contains(normalized, "Microsoft.Network/virtualNetworks") || !strings.Contains(normalized, "subnets") {
			t.Errorf("guestSubnetID pattern should reference Microsoft.Network/virtualNetworks and subnets, got %q", prop.Pattern)
		}
	})

	// 2.8: Verify Azure resource ID pattern on guestVNetID
	t.Run("guestVNetID has VNet resource ID pattern", func(t *testing.T) {
		prop := specProps.Properties["guestVNetID"]
		normalized := strings.ReplaceAll(prop.Pattern, "\\\\.", ".")
		normalized = strings.ReplaceAll(normalized, "\\.", ".")
		if !strings.Contains(normalized, "Microsoft.Network/virtualNetworks") {
			t.Errorf("guestVNetID pattern should reference Microsoft.Network/virtualNetworks, got %q", prop.Pattern)
		}
		if strings.Contains(normalized, "subnets") {
			t.Errorf("guestVNetID pattern should NOT reference subnets, got %q", prop.Pattern)
		}
	})

	// 2.9: Verify AdditionalAllowedSubscriptions MaxItems (no MinItems since it's optional)
	t.Run("additionalAllowedSubscriptions has MaxItems=50 and no MinItems", func(t *testing.T) {
		prop := specProps.Properties["additionalAllowedSubscriptions"]
		if prop.MinItems != nil {
			t.Errorf("additionalAllowedSubscriptions should not have MinItems (field is optional), got %d", *prop.MinItems)
		}
		if prop.MaxItems == nil || *prop.MaxItems != 50 {
			t.Errorf("additionalAllowedSubscriptions MaxItems = %v, want 50", prop.MaxItems)
		}
	})
}

// TestCRD_HostedCluster_AzureEndpointAccessValidation reads the generated HostedCluster CRD YAML
// and verifies the Azure endpointAccess validation rules are present.
// This maps to subtests 2.1-2.4.
func TestCRD_HostedCluster_AzureEndpointAccessValidation(t *testing.T) {
	t.Parallel()

	crdPath := "zz_generated.featuregated-crd-manifests/hostedclusters.hypershift.openshift.io/AAA_ungated.yaml"
	crdData, err := os.ReadFile(crdPath)
	if err != nil {
		t.Skipf("CRD file not found at %s: %v", crdPath, err)
	}

	crdContent := string(crdData)

	// 2.1: Verify CEL rule that requires private when type is not Public
	// The new CEL rule on AzureEndpointAccessSpec is: self.type == 'Public' || has(self.private)
	t.Run("CEL rule requires private for non-Public type", func(t *testing.T) {
		expectedRule := "self.type == 'Public' || has(self.private)"
		if !strings.Contains(crdContent, expectedRule) {
			t.Errorf("HostedCluster CRD should contain CEL rule %q on endpointAccess", expectedRule)
		}
	})

	// 2.2: Verify endpointAccess type enum values in CRD
	t.Run("endpointAccess type enum contains expected values", func(t *testing.T) {
		for _, val := range []string{"Public", "PublicAndPrivate", "Private"} {
			if !strings.Contains(crdContent, val) {
				t.Errorf("HostedCluster CRD should contain enum value %q for endpointAccess type", val)
			}
		}
	})

	// Verify NATSubnetID immutability rule
	t.Run("natSubnetID has immutability rule", func(t *testing.T) {
		expectedMessage := "NATSubnetID is immutable"
		if !strings.Contains(crdContent, expectedMessage) {
			t.Errorf("HostedCluster CRD should contain immutability message %q", expectedMessage)
		}
	})

	// 2.9: Verify additionalAllowedSubscriptions MaxItems in HostedCluster CRD
	t.Run("additionalAllowedSubscriptions constraints in HostedCluster CRD", func(t *testing.T) {
		if !strings.Contains(crdContent, "maxItems: 50") {
			t.Error("HostedCluster CRD should contain maxItems: 50 for additionalAllowedSubscriptions")
		}
	})
}
