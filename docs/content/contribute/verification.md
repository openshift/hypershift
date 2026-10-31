# Verification

Run the complete verification gate from a clean, committed checkout with Podman
or Docker running:

```bash
make verify
```

Local runs, GitHub Actions, Prow and the pre-push hook use the same verification
runtime and full workload. Host Go and Python are not required for local
verification. Choose an engine with `make verify RUNTIME=podman` or
`make verify RUNTIME=docker`.

After edits, run `make update`, inspect and commit the result, then run
`make verify`. Focused helpers such as `make lint` remain useful while developing.

## What verification checks

The complete `verify` workload includes these phases:

| Phase | Checks and preparation |
|-------|------------------------|
| Repository | `generate`, `update` (API/root/tools modules, workspace, provider types, CRDs, clients, API docs, aggregated docs and ownership rules), `verify-capi-sync`, `verify-capi-decoupling`, and a final clean-tree check |
| Repository | API dependency policy, CRD schema compatibility, spelling, Codecov configuration, CPO container file consistency, commit messages, docs navigation, paired Tekton pipelines and Make target orchestration tests |
| `lint` | Root-module `staticcheck` with its existing package list/default tags; `vet` with `integration,e2e,reqserving,e2ev2,backuprestore,envtest` tags; API and root `golangci-lint` with their existing configurations and comparison base |
| `lint` | `verify-fmt` checks the same root-module Go files as `go fmt ./...`, including tests and files excluded by build tags. `generate` prepares ignored mocks before analysis; it does not force provider synchronization. |

`make fmt` and `make lint-fix` remain explicit correction commands. The standalone
staticcheck and vet checks remain even where golangci-lint overlaps, because their
package and build-tag coverage differs. The separate dead-code gate remains
independent of these targets.

`verify-ci` is a compatibility alias for the same full `verify` workload until
existing callers in `openshift/release` migrate to `make verify`. This also gives
Prow the repository policy checks that previously ran only through local
`verify` or separate GitHub Actions workflows. The pre-push hook runs the full
containerized `make verify`. `verify-quick` remains an alias for `verify-generated`
for older callers; focused helpers remain useful
while developing, but do not replace the full gate. Verification finishes
generation and provider synchronization before starting readers. Do not run
independent `make verify` and `make lint` processes in the same checkout.

## Container execution and caches

Local verification, GitHub Actions verification jobs, and Prow verification use
the dedicated multi-platform image pinned in `hack/verify-image`. It is built
from `Dockerfile.verify` using Red Hat UBI Minimal and Go Toolset, and contains
Go 1.26.4, GCC, Make, Git, jq, curl and Python,
without GitHub Actions runner software or unrelated development CLIs. Repository
tools are built from the checkout's vendored sources and cached, so a PR that
changes a generator or linter still verifies its own tool code.

GHA resolves the pin before allocating a job container on the separate
`arc-verify-runner-set`. The ARC runner orchestrates the job; verification runs
in the dedicated image. Prow's configuration in `openshift/release` uses that
same image as its base and adds only the source checkout and write permissions.
Each context runs the complete `make verify` against its checkout of the PR code.

The local launcher mounts the checkout and shared Git metadata at their original
paths, so linked worktrees work too. Verification runs as your UID/GID and writes
generated changes back for inspection. Tools and binaries mask `hack/tools/bin`
and `bin`, keeping host binaries and Python packages separate. Three persistent
volumes hold tools, root binaries, and module/compiler/analyzer caches, separated
by image ID (including architecture and toolchain), checkout path and UID/GID.
This keeps cache traffic inside the Linux VM on macOS. The launcher prints the
image ID and volume names. A short initialization container sets ownership of the
volumes without mounting the checkout. Share the checkout and Git metadata paths
with your container service.

`hack/verify-in-container.sh` prepares writable HOME, Go, Python and analyzer
cache directories in every context. CI uses ephemeral temporary storage; the
local launcher supplies its persistent volume. Temporary files share that cache
volume so Python installations retain reusable SELinux labels. Git trusts only
the working checkout through process-local configuration; host Git configuration
is unchanged.

