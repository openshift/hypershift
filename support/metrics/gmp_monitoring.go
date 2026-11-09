package metrics

import (
	"fmt"
)

// ValidateGMPMonitoring rejects enabling Google Managed Prometheus monitoring
// together with RHOBS monitoring, since only one monitoring API can be emitted.
func ValidateGMPMonitoring(gmpMonitoring, rhobsMonitoring bool) error {
	if gmpMonitoring && rhobsMonitoring {
		return fmt.Errorf("GMP monitoring cannot be enabled together with RHOBS monitoring")
	}
	return nil
}
