/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package markers

import (
	"go/ast"
	"go/token"
	"reflect"
	"regexp"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"

	"k8s.io/gengo/v2/codetags"

	kalerrors "sigs.k8s.io/kube-api-linter/pkg/analysis/errors"
)

// UnnamedArgument is the argument key used
// when parsing markers that don't have a specific
// named argument.
//
// This is specific to declarative validation markers only.
// Kubebuilder-style markers either have named arguments or a payload.
//
// An example of a Declarative Validation marker with an unnamed argument
// is "k8s:ifEnabled(\"my-feature\")=...".
//
// An example of a Declarative Validation marker with named arguments
// is "k8s:item(one: "value", two: "value")=...".
const UnnamedArgument = ""

// Markers allows access to markers extracted from the
// go types.
type Markers interface {
	// FieldMarkers returns markers associated to the field.
	FieldMarkers(*ast.Field) MarkerSet

	// StructMarkers returns markers associated to the given sturct.
	StructMarkers(*ast.StructType) MarkerSet

	// TypeMarkers returns markers associated to the given type.
	TypeMarkers(*ast.TypeSpec) MarkerSet
}

func newMarkers() Markers {
	return &markers{
		fieldMarkers:  make(map[*ast.Field]MarkerSet),
		structMarkers: make(map[*ast.StructType]MarkerSet),
		typeMarkers:   make(map[*ast.TypeSpec]MarkerSet),
	}
}

// markers implements the storage for the implementation of the Markers interface.
type markers struct {
	fieldMarkers  map[*ast.Field]MarkerSet
	structMarkers map[*ast.StructType]MarkerSet
	typeMarkers   map[*ast.TypeSpec]MarkerSet
}

// FieldMarkers return the appropriate MarkerSet for the field,
// or an empty MarkerSet if the appropriate MarkerSet isn't found.
func (m *markers) FieldMarkers(field *ast.Field) MarkerSet {
	fMarkers := m.fieldMarkers[field]

	return NewMarkerSet(fMarkers.UnsortedList()...)
}

// StructMarkers returns the appropriate MarkerSet if found, else
// it returns an empty MarkerSet.
func (m *markers) StructMarkers(sTyp *ast.StructType) MarkerSet {
	sMarkers := m.structMarkers[sTyp]

	return NewMarkerSet(sMarkers.UnsortedList()...)
}

// TypeMarkers return the appropriate MarkerSet for the type,
// or an empty MarkerSet if the appropriate MarkerSet isn't found.
func (m *markers) TypeMarkers(typ *ast.TypeSpec) MarkerSet {
	tMarkers := m.typeMarkers[typ]

	return NewMarkerSet(tMarkers.UnsortedList()...)
}

func (m *markers) insertFieldMarkers(field *ast.Field, ms MarkerSet) {
	m.fieldMarkers[field] = ms
}

func (m *markers) insertStructMarkers(sTyp *ast.StructType, ms MarkerSet) {
	m.structMarkers[sTyp] = ms
}

func (m *markers) insertTypeMarkers(typ *ast.TypeSpec, ms MarkerSet) {
	m.typeMarkers[typ] = ms
}

// Analyzer is the analyzer for the markers package.
// It iterates over declarations within a package and parses the comments to extract markers.
var Analyzer = &analysis.Analyzer{
	Name:       "markers",
	Doc:        "Iterates over declarations within a package and parses the comments to extract markers",
	Run:        run,
	Requires:   []*analysis.Analyzer{inspect.Analyzer},
	ResultType: reflect.TypeOf(newMarkers()),
}

func run(pass *analysis.Pass) (any, error) {
	inspect, ok := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if !ok {
		return nil, kalerrors.ErrCouldNotGetInspector
	}

	nodeFilter := []ast.Node{
		// In order to get the godoc comments from a type
		// definition as such:
		//
		// // comment
		// type Foo struct {...}
		//
		// We need to use the ast.GenDecl type instead of the
		// ast.TypeSpec type. The ast.TypeSpec.Doc field will only
		// be populated if types are defined as such:
		//
		// type(
		//   // comment
		//   Foo struct {...}
		// )
		//
		// For more information, see https://github.com/golang/go/issues/27477
		(*ast.GenDecl)(nil),
		(*ast.Field)(nil),
	}

	results, ok := newMarkers().(*markers)
	if !ok {
		return nil, kalerrors.ErrCouldNotCreateMarkers
	}

	inspect.Preorder(nodeFilter, func(n ast.Node) {
		switch typ := n.(type) {
		case *ast.GenDecl:
			extractGenDeclMarkers(typ, results)
		case *ast.Field:
			extractFieldMarkers(typ, results)
		}
	})

	return results, nil
}

