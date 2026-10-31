#!/usr/bin/env bash
set -euo pipefail

# Keep the checkout at its host path: linked worktrees contain absolute Git paths.
repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
cd "$repo"
runtime=${RUNTIME:-$(sh hack/utils.sh get_container_engine)}
if ! "$runtime" info >/dev/null 2>&1; then
    echo "Cannot connect to $runtime; start its container service and retry make verify." >&2
    exit 1
fi

# Use the same published execution image as GHA jobs and Prow verification.
image=$(./hack/verify-image.sh)
if ! "$runtime" image inspect "$image" >/dev/null 2>&1; then
    "$runtime" pull "$image"
fi
image_id=$("$runtime" image inspect --format '{{.Id}}' "$image")
image_id=${image_id#sha256:}
sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum
    else
        shasum -a 256
    fi | cut -d ' ' -f 1
}
checkout_id=$(printf '%s' "$repo" | sha256)
cache_id=$(printf '%s\n' "$image_id" "$checkout_id" "$(id -u):$(id -g)" "${VERIFY_CACHE_KEY:-default}" | sha256)
cache="hypershift-verify-$cache_id"

# Docker can otherwise create missing nested bind-mount destinations as root.
mkdir -p "$repo/hack/tools/bin" "$repo/bin"

run_args=(run --rm)
if [[ $(basename "$runtime") == podman ]]; then
    run_args+=(--userns=keep-id)
fi
echo "Verification image: $image ($image_id)" >&2
echo "Verification cache volumes: $cache-cache $cache-tools $cache-bin" >&2

# Keep compiler/analyzer I/O inside the Linux VM, off macOS shared filesystems.
# Initialize only the mount roots as root; generation itself runs as the caller.
# shellcheck disable=SC2016 # Expand positional parameters in the container shell.
"$runtime" "${run_args[@]}" --user 0:0 \
    --volume "$cache-cache:/verify-cache:z" \
    --volume "$cache-tools:/verify-tools:z" \
    --volume "$cache-bin:/verify-bin:z" \
    --entrypoint /bin/sh "$image" -ec '
        mkdir -p /verify-cache/home /verify-cache/go /verify-cache/go-build /verify-cache/analysis /verify-cache/golangci-lint /verify-cache/tmp
        chown "$1" /verify-cache /verify-cache/* /verify-tools /verify-bin
        chmod 700 /verify-cache
    ' -- "$(id -u):$(id -g)"

args=("${run_args[@]}" --user "$(id -u):$(id -g)")
# pip --target moves packages from temporary files; keep their shared SELinux label
# when reusing the tools volume in a later container with a different MCS label.
args+=(--volume "$repo:$repo:z" --workdir "$repo"
       --volume "$cache-cache:/verify-cache:z"
       --volume "$cache-tools:$repo/hack/tools/bin:z"
       --volume "$cache-bin:$repo/bin:z"
       --env VERIFY_CONTAINER=0 --env VERIFY_CACHE_DIR=/verify-cache
       --env TMPDIR=/verify-cache/tmp)

common_dir=$(cd "$(git rev-parse --git-common-dir)" && pwd -P)
git_dir=$(git rev-parse --absolute-git-dir)
if [[ "$common_dir" != "$repo/"* ]]; then
    args+=(--volume "$common_dir:$common_dir:z")
fi
if [[ "$git_dir" != "$repo/"* && "$git_dir" != "$common_dir" && "$git_dir" != "$common_dir/"* ]]; then
    args+=(--volume "$git_dir:$git_dir:z")
fi

# Forward comparison refs, not the host Go environment or Make jobserver flags.
for variable in PULL_BASE_SHA PULL_PULL_SHA; do
    if [[ -n ${!variable:-} ]]; then
        args+=(--env "$variable")
    fi
done
if [[ $# == 0 ]]; then
    set -- make verify
fi
exec "$runtime" "${args[@]}" --entrypoint /bin/bash "$image" "$repo/hack/verify-in-container.sh" "$@"
