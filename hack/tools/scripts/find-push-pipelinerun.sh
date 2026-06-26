#!/usr/bin/env bash
set -euo pipefail

readonly DEFAULT_NAMESPACE="crt-redhat-acm-tenant"
readonly DEFAULT_KA_HOST="https://kubearchive-api-server-product-kubearchive.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com"
readonly LOG_URL_ANNOTATION="pipelinesascode.tekton.dev/log-url"

usage() {
    printf "Find Konflux on-push PipelineRun(s) triggered by a merged PR.\n\n"
    printf "Usage: %s <PR> [COMPONENT]\n\n" "$(basename "$0")"
    printf "  PR          One of:\n"
    printf "                - PR number (e.g., 8761) — repo inferred via gh\n"
    printf "                - Full URL (e.g., https://github.com/openshift/hypershift/pull/8761)\n"
    printf "                - owner/repo#number (e.g., openshift/hypershift#8761)\n"
    printf "  COMPONENT   Optional: filter by component name prefix (e.g., hypershift-release)\n\n"
    printf "Environment variables:\n"
    printf "  KONFLUX_NAMESPACE  Konflux namespace (default: %s)\n" "${DEFAULT_NAMESPACE}"
    printf "  KUBEARCHIVE_HOST   KubeArchive API host (default: %s)\n" "${DEFAULT_KA_HOST}"
}

check_prerequisites() {
    local cmd
    for cmd in gh oc jq curl; do
        if ! command -v "${cmd}" &>/dev/null; then
            printf "Error: %s is required but not found in PATH\n" "${cmd}" >&2
            return 1
        fi
    done
}

resolve_pr() {
    local -r input="$1"
    local repo pr_number

    if [[ "${input}" =~ ^https://github\.com/([^/]+/[^/]+)/pull/([0-9]+) ]]; then
        repo="${BASH_REMATCH[1]}"
        pr_number="${BASH_REMATCH[2]}"
    elif [[ "${input}" =~ ^([^/]+/[^#]+)#([0-9]+)$ ]]; then
        repo="${BASH_REMATCH[1]}"
        pr_number="${BASH_REMATCH[2]}"
    elif [[ "${input}" =~ ^[0-9]+$ ]]; then
        pr_number="${input}"
        repo="$(gh repo view --json nameWithOwner -q '.nameWithOwner' 2>/dev/null)" || {
            printf "Error: could not determine repo via 'gh repo view'; use owner/repo#%s or a full URL\n" "${pr_number}" >&2
            return 1
        }
    else
        printf "Error: unrecognized PR reference: %s\n" "${input}" >&2
        usage >&2
        return 1
    fi

    printf '%s\n%s\n' "${repo}" "${pr_number}"
}

get_merge_sha() {
    local -r pr_number="$1"
    local -r repo="$2"
    local pr_json state sha

    pr_json="$(gh pr view "${pr_number}" --repo "${repo}" --json mergeCommit,state 2>&1)" || {
        printf "Error: could not fetch PR %s from %s\n" "${pr_number}" "${repo}" >&2
        return 1
    }

    state="$(printf '%s' "${pr_json}" | jq -r '.state')"
    if [[ "${state}" != "MERGED" ]]; then
        printf "Error: PR %s is not merged (state: %s)\n" "${pr_number}" "${state}" >&2
        return 1
    fi

    sha="$(printf '%s' "${pr_json}" | jq -r '.mergeCommit.oid')"
    printf '%s' "${sha}"
}

format_pipelineruns() {
    local items
    items="$(jq -r '
        [.items[] | {
            name: .metadata.name,
            status: (.status.conditions[0].reason // "<none>"),
            created: .metadata.creationTimestamp,
            url: (.metadata.annotations["'"${LOG_URL_ANNOTATION}"'"] // "")
        }]
        | sort_by(.created)
        | .[]
        | [.name, .status, .created, .url]
        | @tsv')"

    if [[ -z "${items}" ]]; then
        return 1
    fi

    printf "%-50s %-20s %-25s %s\n" "NAME" "STATUS" "CREATED" "URL"
    while IFS=$'\t' read -r name status created url; do
        printf "%-50s %-20s %-25s %s\n" "${name}" "${status}" "${created}" "${url}"
    done <<< "${items}"
}

query_pipelineruns_live() {
    local -r sha="$1"
    local -r namespace="$2"
    local -r selector="pipelinesascode.tekton.dev/sha=${sha},pipelinesascode.tekton.dev/event-type=push"
    local output

    output="$(oc get pipelineruns -n "${namespace}" -l "${selector}" -o json 2>&1)" || return 1

    printf '%s' "${output}" | format_pipelineruns
}

query_pipelineruns_archived() {
    local -r sha="$1"
    local -r namespace="$2"
    local -r ka_host="${KUBEARCHIVE_HOST:-${DEFAULT_KA_HOST}}"
    local -r selector="pipelinesascode.tekton.dev/sha=${sha},pipelinesascode.tekton.dev/event-type=push"
    local token response

    token="$(oc whoami -t 2>/dev/null)" || {
        printf "Error: could not get auth token via 'oc whoami -t'\n" >&2
        return 1
    }

    response="$(curl -sf -H "Authorization: Bearer ${token}" \
        "${ka_host}/apis/tekton.dev/v1/namespaces/${namespace}/pipelineruns?labelSelector=${selector}")" || {
        printf "Error: KubeArchive query failed (is %s reachable?)\n" "${ka_host}" >&2
        return 1
    }

    printf '%s' "${response}" | format_pipelineruns
}

filter_by_component() {
    local -r component="$1"
    local -r sha="$2"
    local output header filtered

    output="$(cat)"
    header="$(printf '%s\n' "${output}" | head -1)"
    filtered="$(printf '%s\n' "${output}" | tail -n +2 | grep "^${component}" || true)"
    if [[ -z "${filtered}" ]]; then
        printf "No push PipelineRuns found for component '%s' at commit %s\n" "${component}" "${sha}" >&2
        return 1
    fi
    printf '%s\n%s\n' "${header}" "${filtered}"
}

query_pipelineruns() {
    local -r sha="$1"
    local -r component="${2:-}"
    local -r namespace="${KONFLUX_NAMESPACE:-${DEFAULT_NAMESPACE}}"
    local output

    if output="$(query_pipelineruns_live "${sha}" "${namespace}" 2>/dev/null)"; then
        :
    else
        printf "No live PipelineRuns found, querying KubeArchive...\n" >&2
        output="$(query_pipelineruns_archived "${sha}" "${namespace}")" || {
            printf "No push PipelineRuns found for commit %s\n" "${sha}" >&2
            return 1
        }
        printf "(source: KubeArchive)\n\n" >&2
    fi

    if [[ -n "${component}" ]]; then
        printf '%s\n' "${output}" | filter_by_component "${component}" "${sha}"
    else
        printf '%s\n' "${output}"
    fi
}

main() {
    if [[ $# -lt 1 || "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
        usage
        return 0
    fi

    local -r component="${2:-}"
    local resolved repo pr_number sha

    check_prerequisites

    resolved="$(resolve_pr "$1")"
    repo="$(printf '%s\n' "${resolved}" | sed -n '1p')"
    pr_number="$(printf '%s\n' "${resolved}" | sed -n '2p')"

    sha="$(get_merge_sha "${pr_number}" "${repo}")"
    printf "PR https://github.com/%s/pull/%s merged at commit %s\n\n" "${repo}" "${pr_number}" "${sha}" >&2

    query_pipelineruns "${sha}" "${component}"
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
