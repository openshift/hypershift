package internal

import "strings"

const (
	DeltaPrefix    = "\u2206"
	AltDeltaPrefix = "\u0394"
)

func HasDeltaPrefix(name string) bool {
	return strings.HasPrefix(name, DeltaPrefix) || strings.HasPrefix(name, AltDeltaPrefix)
}

// Gets a delta counter name prefixed with ∆.
func DeltaCounterName(name string) string {
	if HasDeltaPrefix(name) {
		return name
	}
	return DeltaPrefix + name
}
