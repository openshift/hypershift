# GMP API data types

This package is a maintained API-only extraction from
[`GoogleCloudPlatform/prometheus-engine`](https://github.com/GoogleCloudPlatform/prometheus-engine).
It contains `PodMonitoring`, `Rules`, their lists and the complete closure of
nested data types, including
status, authentication and TLS fields. Upstream license headers, field comments,
JSON tags and generation markers are preserved. The generated `upstream.sha256`
records the resolved source version, upstream commit, module checksums and input
file checksums.

Collection, rule evaluation, admission, Secret resolution and Prometheus config
construction are excluded. The package imports only Kubernetes API machinery and
the already selected `prometheus/common/model` package. It does not import the
Google operator or require its Prometheus fork. Existing dependency versions stay
unchanged. `ClusterPodMonitoring`, `ClusterRules` and `GlobalRules` are deferred;
management rules spanning HCP namespaces require a separate rule-scope decision.

`AddToScheme` registers the four supported kinds. HyperShift adds them to both
`Scheme` and `AllMonitoringScheme` regardless of process environment. Existing
CoreOS/RHOBS registration remains unchanged. Registration creates no resources,
clients or watches and does not select a backend at reconciliation time.

## Maintenance

The GCP HCP team owns this API snapshot, its extraction tooling and compatibility
validation. Changes to the package and tooling are reviewed by `gcp-reviewers`
through the normal HyperShift review process.

The source version is selected in
[`hack/gmp-api-source/go.mod`](../../hack/gmp-api-source/go.mod). This is a
download-only module; do not run `go mod tidy` there. HyperShift does not import
the upstream operator package. Do not edit generated types, deepcopies or
checksums by hand.

Version bumps are deliberate. The pin should track the GMP version deployed by GKE,
not the latest upstream release, so `renovate.json` disables automated dependency
updates for `hack/gmp-api-source/go.mod`. The regeneration tooling does not monitor
upstream releases or establish compatibility with deployed GKE/GMP components.

### Regenerate the current snapshot

From the repository root, with Go, Python 3 and Make available:

```sh
make gmp-sync
make verify-gmp-sync
```

Regenerating an unchanged source pin should produce no differences. The updater
downloads the selected module in isolation, extracts the supported data types,
copies the upstream license and CRD fixtures, records provenance and generates
deepcopies with the repository's pinned `controller-gen`. It does not change
HyperShift's root module or vendor directory. Ordinary `make generate` continues
to regenerate deepcopies offline.

`make verify-gmp-sync` verifies the recorded inputs and regenerates into temporary
storage. It fails on missing, different or unexpected generated files without
rewriting tracked files. It runs as part of `make lint` (and therefore `make
verify-ci`, `make verify`, and `make verify-parallel`), so a stale snapshot fails
CI; run it manually after any GMP snapshot, tooling or dependency change to catch
drift before pushing. `make gmp-sync` runs as part of `make update`. An offline
unit test in `hack/gmp-api-gen` runs with the regular unit tests and fails if
`upstream.sha256` no longer matches the version and checksums pinned in
`hack/gmp-api-source`.

### Update to a newer upstream version

1. Select a source version appropriate for the supported GKE/GMP environments and
   review its relevant API/schema changes with the GCP HCP team.
2. Update the `prometheus-engine` requirement in `hack/gmp-api-source/go.mod`.
3. Run `make gmp-sync`.
4. Run `make verify-gmp-sync` and the focused tests:

   ```sh
   GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor go test -race \
     ./hack/gmp-api-gen ./support/gmpmonitoring
   ```

5. Review the generated diff and include compatibility evidence in the PR.

Expected changes are limited to the source pin/checksums, generated provenance,
API types, deepcopies, license and schema fixtures. Unexpected runtime dependencies
or extraction failures require review by the owning team; do not work around them
by editing generated files. The extractor follows the type closure plus the
monitoring condition constant group only. If an added type requires another
constant, extend the generator deliberately.

The pinned source is always downloaded with checksum database verification
(`GOSUMDB=sum.golang.org`), regardless of local `GOPRIVATE`/`GONOSUMDB` settings.
Generation fails if copied upstream source contains Go directives such as
`//go:generate` or build constraints.

## Compatibility checks

The copied source version does not need to numerically match the deployed GMP
image version. Compatibility depends on the fields HyperShift emits and the
schemas and behavior supported by the deployed components. Record the GKE version,
GMP operator image and validation performed when assessing compatibility.

Tests cover scheme registration in fresh default (CoreOS) and RHOBS processes, actual
HyperShift YAML/JSON serializer round trips (objects and lists, including status),
deepcopy isolation and emitted objects against pinned upstream v1 OpenAPI schemas
with negative controls. Schema fixtures are test input only; HyperShift does not
install these CRDs.

Offline schema validation checks OpenAPI structure and constraints. It does not
execute Kubernetes CEL validation or Google's admission/runtime checks. The pinned
source is a serialization reference, not a claim that every deployed GKE cluster
uses that schema. Live CRD compatibility, scrape behavior and rule evaluation require
later validation against the selected GKE/GMP versions.