func extractGenDeclMarkers(typ *ast.GenDecl, results *markers) {
	declMarkers := NewMarkerSet()

	if typ.Doc != nil {
		for _, comment := range typ.Doc.List {
			if marker := extractMarker(comment); marker.Identifier != "" {
				declMarkers.Insert(marker)
			}
		}
	}

	if len(typ.Specs) == 0 {
		return
	}

	tSpec, ok := typ.Specs[0].(*ast.TypeSpec)
	if !ok {
		return
	}

	results.insertTypeMarkers(tSpec, declMarkers)

	if sTyp, ok := tSpec.Type.(*ast.StructType); ok {
		results.insertStructMarkers(sTyp, declMarkers)
	}
}

func extractFieldMarkers(field *ast.Field, results *markers) {
	if field == nil || field.Doc == nil {
		return
	}

	fieldMarkers := NewMarkerSet()

	for _, comment := range field.Doc.List {
		marker := extractMarker(comment)
		if marker.Identifier != "" {
			fieldMarkers.Insert(marker)
		}
	}

	results.insertFieldMarkers(field, fieldMarkers)
}

// validMarkerStart validates that a marker starts with an alphabetic character
// and contains only valid marker content (letters, numbers, colons, parentheses, quotes, spaces, and commas).
// This excludes markdown tables (e.g., "-------") and other non-marker content,
// while supporting declarative validation tags with parentheses and nested markers.
var validMarkerStart = regexp.MustCompile(`^[a-zA-Z]([a-zA-Z0-9:\(\)\"\" ,])+=?`)

func extractMarker(comment *ast.Comment) Marker {
	if !strings.HasPrefix(comment.Text, "// +") {
		return Marker{}
	}

	markerContent := strings.TrimPrefix(comment.Text, "// +")

	// Valid markers must start with an alphabetic character (a-zA-Z).
	// This excludes markdown tables (e.g., "// +-------") and other non-marker content,
	// while supporting declarative validation tags that may include parentheses and nested markers.
	if !validMarkerStart.MatchString(markerContent) {
		return Marker{}
	}

	if isDeclarativeValidationMarker(markerContent) {
		marker := extractDeclarativeValidationMarker(markerContent, comment)
		if marker == nil {
			return Marker{}
		}

		return *marker
	}

	return extractKubebuilderMarker(markerContent, comment)
}

func extractKubebuilderMarker(markerContent string, comment *ast.Comment) Marker {
	id, arguments, payload := extractMarkerIDArgumentsAndPayload(DefaultRegistry(), markerContent)

	return Marker{
		Type:       MarkerTypeKubebuilder,
		Identifier: id,
		Arguments:  arguments,
		Payload:    payload,
		RawComment: comment.Text,
		Pos:        comment.Pos(),
		End:        comment.End(),
	}
}

func extractMarkerIDArgumentsAndPayload(knownMarkers Registry, marker string) (string, map[string]string, Payload) {
	if id, ok := knownMarkers.Match(marker); ok {
		return extractKnownMarkerIDArgumentsAndPayload(id, marker)
	}

	return extractUnknownMarkerIDArgumentsAndPayload(marker)
}

func isDeclarativeValidationMarker(marker string) bool {
	return strings.HasPrefix(marker, "k8s:")
}

func extractDeclarativeValidationMarker(marker string, comment *ast.Comment) *Marker {
	tag, err := codetags.Parse(marker)
	if err != nil {
		return nil
	}

	return markerForTag(tag, comment)
}

func markerForTag(tag codetags.Tag, comment *ast.Comment) *Marker {
	out := &Marker{
		Type:       MarkerTypeDeclarativeValidation,
		Identifier: tag.Name,
		Arguments:  make(map[string]string),
		RawComment: comment.Text,
		Pos:        comment.Pos(),
		End:        comment.End(),
	}

	for _, arg := range tag.Args {
		out.Arguments[arg.Name] = arg.Value
	}

	switch tag.ValueType {
	case codetags.ValueTypeString, codetags.ValueTypeInt, codetags.ValueTypeBool, codetags.ValueTypeRaw:
		// all resolvable to an exact string value
		out.Payload = Payload{
			Value: tag.Value,
		}
	case codetags.ValueTypeNone:
		// nothing
	case codetags.ValueTypeTag:
		out.Payload = Payload{
			Marker: markerForTag(*tag.ValueTag, comment),
		}
	default:
		return nil
	}

	return out
}

