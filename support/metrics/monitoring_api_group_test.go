package metrics

import (
	"testing"

	"github.com/openshift/hypershift/support/rhobsmonitoring"

	"github.com/stretchr/testify/require"
)

func TestParseMonitoringAPIGroup(t *testing.T) {
	for _, tc := range []struct {
		name          string
		value         string
		rhobs         bool
		want          MonitoringAPIGroup
		errorContains string
	}{
		{name: "When the value is empty, it should default to CoreOS", want: MonitoringAPIGroupCoreOS},
		{name: "When CoreOS is selected, it should accept CoreOS", value: string(MonitoringAPIGroupCoreOS), want: MonitoringAPIGroupCoreOS},
		{name: "When GMP is selected, it should accept GMP", value: string(MonitoringAPIGroupGMP), want: MonitoringAPIGroupGMP},
		{name: "When an unsupported group is selected, it should reject it", value: "monitoring.rhobs", errorContains: "unsupported MONITORING_API_GROUP value"},
		{name: "When the group contains whitespace, it should reject it", value: " monitoring.googleapis.com", errorContains: "unsupported MONITORING_API_GROUP value"},
		{name: "When RHOBS uses the default, it should retain CoreOS selection", rhobs: true, want: MonitoringAPIGroupCoreOS},
		{name: "When RHOBS uses explicit CoreOS, it should accept it", value: string(MonitoringAPIGroupCoreOS), rhobs: true, want: MonitoringAPIGroupCoreOS},
		{name: "When RHOBS and GMP are selected, it should reject the conflict", value: string(MonitoringAPIGroupGMP), rhobs: true, errorContains: "cannot be used with RHOBS monitoring"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseMonitoringAPIGroup(tc.value, tc.rhobs)
			if tc.errorContains != "" {
				require.ErrorContains(t, err, tc.errorContains)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, got)
		})
	}
}

func TestMonitoringAPIGroupFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name, value, rhobs string
		want               MonitoringAPIGroup
		errorContains      string
	}{
		{name: "When the environment is empty, it should default to CoreOS", want: MonitoringAPIGroupCoreOS},
		{name: "When the environment selects GMP, it should return GMP", value: string(MonitoringAPIGroupGMP), want: MonitoringAPIGroupGMP},
		{name: "When the environment selects an invalid group, it should fail", value: "unsupported", errorContains: "unsupported MONITORING_API_GROUP value"},
		{name: "When RHOBS is enabled with GMP, it should fail", value: string(MonitoringAPIGroupGMP), rhobs: "1", errorContains: "cannot be used with RHOBS monitoring"},
		{name: "When RHOBS has an inactive value with GMP, it should accept GMP", value: string(MonitoringAPIGroupGMP), rhobs: "true", want: MonitoringAPIGroupGMP},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(MonitoringAPIGroupEnvVar, tc.value)
			t.Setenv(rhobsmonitoring.EnvironmentVariable, tc.rhobs)
			got, err := MonitoringAPIGroupFromEnv()
			if tc.errorContains != "" {
				require.ErrorContains(t, err, tc.errorContains)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, got)
		})
	}
}
