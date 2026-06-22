package metrics

import (
	corev1 "k8s.io/api/core/v1"
)

const (
	// The common label for monitoring in HyperShift
	// Namespaces with this label will be actively monitored by the observability operator
	HyperShiftMonitoringLabel = "hypershift.openshift.io/monitoring"
)

// EnableOBOMonitoring enforces observability operator monitoring on the given namespace
func EnableOBOMonitoring(namespace *corev1.Namespace) {
	if namespace.Labels == nil {
		namespace.Labels = make(map[string]string)
	}

	namespace.Labels[HyperShiftMonitoringLabel] = "true"
}
