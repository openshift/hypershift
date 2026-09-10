#!/bin/sh

set -eu

apply_patch() {
	patch_file="$1"
	if git apply --check --unidiff-zero "$patch_file" >/dev/null 2>&1; then
		git apply --whitespace=nowarn --unidiff-zero "$patch_file"
	elif git apply --reverse --check --unidiff-zero "$patch_file" >/dev/null 2>&1; then
		return 0
	else
		echo "cannot apply vendor patch: $patch_file" >&2
		exit 1
	fi
}

apply_patch hack/patches/library-go-k8s-1.37-builder.patch
apply_patch hack/patches/library-go-k8s-1.37-manifest.patch
