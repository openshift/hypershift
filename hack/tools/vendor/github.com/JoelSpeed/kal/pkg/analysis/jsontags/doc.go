/*
jsontags provides a linter to ensure that JSON tags are present on struct fields, and that they match a given regex.

Kubernetes API types should have JSON tags on all fields, to ensure that the fields are correctly serialized and deserialized.
The JSON tags should be camelCase, with a lower case first letter, and should match the field name in all but capitalization.
There should be no hyphens or underscores in either the field name or the JSON tag.

The linter can be configured with a regex to match the JSON tags against.
By default, the regex is `^[a-z][a-z0-9]*(?:[A-Z][a-z0-9]*)*$`, which allows for camelCase JSON tags, with consecutive capital letters,
to allow, for example, for fields like `requestTTLSeconds`.

To disallow consecutive capital letters, the regex can be set to `^[a-z][a-z0-9]*(?:[A-Z][a-z0-9]+)*$`.
The regex can be configured with the JSONTagRegex field in the JSONTagsConfig struct.
*/
package jsontags
