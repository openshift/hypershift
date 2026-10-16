package util

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// LogLevelToKlogVerbosity maps a LogLevel enum value to a klog verbosity integer.
// The LogLevel enum is the source of truth; every value must be handled here.
// An unhandled value panics so that expanding the enum without updating this
// switch is caught loudly rather than silently mishandled.
func LogLevelToKlogVerbosity(level hyperv1.LogLevel) int {
	switch level {
	case hyperv1.Normal, "":
		return 2
	case hyperv1.Debug:
		return 4
	case hyperv1.Trace:
		return 6
	case hyperv1.TraceAll:
		return 8
	default:
		panic(fmt.Sprintf("unhandled LogLevel %q in LogLevelToKlogVerbosity; update the switch", level))
	}
}

// LogLevelToEtcdLevel maps a LogLevel enum to the ETCD_LOG_LEVEL environment
// variable. etcd only supports "info" and "debug"; Trace and TraceAll are not
// valid for etcd and are rejected at the API level (CEL), so they are treated as
// unhandled here and panic. An unhandled value panics so that expanding the enum
// without updating this switch is caught loudly rather than silently mishandled.
func LogLevelToEtcdLevel(level hyperv1.LogLevel) string {
	switch level {
	case hyperv1.Normal, "":
		return "info"
	case hyperv1.Debug:
		return "debug"
	default:
		panic(fmt.Sprintf("unhandled LogLevel %q in LogLevelToEtcdLevel; update the switch", level))
	}
}
