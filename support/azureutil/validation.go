package azureutil

import "fmt"

// ValidateAzureResourceName checks that a constructed Azure resource name does not
// exceed the Azure maximum of 80 characters. It returns an error if the name is too long.
func ValidateAzureResourceName(name, resourceType string) error {
	if len(name) > AzureResourceNameMaxLength {
		return fmt.Errorf("%s name %q exceeds Azure maximum of %d characters (got %d)", resourceType, name, AzureResourceNameMaxLength, len(name))
	}
	return nil
}
