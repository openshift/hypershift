---
name: API Conventions
description: "Enforces OpenShift/Kubernetes/HyperShift API design conventions when writing or reviewing CRD types in api/. Auto-applies when creating or modifying Go structs under api/, adding kubebuilder markers, designing unions, or reviewing API PRs."
---

# HyperShift API Design Conventions

These rules are derived from OpenShift API conventions and expert API reviewer feedback.
Apply them whenever creating or modifying types under `api/`.

Reference: https://github.com/openshift/enhancements/blob/master/dev-guide/api-conventions.md

---

## 1. API Versioning

- **Do NOT start new APIs as `v1beta1`** in OpenShift. OpenShift uses `v1alpha1` (dev/preview) or `v1` (GA). There is no beta stage.
- If a new CRD is behind a feature gate (TechPreviewNoUpgrade), it should be `v1alpha1`.
- Consider: if things are wrong and need changing during dev, having only one version restricts what can be changed. Field names may need to be burned to introduce new structures.

## 2. Discriminated Unions

### Structure
- Always mark union structs with `// +union` marker.
- Use a **discriminator field** (e.g., `storageType`, `platform`) with `// +unionDiscriminator` marker.
- Union member fields get `// +unionMember` markers.

### CEL Validation
- Declaring `+unionDiscriminator` alone is NOT enough — you MUST also add CEL enforcement rules.
- Enforce **required AND forbidden** semantics: when discriminator is `X`, field `x` is required AND all other union fields are forbidden.
- Pattern:
```go
// +kubebuilder:validation:XValidation:rule="self.storageType == 'S3' ? has(self.s3) : !has(self.s3)",message="s3 configuration is required when storageType is S3, and forbidden otherwise"
```
- Do NOT just validate "required when X" — also validate "forbidden otherwise".

### Discriminator Values
- You do NOT need to map discriminator values when they match the field name modulo casing (e.g., discriminator value `S3` maps to field `s3` automatically).

### Future-proofing
- If a union might need sibling fields later, wrap it in a parent struct. E.g., create a `storage` field containing the union, so future non-union fields can be added as siblings to `storage`.
- For platform-specific configuration, use platform-specific blocks (e.g., `aws`, `azure`) so each can grow independently while keeping input cohesive.

## 3. Pointer Usage

### When to use pointers
- Only use pointers when there is a **semantic difference between nil and the zero value**.
- Union member fields with `has()` CEL checks need pointers (to distinguish unset from empty).
- Optional feature-gated structs may need pointers.

### When NOT to use pointers
- If the zero value of a struct is not a valid user choice, do NOT use a pointer.
- Status sub-structs that are value types (no nil vs zero semantic difference) should NOT be pointers.
- If using `omitzero`, you do NOT need a pointer — `omitzero` handles the serialization.
- Fields in HostedCluster/HostedControlPlane that reference config structs: if an empty struct is meaningless, don't use a pointer.

## 4. JSON Tags: omitempty vs omitzero

### Rules
- **`omitempty`**: Use for optional strings, integers, booleans, slices, maps.
- **`omitzero`**: Use for optional structs (both pointer and non-pointer). `omitempty` does nothing for non-pointer structs.
- **Never** use `omitempty` on a non-pointer struct — it has no effect.
- When using `omitzero` on a struct, you do NOT need a pointer.
- **Required fields MUST NOT have `omitempty` or `omitzero`**. If a field is `+required`, it should always be present in the serialized output. Only optional fields use these tags.
- All optional fields MUST have either `omitempty` or `omitzero` per OpenShift conventions.

## 5. Markers and Validation

### Forbidden markers on CRDs
- `+kubebuilder:validation:Pattern` is **no longer allowed** in openshift/api. Use CEL instead with human-readable `message` fields.

### Required vs Optional
- Use `// +required` marker (not `+kubebuilder:validation:Required`).
- Use `// +optional` for optional fields.
- A CRD's root `spec` field should normally be `+required` — what is the purpose of creating a resource with no spec?

### MinItems / MaxItems / MinProperties / MaxProperties
- Use `// +kubebuilder:validation:MinItems=1` on list fields that must have at least one entry.
- Use `// +kubebuilder:validation:MinProperties=1` on optional structs so that when set, at least one child must also be set.
- Use `// +kubebuilder:validation:MaxProperties=1` or `+kubebuilder:validation:ExactlyOneOf` for mutual exclusion.
- Do NOT set MaxItems on status list subresource `Items` fields — schema is not generated from this.

### Status Conditions
- Document the expected condition types in the godoc of the conditions field.

## 6. CEL Validation Best Practices

### Prefer CEL over regex patterns
- Always use CEL `XValidation` rules instead of `+kubebuilder:validation:Pattern`.
- Write **human-readable messages** explaining what the validation enforces.
- Users should never need to interpret a raw regex.

### URL validation
- Use CEL's URL library for URL fields:
```go
// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'https'",message="must be a valid HTTPS URL"
// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getHostname().matches('...')",message="hostname must match ..."
// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getEscapedPath().matches('...')",message="path must match ..."
```
- Break complex URL validation into multiple CEL rules, each with its own error message.

### ARN validation
- For AWS ARNs, do better than just `^arn:`. The ARN format is well-documented — validate the full structure.
- Explain in the message and godoc what a valid ARN looks like.

### Immutability
- Use `self == oldSelf` for immutable fields.
- Consider whether the entire spec should be immutable (e.g., one-shot resources like backup requests). If so, apply `self == oldSelf` at the spec level.
- **Beware 2-step mutation bypass**: if a parent is optional, a user can remove the parent, then re-add it with a different value. Add CEL at the parent level to prevent removal once set:
```go
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.field) || has(self.field)",message="field cannot be removed once set"
```

