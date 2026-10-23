package metrics

import (
	"fmt"
	"os"

	"github.com/openshift/hypershift/support/rhobsmonitoring"
)

// MonitoringAPIGroup selects which monitoring API group to use for emitting monitors.
type MonitoringAPIGroup string

const (
	MonitoringAPIGroupCoreOS MonitoringAPIGroup = "monitoring.coreos.com"
	MonitoringAPIGroupGMP    MonitoringAPIGroup = "monitoring.googleapis.com"
	MonitoringAPIGroupEnvVar                    = "MONITORING_API_GROUP"
)

// MonitoringAPIGroupFromEnv reads MONITORING_API_GROUP from the environment, defaulting to
// monitoring.coreos.com when unset.
func MonitoringAPIGroupFromEnv() (MonitoringAPIGroup, error) {
	return ParseMonitoringAPIGroup(os.Getenv(MonitoringAPIGroupEnvVar), os.Getenv(rhobsmonitoring.EnvironmentVariable) == "1")
}

// ParseMonitoringAPIGroup defaults an empty value to CoreOS and rejects unsupported
// groups and GMP combined with RHOBS monitoring.
func ParseMonitoringAPIGroup(val string, rhobsMonitoring bool) (MonitoringAPIGroup, error) {
	if val == "" {
		return MonitoringAPIGroupCoreOS, nil
	}
	switch MonitoringAPIGroup(val) {
	case MonitoringAPIGroupCoreOS:
		return MonitoringAPIGroupCoreOS, nil
	case MonitoringAPIGroupGMP:
		if rhobsMonitoring {
			return "", fmt.Errorf("%s=%s cannot be used with RHOBS monitoring; unset %s and disable --rhobs-monitoring to use GMP", MonitoringAPIGroupEnvVar, MonitoringAPIGroupGMP, rhobsmonitoring.EnvironmentVariable)
		}
		return MonitoringAPIGroup(val), nil
	default:
		return "", fmt.Errorf("unsupported %s value %q: must be %q or %q", MonitoringAPIGroupEnvVar, val, MonitoringAPIGroupCoreOS, MonitoringAPIGroupGMP)
	}
}
