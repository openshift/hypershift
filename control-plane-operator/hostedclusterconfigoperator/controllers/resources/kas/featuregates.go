package kas

import (
	configv1 "github.com/openshift/api/config/v1"
	hyperconfig "github.com/openshift/hypershift/support/config"
)

func RecoverFeatureGates(fg *configv1.FeatureGateSpec) []string {
	if fg != nil {
		return hyperconfig.FeatureGates(&fg.FeatureGateSelection)
	} else {
		return hyperconfig.FeatureGates(&configv1.FeatureGateSelection{
			FeatureSet: configv1.Default,
		})
	}
}

func CheckDeployValidatingAdmissionPolicy(fg *configv1.FeatureGateSpec) bool {
	var deployVAP bool
	featureGates := RecoverFeatureGates(fg)
	for _, gate := range featureGates {
		if gate == "ValidatingAdmissionPolicy=true" {
			deployVAP = true
		}
	}

	return deployVAP
}
