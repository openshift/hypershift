package markers

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-tools/pkg/markers"
)

// FeatureGatesForCurrentFile is reset every
var FeatureGatesForCurrentFile = sets.String{}

var RequiredFeatureSets = sets.NewString()

func init() {
	featureSet := os.Getenv("OPENSHIFT_REQUIRED_FEATURESET")
	if len(featureSet) == 0 {
		return
	}

	for _, curr := range strings.Split(featureSet, ",") {
		RequiredFeatureSets.Insert(curr)
	}
}

const OpenShiftFeatureSetMarkerName = "openshift:enable:FeatureSets"
const OpenShiftFeatureSetAwareEnumMarkerName = "openshift:validation:FeatureSetAwareEnum"
const OpenShiftFeatureSetAwareXValidationMarkerName = "openshift:validation:FeatureSetAwareXValidation"
const OpenShiftFeatureGateMarkerName = "openshift:enable:FeatureGate"
const OpenShiftFeatureGateAwareEnumMarkerName = "openshift:validation:FeatureGateAwareEnum"
const OpenShiftFeatureGateAwareMaxItemsMarkerName = "openshift:validation:FeatureGateAwareMaxItems"
const OpenShiftFeatureGateAwareXValidationMarkerName = "openshift:validation:FeatureGateAwareXValidation"

func init() {
	ValidationMarkers = append(ValidationMarkers,
		must(markers.MakeDefinition(OpenShiftFeatureSetAwareEnumMarkerName, markers.DescribesField, FeatureSetEnum{})).
			WithHelp(markers.SimpleHelp("OpenShift", "specifies the FeatureSet that is required to generate this field.")),
	)
	FieldOnlyMarkers = append(FieldOnlyMarkers,
		must(markers.MakeDefinition(OpenShiftFeatureSetMarkerName, markers.DescribesField, []string{})).
			WithHelp(markers.SimpleHelp("OpenShift", "specifies the FeatureSet that is required to generate this field.")),
	)
	ValidationMarkers = append(ValidationMarkers,
		must(markers.MakeDefinition(OpenShiftFeatureSetAwareXValidationMarkerName, markers.DescribesType, FeatureSetXValidation{})).
			WithHelp(markers.SimpleHelp("OpenShift", "specifies the FeatureSet that is required to generate this XValidation rule.")),
	)

	ValidationMarkers = append(ValidationMarkers,
		must(markers.MakeDefinition(OpenShiftFeatureGateAwareEnumMarkerName, markers.DescribesField, FeatureGateEnum{})).
			WithHelp(markers.SimpleHelp("OpenShift", "specifies the FeatureGate that is required to generate this field.")),
	)
	ValidationMarkers = append(ValidationMarkers,
		must(markers.MakeDefinition(OpenShiftFeatureGateAwareMaxItemsMarkerName, markers.DescribesField, FeatureGateMaxItems{})).
			WithHelp(markers.SimpleHelp("OpenShift", "specifies the FeatureGate that is required to generate this field.")),
	)
	FieldOnlyMarkers = append(FieldOnlyMarkers,
		must(markers.MakeDefinition(OpenShiftFeatureGateMarkerName, markers.DescribesField, []string{})).
			WithHelp(markers.SimpleHelp("OpenShift", "specifies the FeatureGate that is required to generate this field.")),
	)
	ValidationMarkers = append(ValidationMarkers,
		must(markers.MakeDefinition(OpenShiftFeatureGateAwareXValidationMarkerName, markers.DescribesType, FeatureGateXValidation{})).
			WithHelp(markers.SimpleHelp("OpenShift", "specifies the FeatureGate that is required to generate this XValidation rule.")),
	)
}

type FeatureSetEnum struct {
	FeatureSetNames []string `marker:"featureSet"`
	EnumValues      []string `marker:"enum"`
}

