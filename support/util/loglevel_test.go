package util

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestLogLevelToKlogVerbosity(t *testing.T) {
	logLevel := func(l hyperv1.LogLevel) *hyperv1.LogLevel { return &l }

	tests := []struct {
		name     string
		level    *hyperv1.LogLevel
		expected int
	}{
		{
			name:     "When LogLevel is nil it should default to verbosity 2",
			level:    nil,
			expected: 2,
		},
		{
			name:     "When LogLevel is Normal it should return verbosity 2",
			level:    logLevel(hyperv1.Normal),
			expected: 2,
		},
		{
			name:     "When LogLevel is Debug it should return verbosity 4",
			level:    logLevel(hyperv1.Debug),
			expected: 4,
		},
		{
			name:     "When LogLevel is Trace it should return verbosity 6",
			level:    logLevel(hyperv1.Trace),
			expected: 6,
		},
		{
			name:     "When LogLevel is TraceAll it should return verbosity 8",
			level:    logLevel(hyperv1.TraceAll),
			expected: 8,
		},
		{
			name:     "When LogLevel is empty string it should default to verbosity 2",
			level:    logLevel(hyperv1.LogLevel("")),
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(LogLevelToKlogVerbosity(tt.level)).To(Equal(tt.expected))
		})
	}
}