func extractKnownMarkerIDArgumentsAndPayload(id string, marker string) (string, map[string]string, Payload) {
	args, payload := extractArgumentsAndPayload(strings.TrimPrefix(marker, id))
	return id, args, payload
}

var expressionRegex = regexp.MustCompile("\\w*=(?:'[^']*'|\"(\\\\\"|[^\"])*\"|[\\w;\\-\"]+|`[^`]*`)")

func extractArgumentsAndPayload(expressionStr string) (map[string]string, Payload) {
	expressionsMap := map[string]string{}

	var payload Payload

	// Do some normalization work to ensure we can parse expressions in
	// a standard way. Trim any lingering colons (:) and replace all ':='s with '='
	expressionStr = strings.TrimPrefix(expressionStr, ":")
	expressionStr = strings.ReplaceAll(expressionStr, ":=", "=")

	expressions := expressionRegex.FindAllString(expressionStr, -1)
	for _, expression := range expressions {
		key, value, ok := strings.Cut(expression, "=")
		if !ok {
			continue
		}

		if key == UnnamedArgument {
			payload.Value = value
			continue
		}

		expressionsMap[key] = value
	}

	return expressionsMap, payload
}

func extractUnknownMarkerIDArgumentsAndPayload(marker string) (string, map[string]string, Payload) {
	// if there is only a single "=" split on the equal sign and trim any
	// dangling ":" characters.
	if strings.Count(marker, "=") == 1 {
		splits := strings.Split(marker, "=")
		// Trim any dangling ":" characters on the identifier to handle
		// cases like +kubebuilder:object:root:=true
		identifier := strings.TrimSuffix(splits[0], ":")

		// If there is a single "=" sign that means the left side of the
		// marker is the identifier and there is no real argument identifier.
		return identifier, make(map[string]string), Payload{Value: splits[1]}
	}

	// split on :
	separators := strings.Split(marker, ":")

	identifier := ""
	expressionString := ""

	for _, item := range separators {
		// Not an expression
		if strings.Count(item, "=") == 0 {
			if identifier == "" {
				identifier = item

				continue
			}

			identifier = strings.Join([]string{identifier, item}, ":")

			continue
		}

		// The item is likely an expression, join it with any existing expression string.
		// While something like 'foo:bar=baz:value=something' isn't a valid marker based on our
		// current understanding, this logic should ensure we are joining expressions appropriately
		// in a scenario like this.
		if expressionString == "" {
			expressionString = item
			continue
		}

		expressionString = strings.Join([]string{expressionString, item}, ",")
	}

	expressions, payload := extractArgumentsAndPayload(expressionString)

	return identifier, expressions, payload
}

// MarkerType is a representation of the style of marker.
// Currently can be one of Kubebuilder or DeclarativeValidation.
type MarkerType string

const (
	// MarkerTypeKubebuilder represents a Kubebuilder-style marker.
	MarkerTypeKubebuilder MarkerType = "Kubebuilder"
	// MarkerTypeDeclarativeValidation represents a Declarative Validation marker.
	MarkerTypeDeclarativeValidation MarkerType = "DeclarativeValidation"
)

// Payload represents the payload of a marker.
type Payload struct {
	// Value is the payload value of a marker represented as a string.
	// Value is set when the payload value of a marker is not another marker.
	Value string

	// Marker is the marker in the payload value of another marker.
	// Marker is only set when the payload value of a marker is another marker.
	Marker *Marker
}

