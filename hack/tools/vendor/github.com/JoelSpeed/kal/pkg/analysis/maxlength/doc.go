/*
maxlength is an analyzer that checks that all string fields have a maximum length, and that all array fields have a maximum number of items.

String fields that are not otherwise bound in length, through being an enum or formatted in a certain way, should have a maximum length.
This ensures that CEL validations on the field are not overly costly in terms of time and memory.

Array fields should have a maximum number of items.
This ensures that any CEL validations on the field are not overly costly in terms of time and memory.
Where arrays are used to represent a list of structures, CEL rules may exist within the array.
Limiting the array length ensures the cardinality of the rules within the array is not unbounded.

For strings, the maximum length can be set using the `kubebuilder:validation:MaxLength` tag.
For arrays, the maximum number of items can be set using the `kubebuilder:validation:MaxItems` tag.

For arrays of strings, the maximum length of each string can be set using the `kubebuilder:validation:items:MaxLength` tag,
on the array field itself.
Or, if the array uses a string type alias, the `kubebuilder:validation:MaxLength` tag can be used on the alias.
*/
package maxlength
