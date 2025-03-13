/*
extractjsontags is a helper package that extracts JSON tags from a struct field.

It returns data behind the interface [StructFieldTags] which is used to find information about JSON tags on fields within a struct.

Data about json tags, for a field within a struct can be accessed by calling the `FieldTags` method on the interface.

Example:

	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	jsonTags := pass.ResultOf[extractjsontags.Analyzer].(extractjsontags.StructFieldTags)

	// Filter to fields so that we can iterate over fields in a struct.
	nodeFilter := []ast.Node{
		(*ast.Field)(nil),
	}

	inspect.Preorder(nodeFilter, func(n ast.Node) {
		field, ok := n.(*ast.Field)
		if !ok {
			return
		}

		tagInfo := jsonTags.FieldTags(field)

		...

	})

For each field, tag information is returned as a [FieldTagInfo] struct.
This can be used to determine the name of the field, as per the json tag, whether the
field is inline, has omitempty or is missing completely.
*/
package extractjsontags
