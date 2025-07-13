# Kube API Linter

Kube API Linter (KAL) is a Golang based linter for Kubernetes API types.
It checks for common mistakes and enforces best practices.
The rules implemented by the Kube API Linter, are based on the [Kubernetes API Conventions][api-conventions].

Kube API Linter is aimed at being an assistant to API review, by catching the mechanical elements of API review, and allowing reviewers to focus on the more complex aspects of API design.

[api-conventions]: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md

## Installation

Kube API Linter ships as a golangci-lint plugin, and a golangci-lint module.

### Golangci-lint Module

To install the `golangci-lint` module, first you must have `golangci-lint` installed.
If you do not have `golangci-lint` installed, review the `golangci-lint` [install guide][golangci-lint-install].

[golangci-lint-install]: https://golangci-lint.run/welcome/install/

You will need to create a `.custom-gcl.yml` file to describe the custom linters you want to run. The following is an example of a `.custom-gcl.yml` file:

```yaml
version:  v1.64.8
name: golangci-kube-api-linter
destination: ./bin
plugins:
- module: 'sigs.k8s.io/kube-api-linter'
  version: 'v0.0.0' # Replace with the latest version
```

Once you have created the custom configuration file, you can run the following command to build the custom `golangci-kal` binary:

```shell
golangci-lint custom
```

The output binary will be a combination of the initial `golangci-lint` binary and the Kube API linter plugin.
This means that you can use any of the standard `golangci-lint` configuration or flags to run the binary, but may also include the Kube API Linter rules.

If you wish to only use the Kube API Linter rules, you can configure your `.golangci.yml` file to only run the Kube API Linter:

```yaml
linters-settings:
  custom:
    kubeapilinter:
      type: "module"
      description: Kube API LInter lints Kube like APIs based on API conventions and best practices.
      settings:
        linters: {}
        lintersConfig: {}
linters:
  disable-all: true
  enable:
    - kubeapilinter

# To only run Kube API Linter on specific path
issues:
  exclude-rules:
    - path-except: "api/*"
      linters:
        - kubeapilinter
```

If you wish to only run selected linters you can do so by specifying the linters you want to enable in the `linters` section:

```yaml
linters-settings:
  custom:
    kubeapilinter:
      type: "module"
      settings:
        linters:
          disable:
            - "*"
          enable:
            - requiredfields
            - statusoptional
            - statussubresource
```

The settings for Kube API Linter are based on the [GolangCIConfig][golangci-config-struct] struct and allow for finer control over the linter rules.

If you wish to use the Kube API Linter in conjunction with other linters, you can enable the Kube API Linter in the `.golangci.yml` file by ensuring that `kubeapilinter` is in the `linters.enabled` list.
To provide further configuration, add the `custom.kubeapilinter` section to your `linter-settings` as per the example above.

[golangci-config-struct]: https://pkg.go.dev/sigs.k8s.io/kube-api-linter/pkg/config#GolangCIConfig

Where fixes are available within a rule, these can be applied automatically with the `--fix` flag.

```shell
golangci-kube-api-linter run path/to/api/types --fix
```

### Golangci-lint Plugin

The Kube API Linter can also be used as a plugin for `golangci-lint`.
To do this, you will need to install the `golangci-lint` binary and then install the Kube API Linter plugin.

More information about golangci-lint plugins can be found in the [golangci-lint plugin documentation][golangci-lint-plugin-docs].

[golangci-lint-plugin-docs]: https://golangci-lint.run/plugins/go-plugins/

```shell
go build -buildmode=plugin -o bin/kube-api-linter.so sigs.k8s.io/kube-api-linter/pkg/plugin
```

This will create a `kube-api-linter.so` file in the `bin` directory.

The `golangci-lint` configuration is similar to the module configuration, however, you will need to specify the plugin path instead.

```yaml
linters-settings:
  custom:
    kubeapilinter:
      path: "bin/kube-api-linter.so"
      description: Kube API LInter lints Kube like APIs based on API conventions and best practices.
      original-url: sigs.k8s.io/kube-api-linter
      settings:
        linters: {}
        lintersConfig: {}
```

The rest of the configuration is the same as the module configuration, except the standard `golangci-lint` binary is invoked, rather than a custom binary.

#### VSCode integration

Since VSCode already integrates with `golangci-lint` via the [Go][vscode-go] extension, you can use the `golangci-kal` binary as a linter in VSCode.
If your project authors are already using VSCode and have the configuration to lint their code when saving, this can be a seamless integration.

