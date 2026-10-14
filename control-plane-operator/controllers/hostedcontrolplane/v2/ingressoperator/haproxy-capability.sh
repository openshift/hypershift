#!/bin/bash
set -euo pipefail
# Protocol: count, haproxy_args[0..count-1], defaultVersion, deprecatedVersion, originalCmd...
count="$1"
shift
haproxy_args=("${@:1:count}")
shift "$count"
default_version="$1"
deprecated_version="$2"
shift 2
if ! help_output=$(timeout --signal=KILL 30s "$1" start --help 2>&1); then
  echo "HAProxy capability detection failed: $help_output" >&2
  exit 1
fi
declare -A flags=()
while IFS= read -r line; do
  if [[ "$line" =~ ^[[:space:]]*(-[[:alnum:]],?[[:space:]]+)?--([[:alnum:]][[:alnum:]-]*)([[:space:]=]|$) ]]; then
    flags["${BASH_REMATCH[2]}"]=1
  fi
done <<< "$help_output"
if [[ -z "${flags[image]:-}" ]]; then
  echo "HAProxy capability detection failed: unrecognized ingress-operator help" >&2
  exit 1
fi
if [[ -z "${flags[haproxy-image]:-}" ]]; then
  echo "Ingress operator does not support HAProxy version selection; using --image" >&2
  exec "$@"
fi
if [[ -z "${flags[default-haproxy-version]:-}" ]]; then
  echo "HAProxy capability detection failed: --haproxy-image requires --default-haproxy-version" >&2
  exit 1
fi
haproxy_args+=("--default-haproxy-version=$default_version")
if [[ -n "${flags[deprecated-haproxy-version]:-}" ]]; then
  # An empty StringSlice flag clears the binary's default (2.8). Omitting it
  # would fail validation when 2.8 is not among the supplied HAProxy images.
  haproxy_args+=("--deprecated-haproxy-version=$deprecated_version")
fi
exec "$@" "${haproxy_args[@]}"