// Marker represents a marker extracted from a comment on a declaration.
type Marker struct {
	// Type is the marker representation this marker was identified as.
	// Currently, the two marker format types are DeclarativeValidation and Kubebuilder.
	// Because the Kubebuilder style has been around the longest and is widely
	// used in projects that have CustomResourceDefinitions we default to Kubebuilder
	// style parsing unless we detect that the marker follows the declarative validation
	// format (i.e begins with +k8s:).
	Type MarkerType

	// Identifier is the value of the marker once the leading comment, '+', and expressions are trimmed.
	Identifier string

	// Arguments are the set of named and unnamed arguments that have been specified for the marker.
	//
	// For Markers with Type == Kubebuilder, there will only ever be named arguments. The following examples highlight how arguments are extracted:
	//     - `+kubebuilder:validation:Required` would result in *no* arguments.
	//     - `+required` would result in *no* arguments.
	//     - `+kubebuilder:validation:MinLength=10` would result in no arguments`.
	//     - `+kubebuilder:validation:XValidation:rule="has(self)",message="should have self"` would result in 2 named arguments, `rule` and `message` with their respective values in string representation.
	//
	// For Markers with Type == DeclarativeValidation, arguments are extracted from the marker parameters. Arguments may be named or unnamed.
	// Some examples:
	//     - `+k8s:forbidden` would result in *no* arguments.
	//     - `+k8s:ifEnabled("my-feature")=...` would result in a single unnamed argument (represented by key `""`) with a value of `"my-feature"`.
	//     - `+k8s:item(one: "value", two: "value")=...` would result in 2 named arguments, `one` and `two` with their respective values in string representation.
	Arguments map[string]string

	// Payload is the payload specified by the marker.
	// In general, it is what is present after the first `=` symbol
	// of a marker.
	Payload Payload

	// RawComment is the raw comment line, unfiltered.
	RawComment string

	// Pos is the starting position in the file for the comment line containing the marker.
	Pos token.Pos

	// End is the ending position in the file for the coment line containing the marker.
	End token.Pos
}

// String returns the string representation of the marker.
func (m Marker) String() string {
	return strings.TrimPrefix(m.RawComment, "// +")
}

// MarkerSet is a set implementation for Markers that uses
// the Marker identifier as the key, but returns all full Markers
// with that identifier as the result.
type MarkerSet map[string][]Marker

// NewMarkerSet initialises a new MarkerSet with the provided values.
// If any markers have the same identifier, they will both be added to
// the list of markers for that identifier. No duplication checks are implemented.
func NewMarkerSet(markers ...Marker) MarkerSet {
	ms := make(MarkerSet)

	ms.Insert(markers...)

	return ms
}

// Insert adds the given markers to the MarkerSet.
// If any markers have the same value, the latter marker in the list
// will take precedence, no duplication checks are implemented.
func (ms MarkerSet) Insert(markers ...Marker) {
	for _, marker := range markers {
		ms[marker.Identifier] = append(ms[marker.Identifier], marker)
	}
}

// Has returns whether marker(s) with the identifier given is present in the
// MarkerSet. If Has returns true, there is at least one marker
// with this identifier.
func (ms MarkerSet) Has(identifier string) bool {
	_, ok := ms[identifier]
	return ok
}

// HasWithValue returns whether marker(s) with the given identifier and
// argument/payload values (i.e "kubebuilder:object:root:=true") is present
// in the MarkerSet.
func (ms MarkerSet) HasWithValue(marker string) bool {
	if isDeclarativeValidationMarker(marker) {
		marker := extractDeclarativeValidationMarker(marker, &ast.Comment{})
		if marker == nil {
			return false
		}

		return ms.HasWithArgumentsAndPayload(marker.Identifier, marker.Arguments, marker.Payload)
	}

	id, args, payload := extractMarkerIDArgumentsAndPayload(DefaultRegistry(), marker)

	return ms.HasWithArgumentsAndPayload(id, args, payload)
}

// HasWithArgumentsAndPayload returns whether marker(s) with the
// identifier, arguments, and payload are present in the MarkerSet.
func (ms MarkerSet) HasWithArgumentsAndPayload(identifier string, arguments map[string]string, payload Payload) bool {
	markers, ok := ms[identifier]
	if !ok {
		return false
	}

	for _, marker := range markers {
		if reflect.DeepEqual(marker.Arguments, arguments) && reflect.DeepEqual(marker.Payload, payload) {
			return true
		}
	}

	return false
}

// Get returns the markers associated with the given identifier.
// If no markers are found, an empty slice is returned.
// The returned slice may contain multiple markers with the same identifier.
func (ms MarkerSet) Get(identifier string) []Marker {
	return ms[identifier]
}

// UnsortedList returns a list of the markers, in no particular order.
func (ms MarkerSet) UnsortedList() []Marker {
	markers := []Marker{}

	for _, marker := range ms {
		markers = append(markers, marker...)
	}

	return markers
}