The launcher sets `VERIFY_CONTAINER=0` inside its container. Existing GitHub
Actions and OpenShift CI containers select this execution mode automatically
through `GITHUB_ACTIONS=true` and `OPENSHIFT_CI=true`, then use the same environment
setup and complete workload. They do not launch nested containers. GHA uses ARC's
Kubernetes container hooks to create its job pod. The verification scale set must
be deployed before enabling the workflow; see
[verification job containers](https://github.com/openshift/hypershift/blob/main/hack/github-actions-runner/README.md#verification-job-containers).

For native runs, the default verification cache is
`${TMPDIR:-/tmp}/hypershift-verify-<uid>`. Cache roots must be real directories
owned by the current user with mode `700`; unsafe existing paths are rejected
before cached executables enter `PATH`. Set `VERIFY_CACHE_DIR` to choose another
private cache directory.

## Publishing and updating the image

The Red Hat base images in `Dockerfile.verify` use tags so rebuilds pick up
Red Hat image updates. Consumers pin the published verification image by digest;
publishing a new image does not change an existing local or CI run's pin.

Provision the Konflux application, component and public image repository once
with `oc apply -f hack/verify-image-component.yaml` in the Konflux cluster. The
`hypershift-verify` component gets its own build service account and registry
credentials. This is independent of the existing runner component.

The `.tekton/hypershift-verify-*` pipelines publish amd64 and arm64 images to
`quay.io/redhat-user-workloads/crt-redhat-acm-tenant/hypershift-verify`.
Dockerfile changes build temporary PR images for validation. Pushes to `main`
that change `Dockerfile.verify` or its push pipeline publish a commit-SHA tag
and `latest`. The paired PR pipelines select the PR's pipeline definition when
it changes and otherwise resolve the shared pipeline from `main`.

Before enabling consumers, publish and validate an initial image. Resolve its
multi-platform digest and update `hack/verify-image` and the Prow verification
base in `openshift/release/ci-operator/config/openshift/hypershift/openshift-hypershift-main.yaml`
together. Use a non-expiring published image for the permanent pin. GHA reads
the pin from each checkout; publishing alone does not change it.

Updating the runtime does not require an ARC rollout. Runner software has its
own image pin and rollout; see the
[ARC deployment guide](https://github.com/openshift/hypershift/blob/main/hack/github-actions-runner/README.md#deploy-the-verification-scale-set).
Consolidating remaining separate lint/policy jobs and further performance
optimization remain follow-ups.

## Measuring performance

Compare the full `make verify` workload at both revisions; moving checks between
phases is not itself a full-workload speed improvement. Record the
revision, OS, CPU, RAM, Go version, command, exit status and cache state for every
run. A failed run is a partial measurement, not a successful baseline.

For a container baseline, use a clean, dedicated checkout and a fresh
`VERIFY_CACHE_KEY` (for example, `VERIFY_CACHE_KEY=baseline-1 make verify`); do not
clear your shared caches. Reuse the key for a warm run. Time the full `make verify`
and retain its log, which identifies the image and cache volumes. Record image pull
time separately if the image was not already cached. A fresh cache root also starts
module downloads, tools and standalone staticcheck analysis cold.

For native comparisons with historical revisions, explicitly use
`VERIFY_CONTAINER=0`. For example, on macOS:

```bash
git status --porcelain         # Must be empty before starting
git rev-parse HEAD
go version
sysctl machdep.cpu.brand_string hw.ncpu hw.memsize
go env GOMODCACHE GOOS GOARCH CGO_ENABLED GOTOOLCHAIN
benchmark_dir=$(mktemp -d /tmp/hypershift-verification.XXXXXX)
GOCACHE="$benchmark_dir/go-build" GOCACHEPROG= \
  GOLANGCI_LINT_CACHE="$benchmark_dir/golangci-cache" \
  /usr/bin/time -l make verify VERIFY_CONTAINER=0 > "$benchmark_dir/verify.log" 2>&1
benchmark_status=$?
echo "exit status: $benchmark_status; artifacts: $benchmark_dir"
```

This procedure starts with cold compiler and golangci-lint caches. The launcher
places module downloads and standalone staticcheck analysis below its verification
cache root, which may be warm from earlier runs. To start those caches cold too,
set `VERIFY_CACHE_DIR="$benchmark_dir/verification"` for the timed command. Record
the Go versions selected in `api/` and `hack/tools/` as well: automatic toolchain
selection can differ between modules. State whether `hack/tools/bin`, ignored mocks
and provider sync stamps existed; a fresh checkout starts without those tools
and generated artifacts. On Linux use `/usr/bin/time -v` and record hardware
with the platform's equivalents. Keep network download/validation failures in
the log. For repeatable comparisons, use the same host and Go version, avoid
other expensive work, and report multiple runs when assessing an optimization.
Prow job duration also includes image builds and scheduling; report the test
container's verification time separately from total job duration.
