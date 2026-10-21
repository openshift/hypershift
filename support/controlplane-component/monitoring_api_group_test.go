package controlplanecomponent

import (
	"testing"

	"github.com/openshift/hypershift/support/metrics"

	"github.com/stretchr/testify/require"
)

func TestWorkloadContext(t *testing.T) {
	for _, tc := range []struct {
		name  string
		group metrics.MonitoringAPIGroup
	}{
		{name: "When monitoring selection is empty, it should preserve the empty context value"},
		{name: "When CoreOS is selected, it should thread CoreOS to the workload", group: metrics.MonitoringAPIGroupCoreOS},
		{name: "When GMP is selected, it should thread GMP to the workload", group: metrics.MonitoringAPIGroupGMP},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cp := &ControlPlaneContext{Context: t.Context(), MetricsSet: metrics.MetricsSetSRE, MonitoringAPIGroup: tc.group}
			workload := cp.workloadContext()
			require.Equal(t, tc.group, workload.MonitoringAPIGroup)
			require.Equal(t, cp.MetricsSet, workload.MetricsSet)
		})
	}
}