Ensure that your project setup includes building the `golangci-kube-api-linter` binary, and then configure the `go.lintTool` and `go.alternateTools` settings in your project `.vscode/settings.json` file.

[vscode-go]: https://code.visualstudio.com/docs/languages/go

```json
{
    "go.lintTool": "golangci-lint",
    "go.alternateTools": {
        "golangci-lint": "${workspaceFolder}/bin/golangci-kube-api-linter",
    }
}
```

Alternatively, you can also replace the binary with a script that runs the `golangci-kube-api-linter` binary,
allowing for customisation or automatic copmilation of the project should it not already exist.

```json
{
    "go.lintTool": "golangci-lint",
    "go.alternateTools": {
        "golangci-lint": "${workspaceFolder}/hack/golangci-lint.sh",
    }
}
```

# Linters

## Conditions

The `conditions` linter checks that `Conditions` fields in the API types are correctly formatted.
The `Conditions` field should be a slice of `metav1.Condition` with the following tags and markers:

```go
// +listType=map
// +listMapKey=type
// +patchStrategy=merge
// +patchMergeKey=type
// +optional
Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,opt,name=conditions"`
```

Conditions are idiomatically the first field within the status struct, and the linter will highlight when the Conditions are not the first field.

Protobuf tags and patch strategy are required for in-tree API types, but not for CRDs.
When linting CRD based types, set the `useProtobuf` and `usePatchStrategy` config option to `Ignore` or `Forbid`.

### Configuration

```yaml
lintersConfig:
  conditions:
    isFirstField: Warn | Ignore # The policy for the Conditions field being the first field. Defaults to `Warn`.
    useProtobuf: SuggestFix | Warn | Ignore | Forbid # The policy for the protobuf tag on the Conditions field. Defaults to `SuggestFix`.
    usePatchStrategy: SuggestFix | Warn | Ignore | Forbid # The policy for the patchStrategy tag on the Conditions field. Defaults to `SuggestFix`.
```

### Fixes

The `conditions` linter can automatically fix the tags on the `Conditions` field.
When they do not match the expected format, the linter will suggest to update the tags to match the expected format.

For CRDs, protobuf tags and patch strategy are not expected.
By setting the `useProtobuf`/`usePatchStrategy` configuration to `Ignore`, the linter will not suggest to add the protobuf/patch strategy tag to the `Conditions` field tags.
By setting the `useProtobuf`/`usePatchStrategy` configuration to `Forbid`, the linter will suggest to remove the protobuf/patch strategy tag from the `Conditions` field tags.

The linter will also suggest to add missing markers.
If any of the 5 markers in the example above are missing, the linter will suggest to add them directly above the field.

When `usePatchStrategy` is set to `Ignore`, the linter will not suggest to add the `patchStrategy` and `patchMergeKey` tags to the `Conditions` field markers.
When `usePatchStrategy` is set to `Forbid`, the linter will suggest to remove the `patchStrategy` and `patchMergeKey` tags from the `Conditions` field markers.

## CommentStart

The `commentstart` linter checks that all comments in the API types start with the serialized form of the type they are commenting on.
This helps to ensure that generated documentation reflects the most common usage of the field, the serialized YAML form.

### Fixes

The `commentstart` linter can automatically fix comments that do not start with the serialized form of the type.

When the `json` tag is present, and matches the first word of the field comment in all but casing, the linter will suggest that the comment be updated to match the `json` tag.

## DuplicateMarkers

The duplicatemarkers linter checks for exact duplicates of markers for types and fields.
This means that something like:

```go
// +kubebuilder:validation:MaxLength=10
// +kubebuilder:validation:MaxLength=10
```

Will be flagged by this linter, while something like:

```go
// +kubebuilder:validation:MaxLength=10
// +kubebuilder:validation:MaxLength=11
```

will not.

### Fixes

The `duplicatemarkers` linter can automatically fix all markers that are exact match to another markers.
If there are duplicates across fields and their underlying type, the marker on the type will be preferred and the marker on the field will be removed.

## Integers

The `integers` linter checks for usage of unsupported integer types.
Only `int32` and `int64` types should be used in APIs, and other integer types, including unsigned integers are forbidden.

## JSONTags

The `jsontags` linter checks that all fields in the API types have a `json` tag, and that those tags are correctly formatted.
The `json` tag for a field within a Kubernetes API type should use a camel case version of the field name.

The `jsontags` linter checks the tag name against the regex `"^[a-z][a-z0-9]*(?:[A-Z][a-z0-9]*)*$"` which allows consecutive upper case characters, to allow for acronyms, e.g. `requestTTL`.

### Configuration

```yaml
lintersConfig:
  jsonTags:
    jsonTagRegex: "^[a-z][a-z0-9]*(?:[A-Z][a-z0-9]*)*$" # Provide a custom regex, which the json tag must match.
