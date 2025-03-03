/*
nophase provides a linter to ensure that structs do not contain a Phase field.

Phase fields are deprecated and conditions should be preferred. Avoid phase like enum
fields.

The linter will flag any struct field containing the substring 'phase'. This means both
Phase and FooPhase will be flagged.
*/

package nophase
