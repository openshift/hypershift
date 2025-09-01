package awsutil

import (
	"os"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestFindResourceTagByKey(t *testing.T) {
	tests := []struct {
		name     string
		tags     []hyperv1.AWSResourceTag
		key      string
		expected *hyperv1.AWSResourceTag
	}{
		{
			name: "find existing tag",
			tags: []hyperv1.AWSResourceTag{
				{Key: "key1", Value: "value1"},
				{Key: "key2", Value: "value2"},
			},
			key:      "key1",
			expected: &hyperv1.AWSResourceTag{Key: "key1", Value: "value1"},
		},
		{
			name: "tag not found",
			tags: []hyperv1.AWSResourceTag{
				{Key: "key1", Value: "value1"},
			},
			key:      "nonexistent",
			expected: nil,
		},
		{
			name:     "empty tags slice",
			tags:     []hyperv1.AWSResourceTag{},
			key:      "key1",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FindResourceTagByKey(tt.tags, tt.key)
			if tt.expected == nil {
				if result != nil {
					t.Errorf("Expected nil, got %+v", result)
				}
			} else {
				if result == nil {
					t.Errorf("Expected %+v, got nil", tt.expected)
				} else if result.Key != tt.expected.Key || result.Value != tt.expected.Value {
					t.Errorf("Expected %+v, got %+v", tt.expected, result)
				}
			}
		})
	}
}

func TestHasResourceTagWithValue(t *testing.T) {
	tests := []struct {
		name     string
		tags     []hyperv1.AWSResourceTag
		key      string
		value    string
		expected bool
	}{
		{
			name: "tag exists with correct value",
			tags: []hyperv1.AWSResourceTag{
				{Key: "key1", Value: "value1"},
				{Key: "key2", Value: "value2"},
			},
			key:      "key1",
			value:    "value1",
			expected: true,
		},
		{
			name: "tag exists with wrong value",
			tags: []hyperv1.AWSResourceTag{
				{Key: "key1", Value: "value1"},
			},
			key:      "key1",
			value:    "wrongvalue",
			expected: false,
		},
		{
			name: "tag does not exist",
			tags: []hyperv1.AWSResourceTag{
				{Key: "key1", Value: "value1"},
			},
			key:      "nonexistent",
			value:    "value1",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasResourceTagWithValue(tt.tags, tt.key, tt.value)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestGetResourceTagValue(t *testing.T) {
	tests := []struct {
		name     string
		tags     []hyperv1.AWSResourceTag
		key      string
		expected string
	}{
		{
			name: "get existing tag value",
			tags: []hyperv1.AWSResourceTag{
				{Key: "key1", Value: "value1"},
				{Key: "key2", Value: "value2"},
			},
			key:      "key1",
			expected: "value1",
		},
		{
			name: "tag not found",
			tags: []hyperv1.AWSResourceTag{
				{Key: "key1", Value: "value1"},
			},
			key:      "nonexistent",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetResourceTagValue(tt.tags, tt.key)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestIsROSAHCPFromTags(t *testing.T) {
	tests := []struct {
		name     string
		tags     []hyperv1.AWSResourceTag
		expected bool
	}{
		{
			name: "ROSA HCP tags present",
			tags: []hyperv1.AWSResourceTag{
				{Key: "red-hat-clustertype", Value: "rosa"},
				{Key: "other-tag", Value: "other-value"},
			},
			expected: true,
		},
		{
			name: "ROSA HCP tag with wrong value",
			tags: []hyperv1.AWSResourceTag{
				{Key: "red-hat-clustertype", Value: "not-rosa"},
			},
			expected: false,
		},
		{
			name: "ROSA HCP tag not present",
			tags: []hyperv1.AWSResourceTag{
				{Key: "other-tag", Value: "other-value"},
			},
			expected: false,
		},
		{
			name:     "empty tags",
			tags:     []hyperv1.AWSResourceTag{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsROSAHCPFromTags(tt.tags)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestIsROSAHCP(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected bool
	}{
		{
			name:     "ROSA HCP environment variable set",
			envValue: hyperv1.RosaHCP,
			expected: true,
		},
		{
			name:     "different environment variable value",
			envValue: "ARO-HCP",
			expected: false,
		},
		{
			name:     "environment variable not set",
			envValue: "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variable for test
			if tt.envValue != "" {
				os.Setenv("MANAGED_SERVICE", tt.envValue)
				defer os.Unsetenv("MANAGED_SERVICE")
			} else {
				os.Unsetenv("MANAGED_SERVICE")
			}

			result := IsROSAHCP()
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}
