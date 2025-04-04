/*
nofloats is an analyzer that ensures fields in the API types do not contain a `float32` or `float64` type.

Floating-point values cannot be reliably round-tripped without changing
and have varying precision and representation across languages and architectures.
Their use should be avoided as much as possible.
They should never be used in spec.
*/
package nofloats