func (m FeatureSetEnum) ApplyToSchema(schema *apiext.JSONSchemaProps) error {
	if !RequiredFeatureSets.HasAny(m.FeatureSetNames...) {
		return nil
	}

	// TODO(directxman12): this is a bit hacky -- we should
	// probably support AnyType better + using the schema structure
	vals := make([]apiext.JSON, len(m.EnumValues))
	for i, val := range m.EnumValues {
		// TODO(directxman12): check actual type with schema type?
		// if we're expecting a string, marshal the string properly...
		// NB(directxman12): we use json.Marshal to ensure we handle JSON escaping properly
		valMarshalled, err := json.Marshal(val)
		if err != nil {
			return err
		}
		vals[i] = apiext.JSON{Raw: valMarshalled}
	}

	schema.Enum = vals
	return nil
}

type FeatureSetXValidation struct {
	FeatureSetNames []string `marker:"featureSet"`
	Rule            string
	Message         string `marker:",optional"`
}

func (m FeatureSetXValidation) ApplyToSchema(schema *apiext.JSONSchemaProps) error {
	if !RequiredFeatureSets.HasAny(m.FeatureSetNames...) {
		return nil
	}

	validation := XValidation{
		Rule:    m.Rule,
		Message: m.Message,
	}

	return validation.ApplyToSchema(schema)
}

// ApplyFirst means that this will be applied in the first run of markers.
// We do this because, when validations are applied, the markers come out of
// a map which means that the order is not guaranteed. We want to make sure
// that the FeatureSetXValidation is applied before the XValidation so that
// the order is stable.
func (m FeatureSetXValidation) ApplyFirst() {}

type FeatureGateEnum struct {
	FeatureGateNames []string `marker:"featureGate"`
	EnumValues       []string `marker:"enum"`
}

func (m FeatureGateEnum) ApplyToSchema(schema *apiext.JSONSchemaProps) error {
	if !FeatureGatesForCurrentFile.HasAny(m.FeatureGateNames...) {
		return nil
	}

	// TODO(directxman12): this is a bit hacky -- we should
	// probably support AnyType better + using the schema structure
	vals := make([]apiext.JSON, len(m.EnumValues))
	for i, val := range m.EnumValues {
		// TODO(directxman12): check actual type with schema type?
		// if we're expecting a string, marshal the string properly...
		// NB(directxman12): we use json.Marshal to ensure we handle JSON escaping properly
		valMarshalled, err := json.Marshal(val)
		if err != nil {
			return err
		}
		vals[i] = apiext.JSON{Raw: valMarshalled}
	}

	schema.Enum = vals
	return nil
}

type FeatureGateMaxItems struct {
	FeatureGateNames []string `marker:"featureGate"`
	MaxItems         int      `marker:"maxItems"`
}

func (m FeatureGateMaxItems) ApplyToSchema(schema *apiext.JSONSchemaProps) error {
	if !FeatureGatesForCurrentFile.HasAny(m.FeatureGateNames...) {
		return nil
	}

	if schema.Type != "array" {
		return fmt.Errorf("must apply maxitem to an array")
	}
	val := int64(m.MaxItems)
	schema.MaxItems = &val
	return nil
}

type FeatureGateXValidation struct {
	FeatureGateNames []string `marker:"featureGate"`
	Rule             string
	Message          string `marker:",optional"`
}

func (m FeatureGateXValidation) ApplyToSchema(schema *apiext.JSONSchemaProps) error {
	if !FeatureGatesForCurrentFile.HasAny(m.FeatureGateNames...) {
		return nil
	}

	validation := XValidation{
		Rule:    m.Rule,
		Message: m.Message,
	}

	return validation.ApplyToSchema(schema)
}

// ApplyFirst means that this will be applied in the first run of markers.
// We do this because, when validations are applied, the markers come out of
// a map which means that the order is not guaranteed. We want to make sure
// that the FeatureGateXValidation is applied before the XValidation so that
// the order is stable.
func (m FeatureGateXValidation) ApplyFirst() {}
