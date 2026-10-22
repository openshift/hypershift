# Explicit AWS CPO override checks

Use `HYPERSHIFT_PLATFORM=aws` and
`TEST_PLAN=contrib/aws-e2e-v2-cpo-overrides.yaml` for both `create-guests` and
`run-tests`, retaining the existing lifecycle inputs such as `SHARED_DIR`,
`ARTIFACT_DIR`, `PROW_JOB_ID`, pull secret, AWS infrastructure credentials,
region/zones and `RELEASE_IMAGE_LATEST`. This plan creates only two dedicated
worker-bearing public hosted clusters. Their release images come from the
production `LatestOverrideTestReleases("aws")` pair, overriding the ordinary
latest/previous inputs for these fixtures only. It does not select upgrade tests
or overwrite NodePool historical-image inputs.

The management operator must already be installed with the existing
`hypershift install --enable-cpo-overrides` option. This workflow never installs,
patches or enables it. Before provisioning, creation discovers the actual
operator deployment across namespaces, requires an unambiguous fully rolled-out
installation with literal `ENABLE_CPO_OVERRIDES=1`, and resolves both release
payload versions to production CPO mappings. Set
`E2E_HYPERSHIFT_OPERATOR_NAMESPACE` when multiple installations exist. Missing,
unready, ambiguous, disabled or inaccessible prerequisites fail creation.

The selected `hosted-cluster-cpo-required` assertion cannot skip an unmapped
observed release. It requires a nonempty running CPO pod with the exact mapped
container image for each hosted cluster. Requiredness comes from the selected
registration, not a process environment flag, so it survives separate create
and run processes using the same plan and cluster manifest.

This is deliberate explicit selection. The production map's automatic
`runTests` policy is unchanged; AWS currently has automatic override testing
disabled. The legacy `TEST_CPO_OVERRIDE=1` request must also select this dedicated
plan rather than quietly running unrelated default tests or returning a
successful global skip. Ordinary default v2 coverage is unchanged.

Registry access to the mapped historical payloads, an already compatible
management installation, AWS resources and live assertion success remain runtime
prerequisites, not evidence provided by local unit tests or label selection.
