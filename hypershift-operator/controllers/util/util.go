package util

import (
	"strings"

	"k8s.io/apimachinery/pkg/types"
)

// ParseNamespacedName expects a string with the format "namespace/name"
// and returns the proper types.NamespacedName.
// This is useful when watching a CR annotated with the format above to requeue the CR
// described in the annotation.
func ParseNamespacedName(name string) types.NamespacedName {
	parts := strings.SplitN(name, string(types.Separator), 2)
	if len(parts) > 1 {
		return types.NamespacedName{Namespace: parts[0], Name: parts[1]}
	}
	return types.NamespacedName{Name: parts[0]}
}

// True - Returns bool pointer type - *true
func True() *bool {
	a := true
	return &a
}

// False - Returns bool pointer type - *false
func False() *bool {
	a := false
	return &a
}
