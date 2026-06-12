package awsutil

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestShouldIgnoreRevokePermissionError(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected bool
	}{
		{
			name:     "When error code is InvalidPermission.NotFound it should return true",
			code:     InvalidPermissionNotFound,
			expected: true,
		},
		{
			name:     "When error code is DependencyViolation it should return false",
			code:     DependencyViolation,
			expected: false,
		},
		{
			name:     "When error code is empty it should return false",
			code:     "",
			expected: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(ShouldIgnoreRevokePermissionError(tc.code)).To(Equal(tc.expected))
		})
	}
}

func TestShouldIgnoreSGNotFound(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected bool
	}{
		{
			name:     "When error code is InvalidGroup.NotFound it should return true",
			code:     InvalidGroupNotFound,
			expected: true,
		},
		{
			name:     "When error code is DependencyViolation it should return false",
			code:     DependencyViolation,
			expected: false,
		},
		{
			name:     "When error code is empty it should return false",
			code:     "",
			expected: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(ShouldIgnoreSGNotFound(tc.code)).To(Equal(tc.expected))
		})
	}
}
