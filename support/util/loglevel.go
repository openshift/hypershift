package util

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// LogLevelToKlogVerbosity maps a LogLevel enum value to a klog verbosity integer.
// Returns 2 (Normal) for unrecognized or empty values.
func LogLevelToKlogVerbosity(level *hyperv1.LogLevel) int {
	if level == nil {
		return 2
	}
	switch *level {
	case hyperv1.Debug:
		return 4
	case hyperv1.Trace:
		return 6
	case hyperv1.TraceAll:
		return 8
	default:
		return 2
	}
}
