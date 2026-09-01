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
			name:              "When both maps are nil, it should return empty changes and no difference",
			current:           nil,
			input:             nil,
			expectedChanged:   map[string]string{},
			expectedDeleted:   map[string]string{},
			expectedDifferent: false,
		},
		{
			name:              "When current is nil and input is non-empty, it should return all input as changed",
			current:           nil,
			input:             map[string]string{"x": "y"},
			expectedChanged:   map[string]string{"x": "y"},
			expectedDeleted:   map[string]string{},
			expectedDifferent: true,
		},
		{
			name:              "When input is nil and current is non-empty, it should return all current as deleted",
			current:           map[string]string{"x": "y"},
			input:             nil,
			expectedChanged:   map[string]string{},
			expectedDeleted:   map[string]string{"x": "y"},
			expectedDifferent: true,
		},
		{
			name:              "When maps have multiple changes and deletions, it should detect all differences",
			current:           map[string]string{"a": "1", "b": "2", "c": "3"},
			input:             map[string]string{"a": "2", "d": "4"},
			expectedChanged:   map[string]string{"a": "2", "d": "4"},
			expectedDeleted:   map[string]string{"b": "2", "c": "3"},
			expectedDifferent: true,
		},
		{
			name:              "When maps have empty string values, it should handle them correctly",
			current:           map[string]string{"a": "", "b": "2"},
			input:             map[string]string{"a": "", "b": ""},
			expectedChanged:   map[string]string{"b": ""},
			expectedDeleted:   map[string]string{},
			expectedDifferent: true,
		},
		{
			name:              "When maps have same keys in different order, it should return no difference",
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
