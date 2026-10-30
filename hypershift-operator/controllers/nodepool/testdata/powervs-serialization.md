# PowerVS template serialization baseline

The adjacent `powervs-v0.13.1-*.json` fixtures were serialized with
`encoding/json` from the copied CAPIBM v0.13.1 types in
`pkg/capi/ibmcloud/api/v1beta2` at HyperShift commit
`c87e4905311af782d166e0f06ee102a5bebdf487`.
They cover the release image reference and explicit image paths, `s1122`,
nil optional reference fields and omitted fields. The recorded names apply
the existing `HashSimple` byte-slice representation and `workers` prefix.
These are immutable compatibility baselines, not update-on-failure goldens:
a changed name would cause a rollout and requires explicit review.