## 7. Field Documentation (Godoc)

### Required documentation
- Every field MUST have clear godoc explaining:
  - What valid values are and what they mean.
  - The format/pattern expected (in human-readable prose, not just regex).
  - When the field is conditional (e.g., "required when storageType is S3, forbidden otherwise").
- **Do NOT include implementation details** in field godoc. Godoc describes the API contract for users, not internal behavior (e.g., don't mention "propagated to HCP" or "used by controller X").

### Enum/discriminator fields
- Document all valid choices and what each one means.

### DNS names and identifiers
- Document as "DNS1123 subdomain" or equivalent standard.
- Explain the rules in prose: "must be at most 253 characters in length and consist of alphanumeric characters, hyphens and periods."

### String fields with patterns
- Always explain the valid character set and format constraints.
- Reference the source of the constraint (e.g., AWS docs, Azure docs).

## 8. String Field Validation

### Always validate string fields
- Never accept arbitrary strings. Define:
  - Valid character set.
  - Min/max length (via OpenAPI, not regex).
  - Pattern constraints (via CEL).
- Research the actual constraints from the target system (AWS, Azure, etc.).

### Platform-specific examples
- **S3 bucket names**: lowercase letters, numbers, hyphens, periods. No consecutive periods. 3-63 chars. Cannot end in hyphen.
- **S3 object keys**: Check AWS docs for full valid character set. AWS allows up to 1024 bytes.
- **Azure container names**: lowercase letters, numbers, hyphens. No consecutive hyphens. 3-63 chars.
- **AWS regions**: validate pattern (e.g., `us-east-1`).
- **KMS Key ARNs**: validate full ARN structure, not just `^arn:` prefix.
- **Azure Key Vault URLs**: use CEL URL library to validate scheme, hostname pattern, and path structure.

### DNS1123 subdomain regex
- Correct pattern: `[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*`
- On supported OpenShift versions, consider using format validations instead of regex.

## 9. Required Fields Inside Structs

- If a struct is non-nil (i.e., the parent is set), fields within it that are always needed MUST be `+required`, not `+optional`.
- Example: if a platform encryption config struct is present, the key field inside it should be required — it makes no sense to set the struct without the key.

## 10. Feature Gate Comments

- When registering feature gates, document the lifecycle stage (Alpha, Beta, GA) only if it matches OpenShift conventions.
- OpenShift does not have a Beta stage — use Alpha or GA only.
- Avoid comments that don't add value (e.g., `// alpha` next to a feature gate with no further explanation).

## 11. Linter Enforcement

- Run the OpenShift API linter before submitting. The HyperShift repo has linter integration (see `make lint`).
- The linter enforces `omitempty`/`omitzero` conventions, marker correctness, and other API rules automatically.
- Always run `make verify` which includes API linting.

## 12. Custom Types (No Upstream Reuse)

### Secret references
- **Create your own secret reference type** — do NOT re-use upstream `corev1.LocalObjectReference`.
- Upstream types can change at any time, add fields you don't want to support, or break your API.
- Only import upstream types if you are passing them through verbatim to another API.

## 13. Struct Placement

### Where types belong
- Types should live in the file where they are referenced. If `HCPEtcdBackupConfig` is only used in `HostedCluster`, it belongs in `hostedcluster_types.go`, not in a separate file.
- Only create separate type files for top-level CRD types (types with their own `+kubebuilder:resource` marker).

## 14. Mutual Exclusion Patterns

### For exactly-one-of semantics
- Prefer **discriminated unions** (with discriminator field + CEL).
- Alternative: `+kubebuilder:validation:ExactlyOneOf=fieldA,fieldB` (newer kubebuilder feature).
- When using `MinProperties=1` + `MaxProperties=1`, consider that this doesn't evolve well for adding new platforms. Discriminated unions are more extensible.

## 15. Checklist for New API Types

Before submitting a PR with new API types:

- [ ] Correct API version (`v1alpha1` for feature-gated, `v1` for GA)
- [ ] All string fields have character set and length validation
- [ ] All unions are discriminated with `+union` marker and full CEL rules (required + forbidden)
- [ ] CEL used instead of `+kubebuilder:validation:Pattern` with human-readable messages
- [ ] `omitzero` used for struct fields, not `omitempty`
- [ ] Pointers only where nil vs zero has semantic meaning
- [ ] Custom types for external references (no reusing upstream types)
- [ ] All fields documented with valid values and format in godoc
- [ ] Immutable fields protected against 2-step mutation bypass
- [ ] URL fields validated with CEL URL library
- [ ] Status condition types documented
- [ ] Types placed in the correct file
- [ ] `spec` is `+required` unless there's a reason for optional
- [ ] Platform-specific validations match upstream platform docs (AWS, Azure, etc.)
- [ ] `MinItems=1` on lists that must be non-empty
- [ ] `MinProperties=1` on optional structs that must have content when set
- [ ] Required fields do NOT have `omitempty` or `omitzero`
- [ ] All optional fields have `omitempty` or `omitzero`
- [ ] Fields inside non-nil structs that are always needed are `+required`
- [ ] No implementation details in field godoc (only API contract)
- [ ] `+unionDiscriminator` always paired with CEL enforcement rules
- [ ] Platform-specific config uses platform blocks for extensibility
- [ ] OpenShift API linter passes (`make verify`)
- [ ] One-shot resources consider full spec immutability
