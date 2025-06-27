package util

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestMapsDiff(t *testing.T) {
	g := NewWithT(t)
	tests := []struct {
		name              string
		current           map[string]string
		input             map[string]string
		expectedChanged   map[string]string
		expectedDeleted   map[string]string
		expectedDifferent bool
	}{
		{
			name:              "Nil maps",
			current:           nil,
			input:             nil,
			expectedChanged:   map[string]string{},
			expectedDeleted:   map[string]string{},
			expectedDifferent: false,
		},
		{
			name:              "Nil current, non-empty input",
			current:           nil,
			input:             map[string]string{"x": "y"},
			expectedChanged:   map[string]string{"x": "y"},
			expectedDeleted:   map[string]string{},
			expectedDifferent: true,
		},
		{
			name:              "Nil input, non-empty current",
			current:           map[string]string{"x": "y"},
			input:             nil,
			expectedChanged:   map[string]string{},
			expectedDeleted:   map[string]string{"x": "y"},
			expectedDifferent: true,
		},
		{
			name:              "Multiple changes and deletions",
			current:           map[string]string{"a": "1", "b": "2", "c": "3"},
			input:             map[string]string{"a": "2", "d": "4"},
			expectedChanged:   map[string]string{"a": "2", "d": "4"},
			expectedDeleted:   map[string]string{"b": "2", "c": "3"},
			expectedDifferent: true,
		},
		{
			name:              "Empty string values",
			current:           map[string]string{"a": "", "b": "2"},
			input:             map[string]string{"a": "", "b": ""},
			expectedChanged:   map[string]string{"b": ""},
			expectedDeleted:   map[string]string{},
			expectedDifferent: true,
		},
		{
			name:              "Same keys, different order",
			current:           map[string]string{"a": "1", "b": "2"},
			input:             map[string]string{"b": "2", "a": "1"},
			expectedChanged:   map[string]string{},
			expectedDeleted:   map[string]string{},
			expectedDifferent: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			changed, deleted, different := MapsDiff(tc.current, tc.input)
			g.Expect(changed).To(Equal(tc.expectedChanged), "Changed: expected %v, got %v", tc.expectedChanged, changed)
			g.Expect(deleted).To(Equal(tc.expectedDeleted), "Deleted: expected %v, got %v", tc.expectedDeleted, deleted)
			g.Expect(different).To(Equal(tc.expectedDifferent), "Different: expected %v, got %v", tc.expectedDifferent, different)
		})
	}
}
