/*
Copyright 2017 The Kubernetes Authors.

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

package schemaconv

import (
	"fmt"
	"sort"

	"sigs.k8s.io/structured-merge-diff/v7/schema"
)

const (
	quantityResource     = "io.k8s.apimachinery.pkg.api.resource.Quantity"
	rawExtensionResource = "io.k8s.apimachinery.pkg.runtime.RawExtension"
)

type convert struct {
	preserveUnknownFields bool
	output                *schema.Schema

	currentName   string
	current       *schema.Atom
	errorMessages []string
}

func (c *convert) push(name string, a *schema.Atom) *convert {
	return &convert{
		preserveUnknownFields: c.preserveUnknownFields,
		output:                c.output,
		currentName:           name,
		current:               a,
	}
}

func (c *convert) top() *schema.Atom { return c.current }

func (c *convert) pop(c2 *convert) {
	c.errorMessages = append(c.errorMessages, c2.errorMessages...)
}

func (c *convert) reportError(format string, args ...interface{}) {
	c.errorMessages = append(c.errorMessages,
		c.currentName+": "+fmt.Sprintf(format, args...),
	)
}

func (c *convert) insertTypeDef(name string, atom schema.Atom) {
	def := schema.TypeDef{
		Name: name,
		Atom: atom,
	}
	if def.Atom == (schema.Atom{}) {
		// This could happen if there were a top-level reference.
		return
	}
	c.output.Types = append(c.output.Types, def)
}

func (c *convert) addCommonTypes() {
	c.output.Types = append(c.output.Types, untypedDef)
	c.output.Types = append(c.output.Types, deducedDef)
}

var untypedName string = "__untyped_atomic_"

var untypedDef schema.TypeDef = schema.TypeDef{
	Name: untypedName,
	Atom: schema.Atom{
		Scalar: ptr(schema.Scalar("untyped")),
		List: &schema.List{
			ElementType: schema.TypeRef{
				NamedType: &untypedName,
			},
			ElementRelationship: schema.Atomic,
		},
		Map: &schema.Map{
			ElementType: schema.TypeRef{
				NamedType: &untypedName,
			},
			ElementRelationship: schema.Atomic,
		},
	},
}

var deducedName string = "__untyped_deduced_"

var deducedDef schema.TypeDef = schema.TypeDef{
	Name: deducedName,
	Atom: schema.Atom{
		Scalar: ptr(schema.Scalar("untyped")),
		List: &schema.List{
			ElementType: schema.TypeRef{
				NamedType: &untypedName,
			},
			ElementRelationship: schema.Atomic,
		},
		Map: &schema.Map{
			ElementType: schema.TypeRef{
				NamedType: &deducedName,
			},
			ElementRelationship: schema.Separable,
		},
	},
}

func makeUnions(extensions map[string]interface{}) ([]schema.Union, error) {
	schemaUnions := []schema.Union{}
	if iunions, ok := extensions["x-kubernetes-unions"]; ok {
		unions, ok := iunions.([]interface{})
		if !ok {
			return nil, fmt.Errorf(`"x-kubernetes-unions" should be a list, got %#v`, unions)
		}
		for _, iunion := range unions {
			unionMap, err := toMap(iunion)
			if err != nil {
				return nil, fmt.Errorf(`"x-kubernetes-unions" has unexpected items: %w`, err)
			}
			schemaUnion, err := makeUnion(unionMap)
			if err != nil {
				return nil, err
			}
			schemaUnions = append(schemaUnions, schemaUnion)
		}
	}

	// Make sure we have no overlap between unions
	fs := map[string]struct{}{}
	for _, u := range schemaUnions {
		if u.Discriminator != nil {
			if _, ok := fs[*u.Discriminator]; ok {
				return nil, fmt.Errorf("%v field appears multiple times in unions", *u.Discriminator)
			}
			fs[*u.Discriminator] = struct{}{}
		}
		for _, f := range u.Fields {
			if _, ok := fs[f.FieldName]; ok {
				return nil, fmt.Errorf("%v field appears multiple times in unions", f.FieldName)
			}
			fs[f.FieldName] = struct{}{}
		}
	}

	return schemaUnions, nil
}

func makeUnion(extensions map[string]interface{}) (schema.Union, error) {
	union := schema.Union{
		Fields: []schema.UnionField{},
	}

	if idiscriminator, ok := extensions["discriminator"]; ok {
		discriminator, ok := idiscriminator.(string)
		if !ok {
			return schema.Union{}, fmt.Errorf(`"discriminator" must be a string, got: %#v`, idiscriminator)
		}
		union.Discriminator = &discriminator
	}

	if ifields, ok := extensions["fields-to-discriminateBy"]; ok {
		fields, err := toMap(ifields)
		if err != nil {
			return schema.Union{}, fmt.Errorf(`"fields-to-discriminateBy" must be a map[string]string: %w`, err)
		}
		// Needs sorted keys by field.
		keys := []string{}
		for field := range fields {
			keys = append(keys, field)
		}
		sort.Strings(keys)
		reverseMap := map[string]struct{}{}
		for _, field := range keys {
			value := fields[field]
			discriminated, ok := value.(string)
			if !ok {
				return schema.Union{}, fmt.Errorf(`"fields-to-discriminateBy"/%v: value must be a string, got: %#v`, field, value)
			}
			union.Fields = append(union.Fields, schema.UnionField{
				FieldName:          field,
				DiscriminatorValue: discriminated,
			})

			// Check that we don't have the same discriminateBy multiple times.
			if _, ok := reverseMap[discriminated]; ok {
				return schema.Union{}, fmt.Errorf("Multiple fields have the same discriminated name: %v", discriminated)
			}
			reverseMap[discriminated] = struct{}{}
		}
	}

	return union, nil
}

func toMap(o interface{}) (map[string]interface{}, error) {
	switch m := o.(type) {
	// JSON and go.yaml.in/yaml/v3 decode objects into map[string]interface{}
	case map[string]interface{}:
		return m, nil
	// go.yaml.in/yaml/v2 decodes objects into map[interface{}]interface{}
	case map[interface{}]interface{}:
		ret := make(map[string]interface{}, len(m))
		for k, v := range m {
			key, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf(`has non-string key: %#v`, k)
			}
			ret[key] = v
		}
		return ret, nil
	default:
		return nil, fmt.Errorf(`items should be a map of string to interface, got %#v`, o)
	}
}

func toStringSlice(o interface{}) (out []string, ok bool) {
	switch t := o.(type) {
	case []interface{}:
		for _, v := range t {
			switch vt := v.(type) {
			case string:
				out = append(out, vt)
			}
		}
		return out, true
	case []string:
		return t, true
	}
	return nil, false
}

func ptr(s schema.Scalar) *schema.Scalar { return &s }

// Basic conversion functions to convert OpenAPI schema definitions to
// SMD Schema atoms
func convertPrimitive(typ string, format string) (a schema.Atom) {
	switch typ {
	case "integer":
		a.Scalar = ptr(schema.Numeric)
	case "number":
		a.Scalar = ptr(schema.Numeric)
	case "string":
		switch format {
		case "":
			a.Scalar = ptr(schema.String)
		case "byte":
			// byte really means []byte and is encoded as a string.
			a.Scalar = ptr(schema.String)
		case "int-or-string":
			a.Scalar = ptr(schema.Scalar("untyped"))
		case "date-time":
			a.Scalar = ptr(schema.Scalar("untyped"))
		default:
			a.Scalar = ptr(schema.Scalar("untyped"))
		}
	case "boolean":
		a.Scalar = ptr(schema.Boolean)
	default:
		a.Scalar = ptr(schema.Scalar("untyped"))
	}

	return a
}

func getListElementRelationship(ext map[string]any) (schema.ElementRelationship, []string, error) {
	if val, ok := ext["x-kubernetes-list-type"]; ok {
		switch val {
		case "atomic":
			return schema.Atomic, nil, nil
		case "set":
			return schema.Associative, nil, nil
		case "map":
			keys, ok := ext["x-kubernetes-list-map-keys"]

			if !ok {
				return schema.Associative, nil, fmt.Errorf("missing map keys")
			}

			keyNames, ok := toStringSlice(keys)
			if !ok {
				return schema.Associative, nil, fmt.Errorf("uninterpreted map keys: %#v", keys)
			}

			return schema.Associative, keyNames, nil
		default:
			return schema.Atomic, nil, fmt.Errorf("unknown list type %v", val)
		}
	} else if val, ok := ext["x-kubernetes-patch-strategy"]; ok {
		switch val {
		case "merge", "merge,retainKeys":
			if key, ok := ext["x-kubernetes-patch-merge-key"]; ok {
				keyName, ok := key.(string)

				if !ok {
					return schema.Associative, nil, fmt.Errorf("uninterpreted merge key: %#v", key)
				}

				return schema.Associative, []string{keyName}, nil
			}
			// It's not an error for x-kubernetes-patch-merge-key to be absent,
			// it means it's a set
			return schema.Associative, nil, nil
		case "retainKeys":
			return schema.Atomic, nil, nil
		default:
			return schema.Atomic, nil, fmt.Errorf("unknown patch strategy %v", val)
		}
	}

	// Treat as atomic by default
	return schema.Atomic, nil, nil
}

// Returns map element relationship if specified, or empty string if unspecified
func getMapElementRelationship(ext map[string]any) (schema.ElementRelationship, error) {
	val, ok := ext["x-kubernetes-map-type"]
	if !ok {
		// unset Map element relationship
		return "", nil
	}

	switch val {
	case "atomic":
		return schema.Atomic, nil
	case "granular":
		return schema.Separable, nil
	default:
		return "", fmt.Errorf("unknown map type %v", val)
	}
}
