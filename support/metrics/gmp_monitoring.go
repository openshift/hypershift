package metrics

import (
	"fmt"
)

// GMPUnsupportedComponents lists the component asset directories whose CoreOS
// monitors get no GMP PodMonitoring because the component can never run on a
// platform that uses GMP, with the reason. The generator does not stage them and
// the parity test does not expect them. A component that can run on GCP but that
// the generator cannot represent yet does not belong here: it is excluded in
// hack/gmp-config-gen and waived in the parity test with a tracking issue.
var GMPUnsupportedComponents = map[string]string{
	"karpenter":          "Karpenter runs only on AWS and Azure.",
	"karpenter-operator": "Karpenter runs only on AWS and Azure.",
}

// ValidateGMPMonitoring rejects enabling Google Managed Prometheus monitoring
// together with RHOBS monitoring, since only one monitoring API can be emitted.
func ValidateGMPMonitoring(gmpMonitoring, rhobsMonitoring bool) error {
	if gmpMonitoring && rhobsMonitoring {
		return fmt.Errorf("GMP monitoring cannot be enabled together with RHOBS monitoring")
	}
	return nil
}
