package crd

import (
	"fmt"

	crdmarkers "sigs.k8s.io/controller-tools/pkg/crd/markers"
	"sigs.k8s.io/controller-tools/pkg/markers"
)

// mayHandleField returns true if the field should be considered by this invocation of the generator.
// Right now, the only skip is based on the featureset marker.
func mayHandleField(field markers.FieldInfo) bool {
	if len(crdmarkers.RequiredFeatureSets) > 0 {
		if uncastFeatureSet := field.Markers.Get(crdmarkers.OpenShiftFeatureSetMarkerName); uncastFeatureSet != nil {
			featureSetsForField, ok := uncastFeatureSet.([]string)
			if !ok {
				panic(fmt.Sprintf("actually got %t", uncastFeatureSet))
			}
			//  if any of the field's declared featureSets match any of the manifest's declared featuresets, include the field.
			for _, currFeatureSetForField := range featureSetsForField {
				if crdmarkers.RequiredFeatureSets.Has(currFeatureSetForField) {
					return true
				}
			}
		}
		return false
	}

	if uncastFeatureGate := field.Markers.Get(crdmarkers.OpenShiftFeatureGateMarkerName); uncastFeatureGate != nil {
		featureGatesForField, ok := uncastFeatureGate.([]string)
		if !ok {
			panic(fmt.Sprintf("actually got %t", uncastFeatureGate))
		}
		// we actually want to compare the golang marker's value against the manifest's annotation value
		return crdmarkers.FeatureGatesForCurrentFile.HasAny(featureGatesForField...)
	}

	return true
}
