package markers

import (
	"errors"
	"go/ast"
	"go/token"
	"reflect"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// UnnamedExpression is the expression key used
// when parsing markers that don't have a specific
// named expression.
//
// An example of a marker without a named expression
// is "kubebuilder:default:=foo".
//
// An example of a marker with named expressions
// is "kubebuilder:validation:XValidation:rule='...',message='...'".
const UnnamedExpression = ""

var (
	errCouldNotGetInspector  = errors.New("could not get inspector")
	errCouldNotCreateMarkers = errors.New("could not create markers")
)

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

func run(pass *analysis.Pass) (interface{}, error) {
	inspect, ok := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if !ok {
		return nil, errCouldNotGetInspector
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
		return nil, errCouldNotCreateMarkers
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
		if marker := extractMarker(comment); marker.Identifier != "" {
			fieldMarkers.Insert(marker)
		}
	}

	results.insertFieldMarkers(field, fieldMarkers)
}

func extractMarker(comment *ast.Comment) Marker {
	if !strings.HasPrefix(comment.Text, "// +") {
		return Marker{}
	}

	markerContent := strings.TrimPrefix(comment.Text, "// +")
	id, expressions := extractMarkerIDAndExpressions(DefaultRegistry(), markerContent)

	return Marker{
		Identifier:  id,
		Expressions: expressions,
		RawComment:  comment.Text,
		Pos:         comment.Pos(),
		End:         comment.End(),
	}
}

func extractMarkerIDAndExpressions(knownMarkers Registry, marker string) (string, map[string]string) {
	if id, ok := knownMarkers.Match(marker); ok {
		return extractKnownMarkerIDAndExpressions(id, marker)
	}

	return extractUnknownMarkerIDAndExpressions(marker)
}

func extractKnownMarkerIDAndExpressions(id string, marker string) (string, map[string]string) {
	return id, extractExpressions(strings.TrimPrefix(marker, id))
}

func extractExpressions(expressions string) map[string]string {
	expressionsMap := map[string]string{}

	// Do some normalization work to ensure we can parse expressions in
	// a standard way. Trim any lingering colons (:) and replace all ':='s with '='
	expressions = strings.TrimPrefix(expressions, ":")
	expressions = strings.ReplaceAll(expressions, ":=", "=")

	// split expression string on commas (,) to handle multiple expressions
	// in a single marker
	chainedExpressions := strings.Split(expressions, ",")
	for _, chainedExpression := range chainedExpressions {
		exps := strings.SplitN(chainedExpression, "=", 2)
		if len(exps) < 2 {
			continue
		}

		expressionsMap[exps[0]] = exps[1]
	}

	return expressionsMap
}

func extractUnknownMarkerIDAndExpressions(marker string) (string, map[string]string) {
	// if there is only a single "=" split on the equal sign and trim any
	// dangling ":" characters.
	if strings.Count(marker, "=") == 1 {
		splits := strings.Split(marker, "=")
		// Trim any dangling ":" characters on the identifier to handle
		// cases like +kubebuilder:object:root:=true
		identifier := strings.TrimSuffix(splits[0], ":")

		// If there is a single "=" sign that means the left side of the
		// marker is the identifier and there is no real expression identifier.
		expressions := map[string]string{UnnamedExpression: splits[1]}

		return identifier, expressions
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

	expressions := extractExpressions(expressionString)

	return identifier, expressions
}

// Marker represents a marker extracted from a comment on a declaration.
type Marker struct {
	// Identifier is the value of the marker once the leading comment, '+', and expressions are trimmed.
	Identifier string

	// Expressions are the set of expressions that have been specified for the marker
	Expressions map[string]string

	// RawComment is the raw comment line, unfiltered.
	RawComment string

	// Pos is the starting position in the file for the comment line containing the marker.
	Pos token.Pos

	// End is the ending position in the file for the coment line containing the marker.
	End token.Pos
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
// expression values (i.e "kubebuilder:object:root:=true") is present
// in the MarkerSet.
func (ms MarkerSet) HasWithValue(marker string) bool {
	return ms.HasWithExpressions(extractMarkerIDAndExpressions(DefaultRegistry(), marker))
}

// HasWithExpressions returns whether marker(s) with the identifier and
// expressions are present in the MarkerSet.
func (ms MarkerSet) HasWithExpressions(identifier string, expressions map[string]string) bool {
	markers, ok := ms[identifier]
	if !ok {
		return false
	}

	for _, marker := range markers {
		if reflect.DeepEqual(marker.Expressions, expressions) {
			return true
		}
	}

	return false
}

// UnsortedList returns a list of the markers, in no particular order.
func (ms MarkerSet) UnsortedList() []Marker {
	markers := []Marker{}

	for _, marker := range ms {
		markers = append(markers, marker...)
	}

	return markers
}
