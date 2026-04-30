# HyperShift API Module

This module (`github.com/openshift/hypershift/api`) is consumed by external Go clients that vendor it directly. Changes to types in this module affect not only the HyperShift operator but also downstream consumers that embed, serialize, and deserialize these types independently of the Kubernetes API server.

## CRD API Machinery Fundamentals

These are non-obvious behaviors of the Kubernetes API machinery that affect how CRD types in `api/` must be written. These are not style preferences or conventions — they are fundamental facts and reasoning about how the system works.

For conventions, always trust the kube-api-linter (`make api-lint-fix`). Do not question its findings and do not add exclusions unless explicitly told to by a human reviewer. The linter encodes the [OpenShift API conventions](https://github.com/openshift/enhancements/blob/master/dev-guide/api-conventions.md) (authoritative) and [upstream Kubernetes API conventions](https://github.com/kubernetes/community/blob/main/contributors/devel/sig-architecture/api-conventions.md) (informational, downstream wins on conflicts). Do not fetch or read those documents — the linter already enforces them, and the actionable rules are captured in this file.

### API Versioning

- APIs primarily in v1beta1
- Any new API should GA as v1
- Use feature gates for experimental functionality
- CRD generation via controller-gen with OpenShift-specific tooling

### API Dependencies

It is imperative that the imported dependencies are kept minimal. Use `make verify-api-deps` to verify that the dependencies are allowed.
New dependencies must be approved by API reviewers and added to `api/.imports_allowed`.

To avoid introducing new dependencies, do not add utils or methods to the API types.

### Serialization

- **Always set `omitempty` or `omitzero` on every field, regardless of whether it is `+required` or `+optional`.** `omitempty`/`omitzero` tags control serialization, not validation. `+required` is a schema constraint enforced at admission time; the serialization tag controls what goes on the wire. Without a tag, a zero-value field serializes as an explicit value (e.g., `"pullSecret": {"name": ""}`), which makes the API server unable to distinguish "not set" from "explicitly set to empty." This breaks defaulting, server-side apply field ownership, and strategic merge patch — all of which rely on field absence to mean "don't touch this." Additionally, without omission a structured client serializes the empty object, which passes the `+required` check (based on key presence) without validating the value — so a user can forget to set a required field, it passes admission, and the reader sees a required field with an unexpected empty value.
- `omitempty` **does nothing for non-pointer structs.** Only `omitzero` correctly omits a struct field when it equals its zero value. This is a Go encoding/json behavior, not a Kubernetes convention. Use `omitempty` for scalar fields (string, int, bool) and slices/maps. Use `omitzero` for struct fields (available since Go 1.24; this repo requires Go 1.25+).
- **The only reason to use a pointer in a CRD is when the zero value is a valid, distinct user choice.** If the struct has a required field, `{}` can never be valid user input, so there is no ambiguity to resolve and no pointer is needed. `omitzero` on a non-pointer struct will correctly omit the key from serialized output. `MinProperties`/`MaxProperties` on the parent counts serialized keys — it has no concept of whether the Go field is a pointer.
- `// +default` **must be paired with** `// +optional` because the required check runs before defaulting. A required field with a default will be rejected before the default is ever applied.

### Validation Execution

- **OpenAPI schema validation only runs when a key is present in the serialized object.** if a field is omitted, the validation never executes. This is why `MinLength=1` on an optional field is safe: the constraint only fires when the user actually provides a value.
- **Optionality and min constraints are independent concerns.** An optional field with `MinLength=1` means "you don't have to set this, but if you do, it can't be empty." These do not conflict.
- **Admission-time validation rejects the write immediately.** Controller-time validation accepts the write, the user assumes success, and discovers the problem later via conditions or logs. Always prefer admission-time via CEL over controller-time validation.

### Immutability

- **Immutable + optional creates a two-step bypass.** A user can (1) remove the optional field, then (2) re-add it with a different value. To prevent this, add a CEL rule at the parent level that forbids removing the field once set: `oldSelf.has(field) ? self.has(field) : true`.
- **"Once set, cannot be removed" and "once set, cannot be changed" are separate constraints.** You typically need both together, and they require separate CEL rules.

### Defaulting and Transitions

- **Ratcheting validation**: when adding new validation to existing fields, verify that existing clusters with values that predate the new validation can still be updated. CRD validation ratchets (allows unchanged invalid values through), but only for fields that are literally unchanged in the update.

## API Type Change Guidelines

### Best Practices and Patterns

Use api/karpenter/v1beta1/karpenter_types.go and api/hypershift/v1beta1/etcdbackup_types.go as examples of best practices and patterns.

Don't use the other existing APIs as examples as they might have many legacy constraints.

### Field Grouping

**When multiple fields on a spec share a common prefix or relate to the same feature, they MUST be grouped into a dedicated struct.** Top-level specs like HostedClusterSpec and NodePoolSpec should only contain fields that are independently meaningful. If removing one field would make another field meaningless, they belong together in a sub-struct. A common signal is fields that share a name prefix (e.g., `BarEndpoint`, `BarConfig`, `BarID` all relate to "Bar" and should be a single `Bar` field with a `BarSpec` struct).

### N-1 and N+1 Compatibility

Every change to an API type must be safe for both:

- **N+1 (forward):** New code reading data written by old code
- **N-1 (rollback):** Old code reading data written by new code

This matters because consumers like ARO-HCP embed these types directly into their own structs and serialize them to storage (e.g., Cosmos DB) outside CRD validation. If a consumer must revert to a previous code level, they need to deserialize data that was written by the newer version without errors or data corruption.

### Common Pitfalls

- **Changing a value type to a pointer** (e.g., `int32` to `*int32`): Without `omitempty`, a nil pointer serializes as `null`, which cannot be deserialized back into the non-pointer type. Always pair pointer types with `omitempty` on required fields.
- **Removing or renaming a field**: Old code will fail to deserialize the new format if it expects the field.
- **Changing a field's type**: Ensure the JSON representation is compatible in both directions.

### Required Tests

When modifying API types, add serialization compatibility tests that:

1. Define a struct matching the **previous** version of the type
2. Serialize the **current** type and verify the previous version can deserialize it
3. Serialize the **previous** type and verify the current version can deserialize it
4. Cover edge cases: zero values, nil pointers, omitted fields

See `api/hypershift/v1beta1/nodepool_types_test.go` for an example of this pattern.

All API CEL validations must be covered with envtests, see test/envtest/README.md for details

#### Key make targets for API work:

```bash
make api               # Regenerate all CRDs, deepcopy, clients
make api-lint-fix      # Run API linter and auto-fix violations
make verify-api-deps   # Verify API dependencies
make verify            # Full verification (includes api, fmt, vet, lint)
make update            # Full update (api-deps, workspace-sync, deps, api, api-docs, clients)
ENVTEST_OCP_K8S_VERSIONS=1.35.0 make test-envtest-ocp # Run envtest for CEL validations
```

All these must pass for any change before creating a PR
