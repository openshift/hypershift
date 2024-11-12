package config

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
)

func FeatureGates(fg configv1.FeatureGateSelection) []string {
	result := []string{}
	var enabled, disabled []configv1.FeatureGateName
	if fg.FeatureSet == configv1.CustomNoUpgrade {
		enabled = fg.CustomNoUpgrade.Enabled
		disabled = fg.CustomNoUpgrade.Disabled
	} else {
		fs, err := features.FeatureSets(features.Hypershift, fg.FeatureSet)
		if err != nil {
			return nil
		}
		for _, fgDescription := range fs.Enabled {
			enabled = append(enabled, fgDescription.FeatureGateAttributes.Name)
		}
		for _, fgDescription := range fs.Disabled {
			disabled = append(disabled, fgDescription.FeatureGateAttributes.Name)
		}
	}
	for _, e := range enabled {
		result = append(result, fmt.Sprintf("%s=true", e))
	}
	for _, d := range disabled {
		result = append(result, fmt.Sprintf("%s=false", d))
	}
	return result
}
