# HyperShift API Module

This module (`github.com/openshift/hypershift/api`) is consumed by external Go clients that vendor it directly. Changes to types in this module affect not only the HyperShift operator but also downstream consumers that embed, serialize, and deserialize these types independently of the Kubernetes API server.

## API Type Change Guidelines

### N-1 and N+1 Compatibility

Every change to an API type must be safe for both:
- **N+1 (forward):** New code reading data written by old code
- **N-1 (rollback):** Old code reading data written by new code

This matters because consumers like ARO-HCP embed these types directly into their own structs and serialize them to storage (e.g., Cosmos DB) outside of CRD validation. If a consumer must revert to a previous code level, they need to deserialize data that was written by the newer version without errors or data corruption.

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
