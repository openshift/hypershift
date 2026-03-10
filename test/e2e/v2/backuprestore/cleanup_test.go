//go:build e2ev2 && backuprestore

package backuprestore

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSupportsVerb(t *testing.T) {
	tests := []struct {
		name     string
		verbs    metav1.Verbs
		verb     string
		expected bool
	}{
		{
			name:     "verb exists in list",
			verbs:    metav1.Verbs{"get", "list", "watch", "create", "update", "delete"},
			verb:     "list",
			expected: true,
		},
		{
			name:     "verb does not exist in list",
			verbs:    metav1.Verbs{"get", "list", "watch"},
			verb:     "delete",
			expected: false,
		},
		{
			name:     "empty verb string",
			verbs:    metav1.Verbs{"get", "list"},
			verb:     "",
			expected: false,
		},
		{
			name:     "wildcard verb exists",
			verbs:    metav1.Verbs{"*"},
			verb:     "*",
			expected: true,
		},
		{
			name:     "wildcard verb does not match specific verb",
			verbs:    metav1.Verbs{"*"},
			verb:     "get",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := supportsVerb(tt.verbs, tt.verb)
			if result != tt.expected {
				t.Errorf("supportsVerb(%v, %q) = %v, want %v", tt.verbs, tt.verb, result, tt.expected)
			}
		})
	}
}
