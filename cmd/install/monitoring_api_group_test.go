package install

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/rhobsmonitoring"

	"github.com/stretchr/testify/require"
)

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		name          string
		option        metrics.MonitoringAPIGroup
		env, rhobsEnv string
		rhobsFlag     bool
		additionalEnv map[string]string
		want          metrics.MonitoringAPIGroup
		errorContains string
	}{
		{name: "When monitoring selection is unset, it should default to CoreOS", want: metrics.MonitoringAPIGroupCoreOS},
		{name: "When CoreOS is pre-populated, it should accept CoreOS", option: metrics.MonitoringAPIGroupCoreOS, want: metrics.MonitoringAPIGroupCoreOS},
		{name: "When GMP is pre-populated, it should accept GMP", option: metrics.MonitoringAPIGroupGMP, want: metrics.MonitoringAPIGroupGMP},
		{name: "When the environment selects GMP, it should accept GMP", env: string(metrics.MonitoringAPIGroupGMP), want: metrics.MonitoringAPIGroupGMP},
		{name: "When a pre-populated group is invalid, it should reject it", option: "unsupported", errorContains: "unsupported MONITORING_API_GROUP value"},
		{name: "When the environment group is invalid, it should reject it", env: "unsupported", errorContains: "unsupported MONITORING_API_GROUP value"},
		{name: "When the explicit group overrides an invalid environment, it should validate the explicit group", option: metrics.MonitoringAPIGroupCoreOS, env: "unsupported", want: metrics.MonitoringAPIGroupCoreOS},
		{name: "When only the installer RHOBS environment is enabled with GMP, it should accept GMP", env: string(metrics.MonitoringAPIGroupGMP), rhobsEnv: "1", want: metrics.MonitoringAPIGroupGMP},
		{name: "When GMP and RHOBS environment are selected with the flag, it should reject the conflict", env: string(metrics.MonitoringAPIGroupGMP), rhobsEnv: "1", rhobsFlag: true, errorContains: "cannot be used with RHOBS monitoring"},
		{name: "When explicit GMP has only the installer RHOBS environment enabled, it should accept GMP", option: metrics.MonitoringAPIGroupGMP, rhobsEnv: "1", want: metrics.MonitoringAPIGroupGMP},
		{name: "When GMP conflicts with the RHOBS flag alone, it should reject the conflict", option: metrics.MonitoringAPIGroupGMP, rhobsFlag: true, errorContains: "cannot be used with RHOBS monitoring"},
		{name: "When RHOBS is enabled with the default group, it should preserve RHOBS validation", rhobsEnv: "1", rhobsFlag: true, want: metrics.MonitoringAPIGroupCoreOS},
		{name: "When the RHOBS flag is used without its environment, it should retain the existing error", rhobsFlag: true, errorContains: "RHOBS_MONITORING environment variable must be set"},
		{name: "When the managed group is set via additional operator env vars, it should reject the override", additionalEnv: map[string]string{metrics.MonitoringAPIGroupEnvVar: string(metrics.MonitoringAPIGroupGMP)}, errorContains: "must not be set via --additional-operator-env-vars"},
		{name: "When an additional managed group key contains whitespace, it should reject the override", additionalEnv: map[string]string{" \t" + metrics.MonitoringAPIGroupEnvVar + " \t": "unsupported"}, errorContains: "must not be set via --additional-operator-env-vars"},
		{name: "When GMP conflicts with additional RHOBS environment, it should reject the conflict", option: metrics.MonitoringAPIGroupGMP, additionalEnv: map[string]string{rhobsmonitoring.EnvironmentVariable: "1"}, errorContains: "cannot be used with RHOBS monitoring"},
		{name: "When additional RHOBS environment is disabled despite the installer environment, it should accept GMP", option: metrics.MonitoringAPIGroupGMP, rhobsEnv: "1", additionalEnv: map[string]string{rhobsmonitoring.EnvironmentVariable: "0"}, want: metrics.MonitoringAPIGroupGMP},
		{name: "When additional RHOBS keys and values contain whitespace with GMP, it should reject the conflict", option: metrics.MonitoringAPIGroupGMP, additionalEnv: map[string]string{" \t" + rhobsmonitoring.EnvironmentVariable + " \t": " \t1 \t"}, errorContains: "cannot be used with RHOBS monitoring"},
		{name: "When additional RHOBS environment is disabled with GMP, it should accept GMP", option: metrics.MonitoringAPIGroupGMP, additionalEnv: map[string]string{rhobsmonitoring.EnvironmentVariable: "0"}, want: metrics.MonitoringAPIGroupGMP},
		{name: "When additional RHOBS environment uses the default group, it should retain CoreOS selection", additionalEnv: map[string]string{rhobsmonitoring.EnvironmentVariable: "1"}, want: metrics.MonitoringAPIGroupCoreOS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(metrics.MonitoringAPIGroupEnvVar, tc.env)
			t.Setenv(rhobsmonitoring.EnvironmentVariable, tc.rhobsEnv)
			opts := Options{PrivatePlatform: string(hyperv1.NonePlatform), MonitoringAPIGroup: tc.option, RHOBSMonitoring: tc.rhobsFlag, AdditionalOperatorEnvVars: tc.additionalEnv}
			// Complete resolves the group from the flag or environment; Validate only checks it.
			require.NoError(t, opts.Complete())
			err := opts.Validate()
			if tc.errorContains != "" {
				require.ErrorContains(t, err, tc.errorContains)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, opts.MonitoringAPIGroup)
			}
		})
	}
}
