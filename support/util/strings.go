package util

import (
	"strings"
)

// Contains checks if a comma-delimited string contains a specific string.
func StringListContains(list string, s string) bool {
	slice := strings.Split(list, ",")
	for _, a := range slice {
		if strings.Trim(a, " ") == s {
			return true
		}
	}
	return false
}