```

## MaxLength

The `maxlength` linter checks that string and array fields in the API are bounded by a maximum length.

For strings, this means they have a `+kubebuilder:validation:MaxLength` marker.

For arrays, this means they have a `+kubebuilder:validation:MaxItems` marker.

For arrays of strings, the array element should also have a `+kubebuilder:validation:MaxLength` marker if the array element is a type alias,
or `+kubebuilder:validation:items:MaxLenth` if the array is an element of the built-in string type.

Adding maximum lengths to strings and arrays not only ensures that the API is not abused (used to store overly large data, reduces DDOS etc.),
but also allows CEL validation cost estimations to be kept within reasonable bounds.

## NoBools

The `nobools` linter checks that fields in the API types do not contain a `bool` type.

Booleans are limited and do not evolve well over time.
It is recommended instead to create a string alias with meaningful values, as an enum.

## NoFloats

The `nofloats` linter checks that fields in the API types do not contain a `float32` or `float64` type.

Floating-point values cannot be reliably round-tripped without changing and have varying precision and representation across languages and architectures.
Their use should be avoided as much as possible.
They should never be used in spec.

## Nomaps

The `nomaps` linter checks the usage of map types.

Maps are discouraged apart from `map[string]string` which is used for labels and annotations in Kubernetes APIs since it's hard to distinguish between structs and maps in spec. Instead of plain map, lists of named subobjects are preferred.

### Configuration

```yaml
lintersConfig:
  nomaps:
    policy: Enforce | AllowStringToStringMaps | Ignore # Determines how the linter should handle maps of simple types. Defaults to AllowStringToStringMaps.
```

## Nophase

The `nophase` linter checks that the fields in the API types don't contain a 'Phase', or any field which contains 'Phase' as a substring, e.g MachinePhase.

## OptionalFields

The `optionalfields` linter checks that all fields marked as optional adhere to being pointers and having the `omitempty` value in their `json` tag where appropriate.

If you prefer to avoid pointers where possible, the linter can be configured with the `WhenRequired` preference to determine, based on the serialization and valid values for the field, whether the field should be a pointer or not.
For example, an optional string with a non-zero minimum length does not need to be a pointer, as the zero value is not valid, and it is safe for the Go marshaller to omit the empty value.

In certain use cases, it can be desirable to not omit optional fields from the serialized form of the object.
In this case, the `omitempty` policy can be set to `Ignore`, and the linter will ensure that the zero value of the object is an acceptable value for the field.

### Configuration

```yaml
lintersConfig:
  optionalFields:
    pointers:
      preference: Always | WhenRequired # Whether to always require pointers, or only when required. Defaults to `Always`.
      policy: SuggestFix | Warn # The policy for pointers in optional fields. Defaults to `SuggestFix`.
    omitempty:
        policy: SuggestFix | Warn | Ignore # The policy for omitempty in optional fields. Defaults to `SuggestFix`.
```

### Fixes

The `optionalfields` linter can automatically fix fields that are marked as optional, that are either not pointers or do not have the `omitempty` value in their `json` tag.
It will suggest to add the pointer to the field, and update the `json` tag to include the `omitempty` value.

If you prefer not to suggest fixes for pointers in optional fields, you can change the `pointers.policy` to `Warn`.

If you prefer not to suggest fixes for `omitempty` in optional fields, you can change the `omitempty.policy` to `Warn` or `Ignore`.

When the `pointers.preference` is set to `WhenRequired`, the linter will suggest to add the pointer to the field only when the field zero value is a valid value for the field.
When the field zero value is not a valid value for the field, the linter will suggest to remove the pointer from the field.

When the `pointers.preference` is set to `Always`, the linter will always suggest to add the pointer to the field, regardless of the validity of the zero value of the field.

## OptionalOrRequired

The `optionalorrequired` linter checks that all fields in the API types are either optional or required, and are marked explicitly as such.

The linter expects to find a comment marker `// +optional` or `// +required` within the comment for the field.

