#!/usr/bin/env bash
set -euo pipefail

mode="${1:---sync}"
if [[ $# -gt 1 || ( "$mode" != --sync && "$mode" != --verify ) ]]; then
    echo 'usage: hack/update-gmp-api.sh [--sync|--verify]' >&2
    exit 2
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_module="$repo_root/hack/gmp-api-source"
target="$repo_root/support/gmpmonitoring"
scratch_dir="$(mktemp -d)"
candidate_dir=""
trap 'rm -rf "$scratch_dir"; if [[ -n "$candidate_dir" ]]; then rm -rf "$candidate_dir"; fi' EXIT

# Download in a disposable module so verification cannot update tracked pins.
cp "$source_module/go.mod" "$scratch_dir/go.mod"
if [[ -f "$source_module/go.sum" ]]; then
    cp "$source_module/go.sum" "$scratch_dir/go.sum"
fi
(cd "$scratch_dir" && GO111MODULE=on GOWORK=off GOFLAGS= go mod edit -json) > "$scratch_dir/pin.json"
upstream_version="$(python3 - "$scratch_dir/pin.json" <<'PY'
import json, sys
with open(sys.argv[1]) as source:
    requirements = json.load(source).get('Require', [])
versions = [item['Version'] for item in requirements if item['Path'] == 'github.com/GoogleCloudPlatform/prometheus-engine']
if len(versions) != 1:
    raise SystemExit('source module must pin exactly one prometheus-engine version')
print(versions[0])
PY
)"
if ! (cd "$scratch_dir" && GO111MODULE=on GOWORK=off GOFLAGS= go mod download -json "github.com/GoogleCloudPlatform/prometheus-engine@$upstream_version") > "$scratch_dir/source.json"; then
    cat "$scratch_dir/source.json" >&2
    exit 1
fi
upstream_dir="$(python3 - "$scratch_dir/source.json" <<'PY'
import json, pathlib, sys
with open(sys.argv[1]) as source:
    info = json.load(source)
directory = info.get('Dir', '')
if info.get('Error') or not directory or not pathlib.Path(directory).is_dir():
    raise SystemExit('failed to resolve the pinned GMP source module')
print(directory)
PY
)"
if ! cmp -s "$source_module/go.mod" "$scratch_dir/go.mod"; then
    echo 'The source module changed during download; review its Go version before syncing.' >&2
    exit 1
fi

# A hidden package uses root vendoring without entering recursive package scans.
candidate_dir="$(mktemp -d "$repo_root/.gmp-api-gen.XXXXXX")"
cp "$target/doc.go" "$target/scheme.go" "$candidate_dir/"
cd "$repo_root"
GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor go run ./hack/gmp-api-gen \
    --upstream "$upstream_dir" --output "$candidate_dir" --source-info "$scratch_dir/source.json"
mkdir "$candidate_dir/testdata"
for resource in podmonitorings rules; do
    cp "$upstream_dir/charts/operator/crds/monitoring.googleapis.com_$resource.yaml" "$candidate_dir/testdata/$resource.crd.yaml"
done
make "$repo_root/hack/tools/bin/controller-gen"
GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor hack/tools/bin/controller-gen \
    object:headerFile=hack/boilerplate.go.txt paths="./$(basename "$candidate_dir")"

python3 - "$mode" "$target" "$candidate_dir" "$source_module" "$scratch_dir" <<'PY'
import pathlib, shutil, sys

mode = sys.argv[1]
target, candidate, source_module, scratch = map(pathlib.Path, sys.argv[2:])
outputs = {
    'types.go', 'zz_generated.deepcopy.go', 'upstream.sha256',
    'testdata/podmonitorings.crd.yaml', 'testdata/rules.crd.yaml',
}
existing = {
    str(path.relative_to(target))
    for path in (target / 'testdata').rglob('*') if path.is_file()
}
for path in target.glob('*.go'):
    if path.name.startswith('zz_generated.') or b'// Code generated ' in path.read_bytes()[:2048]:
        existing.add(path.name)
unexpected = sorted(existing - outputs)
if unexpected:
    raise SystemExit('Unexpected generated files; ask gcp-reviewers to review: ' + ', '.join(unexpected))
missing = sorted(name for name in outputs if not (candidate / name).is_file())
if missing:
    raise SystemExit('Generation did not produce expected files: ' + ', '.join(missing))

pairs = [(candidate / name, target / name) for name in sorted(outputs)]
if not (scratch / 'go.sum').is_file():
    raise SystemExit('Pinned source download did not produce go.sum; cannot record module checksums.')
pairs.append((scratch / 'go.sum', source_module / 'go.sum'))
if mode == '--verify':
    differences = [str(dst) for src, dst in pairs if not dst.is_file() or src.read_bytes() != dst.read_bytes()]
    if differences:
        raise SystemExit('GMP API files do not match the pinned source:\n' + '\n'.join(differences) +
                         '\nRun make gmp-sync, review the generated changes, and commit them.')
    print('GMP API snapshot matches the pinned source.')
else:
    for src, dst in pairs:
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(src, dst)
    print('GMP API snapshot synchronized. Run make verify-gmp-sync and the focused tests.')
PY
