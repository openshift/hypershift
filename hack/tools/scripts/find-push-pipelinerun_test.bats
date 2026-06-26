#!/usr/bin/env bats

setup() {
    SCRIPT_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
    source "${SCRIPT_DIR}/find-push-pipelinerun.sh"
}

# --- resolve_pr ---

@test "resolve_pr: parses full GitHub URL" {
    run resolve_pr "https://github.com/openshift/hypershift/pull/8761"
    [[ "$status" -eq 0 ]]
    [[ "$(sed -n '1p' <<< "$output")" == "openshift/hypershift" ]]
    [[ "$(sed -n '2p' <<< "$output")" == "8761" ]]
}

@test "resolve_pr: parses GitHub URL with trailing slash" {
    run resolve_pr "https://github.com/openshift/hypershift/pull/8761/"
    [[ "$status" -eq 0 ]]
    [[ "$(sed -n '1p' <<< "$output")" == "openshift/hypershift" ]]
    [[ "$(sed -n '2p' <<< "$output")" == "8761" ]]
}

@test "resolve_pr: parses owner/repo#number" {
    run resolve_pr "openshift/hypershift#8761"
    [[ "$status" -eq 0 ]]
    [[ "$(sed -n '1p' <<< "$output")" == "openshift/hypershift" ]]
    [[ "$(sed -n '2p' <<< "$output")" == "8761" ]]
}

@test "resolve_pr: parses owner/repo#number with org containing hyphens" {
    run resolve_pr "my-org/my-repo#42"
    [[ "$status" -eq 0 ]]
    [[ "$(sed -n '1p' <<< "$output")" == "my-org/my-repo" ]]
    [[ "$(sed -n '2p' <<< "$output")" == "42" ]]
}

@test "resolve_pr: rejects non-numeric non-URL input" {
    run resolve_pr "not-a-pr"
    [[ "$status" -ne 0 ]]
    [[ "$output" == *"unrecognized PR reference"* ]]
}

@test "resolve_pr: rejects empty input" {
    run resolve_pr ""
    [[ "$status" -ne 0 ]]
}

# --- format_pipelineruns ---

@test "format_pipelineruns: formats JSON items into table" {
    local json='{"items":[{"metadata":{"name":"my-pipeline-on-push-abc12","creationTimestamp":"2026-06-26T13:47:51Z","annotations":{"pipelinesascode.tekton.dev/log-url":"https://example.com/run/abc"}},"status":{"conditions":[{"reason":"Completed"}]}}]}'

    output="$(printf '%s' "${json}" | format_pipelineruns)"
    [[ "$output" == *"NAME"* ]]
    [[ "$output" == *"my-pipeline-on-push-abc12"* ]]
    [[ "$output" == *"Completed"* ]]
    [[ "$output" == *"https://example.com/run/abc"* ]]
}

@test "format_pipelineruns: sorts by creation timestamp" {
    local json='{"items":[{"metadata":{"name":"second","creationTimestamp":"2026-06-26T14:00:00Z","annotations":{}},"status":{"conditions":[{"reason":"Running"}]}},{"metadata":{"name":"first","creationTimestamp":"2026-06-26T13:00:00Z","annotations":{}},"status":{"conditions":[{"reason":"Completed"}]}}]}'

    output="$(printf '%s' "${json}" | format_pipelineruns)"
    local first_line second_line
    first_line="$(sed -n '2p' <<< "$output")"
    second_line="$(sed -n '3p' <<< "$output")"
    [[ "$first_line" == *"first"* ]]
    [[ "$second_line" == *"second"* ]]
}

@test "format_pipelineruns: returns 1 on empty items" {
    run format_pipelineruns <<< '{"items":[]}'
    [[ "$status" -ne 0 ]]
}

@test "format_pipelineruns: handles missing log-url annotation" {
    local json='{"items":[{"metadata":{"name":"no-url","creationTimestamp":"2026-06-26T13:00:00Z","annotations":{}},"status":{"conditions":[{"reason":"Completed"}]}}]}'

    output="$(printf '%s' "${json}" | format_pipelineruns)"
    [[ "$output" == *"no-url"* ]]
}

@test "format_pipelineruns: handles missing status conditions" {
    local json='{"items":[{"metadata":{"name":"no-status","creationTimestamp":"2026-06-26T13:00:00Z","annotations":{}},"status":{}}]}'

    output="$(printf '%s' "${json}" | format_pipelineruns)"
    [[ "$output" == *"<none>"* ]]
}

# --- filter_by_component ---

@test "filter_by_component: filters matching rows" {
    local table
    table="$(printf '%-50s %-20s %-25s %s\n' "NAME" "STATUS" "CREATED" "URL"
             printf '%-50s %-20s %-25s %s\n' "hypershift-cli-on-push-abc" "Failed" "2026-06-26T13:47:51Z" "https://example.com/1"
             printf '%-50s %-20s %-25s %s\n' "hypershift-release-on-push-def" "Completed" "2026-06-26T13:47:51Z" "https://example.com/2")"

    output="$(printf '%s\n' "${table}" | filter_by_component hypershift-release abc123)"
    [[ "$output" == *"hypershift-release"* ]]
    [[ "$output" != *"hypershift-cli"* ]]
}

@test "filter_by_component: returns 1 when no match" {
    local table
    table="$(printf '%-50s %-20s %-25s %s\n' "NAME" "STATUS" "CREATED" "URL"
             printf '%-50s %-20s %-25s %s\n' "hypershift-cli-on-push-abc" "Failed" "2026-06-26T13:47:51Z" "")"

    run filter_by_component nonexistent abc123 <<< "${table}"
    [[ "$status" -ne 0 ]]
    [[ "$output" == *"No push PipelineRuns found for component"* ]]
}

@test "filter_by_component: preserves header" {
    local table
    table="$(printf '%-50s %-20s %-25s %s\n' "NAME" "STATUS" "CREATED" "URL"
             printf '%-50s %-20s %-25s %s\n' "hypershift-release-on-push-def" "Completed" "2026-06-26T13:47:51Z" "")"

    output="$(printf '%s\n' "${table}" | filter_by_component hypershift-release abc123)"
    [[ "$(sed -n '1p' <<< "$output")" == *"NAME"* ]]
}