It also supports the `// +kubebuilder:validation:Optional` and `// +kubebuilder:validation:Required` markers, but will suggest to use the `// +optional` and `// +required` markers instead.

If you prefer to use the Kubebuilder markers instead, you can change the preference in the configuration.

The `optionalorrequired` linter also checks for the presence of optional or required markers on type declarations, and forbids this pattern.

### Configuration

```yaml
lintersConfig:
  optionalOrRequired:
    preferredOptionalMarker: optional | kubebuilder:validation:Optional # The preferred optional marker to use, fixes will suggest to use this marker. Defaults to `optional`.
    preferredRequiredMarker: required | kubebuilder:validation:Required # The preferred required marker to use, fixes will suggest to use this marker. Defaults to `required`.
```

### Fixes

The `optionalorrequired` linter can automatically fix fields that are using the incorrect form of either the optional or required marker.

It will also remove the secondary marker where both the preferred and secondary marker are present on a field.

## RequiredFields

The `requiredfields` linter checks that fields that are marked as required, follow the convention of not being pointers,
and not having an `omitempty` value in their `json` tag.

### Configuration

```yaml
lintersConfig:
  requiredFields:
    pointerPolicy: Warn | SuggestFix # The policy for pointers in required fields. Defaults to `SuggestFix`.
```

### Fixes

The `requiredfields` linter can automatically fix fields that are marked as required, but are pointers.

It will suggest to remove the pointer from the field, and update the `json` tag to remove the `omitempty` value.

If you prefer not to suggest fixes for pointers in required fields, you can change the `pointerPolicy` to `Warn`.
The linter will then only suggest to remove the `omitempty` value from the `json` tag.

## StatusOptional

The `statusoptional` linter checks that all first-level children fields within a status struct are marked as optional.

This is important because status fields should be optional to allow for partial updates and backward compatibility.
The linter ensures that all direct child fields of any status struct have either the `// +optional` or
`// +kubebuilder:validation:Optional` marker.

### Fixes

The `statusoptional` linter can automatically fix fields in status structs that are not marked as optional.

It will suggest adding the `// +optional` marker to any status field that is missing it.

## StatusSubresource

The `statussubresource` linter checks that the status subresource is configured correctly for
structs marked with the `kubebuilder:object:root:=true` marker. Correct configuration is that
when there is a status field the `kubebuilder:subresource:status` marker is present on the struct
OR when the `kubebuilder:subresource:status` marker is present on the struct there is a status field.

This linter is not enabled by default as it is only applicable to CustomResourceDefinitions.

### Fixes

In the case where there is a status field present but no `kubebuilder:subresource:status` marker, the
linter will suggest adding the comment `// +kubebuilder:subresource:status` above the struct.

## UniqueMarkers

The `uniquemarkers` linter ensures that types and fields do not contain more than a single definition of a marker that should only be present once.

Because this linter has no way of determining which marker definition was intended it does not suggest any fixes 

### Configuration
It can configured to include a set of custom markers in the analysis by setting:
```yaml
lintersConfig:
  uniqueMarkers:
    customMarkers:
      - identifier: custom:SomeCustomMarker
        attributes:
          - fruit
```

For each custom marker, it must specify an `identifier` and optionally some `attributes`.
As an example, take the marker definition `kubebuilder:validation:XValidation:rule='has(self.foo)',message='should have foo',fieldPath='.foo'`.
The identifier for the marker is `kubebuilder:validation:XValidation` and its attributes are `rule`, `message`, and `fieldPath`.

When specifying `attributes`, those attributes are included in the uniqueness identification of a marker definition.

Taking the example configuration from above:

- Marker definitions of `custom:SomeCustomMarker:fruit=apple,color=red` and `custom:SomeCustomMarker:fruit=apple,color=green` would violate the uniqueness requirement and be flagged.
- Marker definitions of `custom:SomeCustomMarker:fruit=apple,color=red` and `custom:SomeCustomMarker:fruit=orange,color=red` would _not_ violate the uniqueness requirement.

Each entry in `customMarkers` must have a unique `identifier`.

# Contributing

New linters can be added by following the [New Linter][new-linter] guide.

[new-linter]: docs/new-linter.md

# License

KAL is licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for the full license text.
