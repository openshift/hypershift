/*
inspector is a helper package that iterates over fields in structs, calling an inspection function on fields
that should be considered for analysis.

The inspector extracts common logic of iterating and filtering through struct fields, so that analyzers
need not re-implement the same filtering over and over.

For example, the inspector filters out struct definitions that are not type declarations, and fields that are ignored.

Example:

	type A struct {
		// This field is included in the analysis.
		Field string `json:"field"`

		// This field, and the fields within are ignored due to the json tag.
		F struct {
			Field string `json:"field"`
		} `json:"-"`
	}

	// Any struct defined within a function is ignored.
	func Foo() {
		type Bar struct {
			Field string
		}
	}

	// All fields within interface declarations are ignored.
	type Bar interface {
		Name() string
	}
*/
package inspector
