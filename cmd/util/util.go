package util

import (
	"fmt"
)

// ValidateRequiredOption returns a cobra style error message when the flag value is empty
func ValidateRequiredOption(flag string, value string) error {
	if len(value) == 0 {
		return fmt.Errorf("required flag(s) \"%s\" not set", flag)
	}
	return nil
}
