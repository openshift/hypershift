package config

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
)

func FeatureGates(fg *configv1.FeatureGateSelection) []string {
	result := []string{}
	var enabled, disabled []string
	if fg.FeatureSet == configv1.CustomNoUpgrade {
		enabled = fg.CustomNoUpgrade.Enabled
		disabled = fg.CustomNoUpgrade.Disabled
	} else {
		fs := configv1.FeatureSets[fg.FeatureSet]
		enabled = fs.Enabled
		disabled = fs.Disabled
	}
	for _, e := range enabled {
		result = append(result, fmt.Sprintf("%s=true", e))
	}
	for _, d := range disabled {
		result = append(result, fmt.Sprintf("%s=false", d))
	}
	return result
}
