# CAPO API conversion

The existing HyperShift conversion registry registers this package for the four
OpenStack cluster, machine and template kinds. Provider types remain localized
and independent of the root CAPI version. Core converters keep their private
scheme and existing registrations.

`go generate ./support/openstackconversion` resolves the CAPO module pinned in
`hack/capi-vendor/openstack`. The generator ports its manual and generated typed
field mappings and condition helpers into this package. It removes upstream
receiver methods and scheme registrations and uses localized provider core types.
Run `make capi-sync-openstack` and `make deps` before generation when changing the
pin. Do not edit `zz_generated.*.go` files by hand.

The mappings come from CAPO v0.15.0 at commit
`fec84092da5cefa50af68551407e638b7040f0a0`, specifically
`api/v1beta1/conversion.go`, `api/v1beta1/zz_generated.conversion.go` and
`api/v1beta2/conversion_helpers.go`.

The adapter preserves version-only spec and status data in the existing
`cluster.x-k8s.io/conversion-data` annotation format. It compares the preserved
object with its typed round-trip representation before restoring fields. It
restores unchanged fields and keeps current values for fields edited in the other
version. A changed conditions array retains the current conditions rather than
restoring old severities. Metadata is never restored from the annotation.
Malformed preservation data fails conversion. Conversion works on a deep copy
of the source so generated pointer mappings do not expose the caller's object.

The supported lossy representations follow CAPO's typed mappings. These include
condition severity without preservation data, Ready derived from hub conditions,
nil and empty optional values, and flavor ID precedence. Tests distinguish these
normalizations from unintended data loss. Local API-server upgrade tests live in
`test/envtest/capo_conversion_test.go`. They do not replace OpenStack e2e evidence.

Legacy condition reasons that are empty or invalid under the beta2 schema become
`Unknown` in beta2. The preservation annotation retains their original beta1
representation. Observed generation follows upstream CAPO's conversion behavior;
it does not establish that a controller reconciled the current generation.

## Draft upgrade limitation

The CAPO v0.15 alpha schemas change OpenStackServer port-security fields from
`disablePortSecurity` to `enablePortSecurity` in both spec and resolved status.
They also change conditions on OpenStackServer and OpenStackFloatingIPPool.
Local persisted-object characterization tests demonstrate that legacy port flags
and condition severity disappear from candidate API responses. Existing unchanged
conditions without reason or message remain accepted in the tested update path,
but equivalent status writes on new objects fail required-field validation.

The beta conversion registry does not resolve these same-version alpha changes.
Existing-object alpha compatibility is not complete. The characterization tests
document the gap; passing them is not evidence that the upgrade preserves alpha
semantics. No admission bridge, schema compatibility overlay or migration
controller is included. Do not treat this draft as ready for production upgrades.
