#!/usr/bin/env bash
# run-eval.sh - Agent/Skill eval runner for HyperShift Claude Code configuration
#
# Usage:
#   ./run-eval.sh                              # Run all scenarios
#   ./run-eval.sh agents/api-sme               # Run scenarios in a subdirectory
#   ./run-eval.sh agents/api-sme/01-*.yaml     # Run specific scenario(s)
#   ./run-eval.sh --baseline                   # Capture baselines instead of comparing
#   ./run-eval.sh --model opus                 # Override model for all scenarios
#   ./run-eval.sh --dry-run                    # Show what would run without invoking claude
#
# Dependencies: bash, yq (v4+), claude CLI

set -uo pipefail

cleanup() { stop_spinner 2>/dev/null; }
trap cleanup EXIT INT TERM

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
SCENARIOS_DIR="${SCRIPT_DIR}/scenarios"
BASELINES_DIR="${SCRIPT_DIR}/baselines"
RESULTS_DIR="${SCRIPT_DIR}/results"
JUDGE_PROMPT_FILE="${SCRIPT_DIR}/judge-prompt.md"

# Defaults
BASELINE_MODE=false
DRY_RUN=false
MODEL_OVERRIDE=""
SCENARIO_FILTER=""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
NC='\033[0m' # No Color

usage() {
    sed -n '2,11p' "${BASH_SOURCE[0]}" | sed 's/^# *//'
    exit 0
}

log_info() { echo -e "${BLUE}[INFO]${NC} $*"; }
log_pass() { echo -e "${GREEN}[PASS]${NC} $*"; }
log_fail() { echo -e "${RED}[FAIL]${NC} $*"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_bold() { echo -e "${BOLD}$*${NC}"; }

# Spinner that shows elapsed seconds
SPINNER_PID=""
start_spinner() {
    local label="$1"
    local chars='⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏'
    local start=$SECONDS
    (
        i=0
        while true; do
            local elapsed=$(( SECONDS - start ))
            printf "\r  ${BLUE}%s${NC} %s [%ds]  " "${chars:i%${#chars}:1}" "$label" "$elapsed"
            sleep 0.2
            ((i++)) || true
        done
    ) &
    SPINNER_PID=$!
    disown "$SPINNER_PID" 2>/dev/null
}

stop_spinner() {
    if [[ -n "$SPINNER_PID" ]]; then
        kill "$SPINNER_PID" 2>/dev/null
        wait "$SPINNER_PID" 2>/dev/null || true
        SPINNER_PID=""
        printf "\r\033[K"  # clear the spinner line
    fi
}

check_dependencies() {
    local missing=()
    command -v yq >/dev/null 2>&1 || missing+=("yq")
    command -v claude >/dev/null 2>&1 || missing+=("claude")

    if [[ ${#missing[@]} -gt 0 ]]; then
        echo "Missing required dependencies: ${missing[*]}"
        echo "Install with:"
        [[ " ${missing[*]} " == *" yq "* ]] && echo "  brew install yq"
        [[ " ${missing[*]} " == *" claude "* ]] && echo "  See https://docs.anthropic.com/en/docs/claude-code"
        exit 1
    fi
}

parse_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --baseline)
                BASELINE_MODE=true
                shift
                ;;
            --dry-run)
                DRY_RUN=true
                shift
                ;;
            --model)
                MODEL_OVERRIDE="$2"
                shift 2
                ;;
            --help|-h)
                usage
                ;;
            *)
                SCENARIO_FILTER="$1"
                shift
                ;;
        esac
    done
}

find_scenarios() {
    local filter="${1:-}"
    if [[ -z "$filter" ]]; then
        find "${SCENARIOS_DIR}" -name '*.yaml' -type f | sort
    elif [[ -f "${SCENARIOS_DIR}/${filter}" ]]; then
        echo "${SCENARIOS_DIR}/${filter}"
    elif [[ -d "${SCENARIOS_DIR}/${filter}" ]]; then
        find "${SCENARIOS_DIR}/${filter}" -name '*.yaml' -type f | sort
    else
        # Try as glob pattern
        local expanded
        expanded=$(find "${SCENARIOS_DIR}" -path "*${filter}*" -name '*.yaml' -type f 2>/dev/null | sort)
        if [[ -z "$expanded" ]]; then
            echo "No scenarios matching: ${filter}" >&2
            exit 1
        fi
        echo "$expanded"
    fi
}

# Get the relative scenario path (e.g., agents/api-sme/01-api-design-review)
scenario_rel_path() {
    local yaml_file="$1"
    local rel="${yaml_file#${SCENARIOS_DIR}/}"
    echo "${rel%.yaml}"
}

run_claude_scenario() {
    local yaml_file="$1"
    local output_file="$2"

    local target model allowed_tools max_budget prompt scenario_type
    scenario_type=$(yq '.type' "$yaml_file")
    target=$(yq '.target' "$yaml_file")
    model=$(yq '.invocation.model // "sonnet"' "$yaml_file")
    local has_allowed_tools
    has_allowed_tools=$(yq '.invocation | has("allowed_tools")' "$yaml_file")
    allowed_tools=$(yq '.invocation.allowed_tools // ""' "$yaml_file")
    max_budget=$(yq '.invocation.max_budget_usd // "1.00"' "$yaml_file")
    prompt=$(yq '.prompt' "$yaml_file")

    # Apply model override if specified
    if [[ -n "$MODEL_OVERRIDE" ]]; then
        model="$MODEL_OVERRIDE"
    fi

    # Build claude command
    # When tools are disabled (allowed_tools=""), use --permission-mode default to
    # prevent the agent from entering plan mode and writing to a plan file (whose
    # content is lost in --output-format text). This is safe because no tools are
    # available to approve anyway.
    local perm_mode="plan"
    if [[ "$has_allowed_tools" == "true" && -z "$allowed_tools" ]]; then
        perm_mode="default"
    fi
    local cmd=(claude -p --output-format text --permission-mode "$perm_mode" --no-session-persistence)
    cmd+=(--model "$model")
    cmd+=(--max-budget-usd "$max_budget")

    if [[ "$scenario_type" == "agent" && -n "$target" ]]; then
        cmd+=(--agent "$target")
    fi

    # If allowed_tools is explicitly set in the YAML (even to ""), pass it through.
    # Empty string disables all tools; omitting the field uses defaults.
    if [[ "$has_allowed_tools" == "true" ]]; then
        cmd+=(--allowed-tools "$allowed_tools")
    fi

    if $DRY_RUN; then
        echo "  CMD: ${cmd[*]} \"<prompt>\""
        echo "  PROMPT: $(echo "$prompt" | head -3)..."
        return 0
    fi

    # Run claude and capture output (stderr to separate file for debugging)
    local stderr_file="${output_file%.md}.stderr"
    local exit_code=0
    start_spinner "Running agent..."
    echo "$prompt" | "${cmd[@]}" > "$output_file" 2>"$stderr_file" || exit_code=$?
    stop_spinner

    # Check for budget exceeded or other errors in the output
    if grep -q "^Error:" "$output_file" 2>/dev/null; then
        local error_msg
        error_msg=$(grep "^Error:" "$output_file" | head -1)
        log_warn "claude returned error: $error_msg"
    fi

    if [[ $exit_code -ne 0 ]]; then
        log_warn "claude exited with code $exit_code"
        echo "[ERROR] claude exited with code $exit_code" >> "$output_file"
    fi

    return 0
}

check_pattern() {
    local output_file="$1"
    local pattern="$2"
    local min_matches="${3:-1}"

    local count
    count=$(grep -cP "$pattern" "$output_file" 2>/dev/null || echo "0")
    if [[ "$count" -ge "$min_matches" ]]; then
        echo "PASS|Found $count matches (minimum: $min_matches)"
    else
        echo "FAIL|Found $count matches (minimum: $min_matches)"
    fi
}

check_contains() {
    local output_file="$1"
    local values_json="$2"

    local missing=()
    local count
    count=$(echo "$values_json" | yq '. | length')

    for ((i=0; i<count; i++)); do
        local value
        value=$(echo "$values_json" | yq ".[$i]")
        if ! grep -qF "$value" "$output_file" 2>/dev/null; then
            missing+=("$value")
        fi
    done

    if [[ ${#missing[@]} -eq 0 ]]; then
        echo "PASS|All required values found"
    else
        echo "FAIL|Missing: ${missing[*]}"
    fi
}

check_not_contains() {
    local output_file="$1"
    local values_json="$2"

    local found=()
    local count
    count=$(echo "$values_json" | yq '. | length')

    for ((i=0; i<count; i++)); do
        local value
        value=$(echo "$values_json" | yq ".[$i]")
        if grep -qF "$value" "$output_file" 2>/dev/null; then
            found+=("$value")
        fi
    done

    if [[ ${#found[@]} -eq 0 ]]; then
        echo "PASS|None of the prohibited values found"
    else
        echo "FAIL|Found prohibited: ${found[*]}"
    fi
}

check_llm_judge() {
    local output_file="$1"
    local judge_prompt="$2"

    local output
    output=$(cat "$output_file")

    local judge_system_prompt
    judge_system_prompt=$(cat "$JUDGE_PROMPT_FILE")

    local judge_input
    judge_input="## Agent Output Under Review
${output}

## Evaluation Criterion
${judge_prompt}

Answer with exactly one word on the first line: PASS or FAIL
Then provide a brief explanation."

    local judge_response
    judge_response=$(echo "$judge_input" | claude -p \
        --model claude-opus-4-6 \
        --allowed-tools "" \
        --max-budget-usd 0.50 \
        --no-session-persistence \
        --output-format text \
        --system-prompt "$judge_system_prompt" 2>/dev/null || echo "FAIL judge invocation error")

    local verdict
    verdict=$(echo "$judge_response" | head -1 | tr -d '[:space:]' | tr '[:lower:]' '[:upper:]')

    local explanation
    explanation=$(echo "$judge_response" | tail -n +2 | head -3 | tr '\n' ' ' | sed 's/  */ /g')

    if [[ "$verdict" == "PASS" ]]; then
        echo "PASS|${explanation}"
    else
        echo "FAIL|${explanation}"
    fi
}

# Result is written to BASELINE_RESULT global (verdict|explanation)
BASELINE_RESULT=""

compare_baseline() {
    local output_file="$1"
    local baseline_file="$2"

    local output baseline
    output=$(cat "$output_file")
    baseline=$(cat "$baseline_file")

    local judge_system_prompt
    judge_system_prompt=$(cat "$JUDGE_PROMPT_FILE")

    local compare_input="## Baseline Output (Known Good)
${baseline}

## Current Output
${output}

## Comparison Task
Compare the current output against the baseline. Determine if the
current output is:
- EQUIVALENT: Same quality and coverage as baseline
- IMPROVED: Better than baseline (more thorough, more accurate)
- REGRESSED: Worse than baseline (missing key points, incorrect)

Answer with one word: EQUIVALENT, IMPROVED, or REGRESSED
Then explain key differences."

    start_spinner "Comparing to baseline..."
    local judge_response
    judge_response=$(echo "$compare_input" | claude -p \
        --model claude-opus-4-6 \
        --allowed-tools "" \
        --max-budget-usd 0.50 \
        --no-session-persistence \
        --output-format text \
        --system-prompt "$judge_system_prompt" 2>/dev/null || echo "ERROR judge invocation error")
    stop_spinner

    local verdict
    verdict=$(echo "$judge_response" | head -1 | tr -d '[:space:]' | tr '[:lower:]' '[:upper:]')

    local explanation
    explanation=$(echo "$judge_response" | tail -n +2 | head -3 | tr '\n' ' ' | sed 's/  */ /g')

    BASELINE_RESULT="${verdict}|${explanation}"
}

# Result is written to CRITERIA_RESULT global (pass:fail:total)
# to avoid subshell capture which breaks spinners
CRITERIA_RESULT=""

run_criteria_checks() {
    local yaml_file="$1"
    local output_file="$2"

    local criteria_count
    criteria_count=$(yq '.criteria | length' "$yaml_file")
    local pass_count=0
    local fail_count=0

    for ((i=0; i<criteria_count; i++)); do
        local criterion_id check_type
        criterion_id=$(yq ".criteria[$i].id" "$yaml_file")
        check_type=$(yq ".criteria[$i].check_type" "$yaml_file")

        local result=""
        case "$check_type" in
            pattern)
                local pattern min_matches
                pattern=$(yq ".criteria[$i].pattern" "$yaml_file")
                min_matches=$(yq ".criteria[$i].min_matches // 1" "$yaml_file")
                result=$(check_pattern "$output_file" "$pattern" "$min_matches")
                ;;
            contains)
                local values_json
                values_json=$(yq -o=json ".criteria[$i].values" "$yaml_file")
                result=$(check_contains "$output_file" "$values_json")
                ;;
            not_contains)
                local values_json
                values_json=$(yq -o=json ".criteria[$i].values" "$yaml_file")
                result=$(check_not_contains "$output_file" "$values_json")
                ;;
            llm_judge)
                local judge_prompt
                judge_prompt=$(yq ".criteria[$i].judge_prompt" "$yaml_file")
                start_spinner "Judging: ${criterion_id}"
                result=$(check_llm_judge "$output_file" "$judge_prompt")
                stop_spinner
                ;;
            *)
                result="FAIL|Unknown check_type: $check_type"
                ;;
        esac

        local verdict="${result%%|*}"
        local detail="${result#*|}"

        if [[ "$verdict" == "PASS" ]]; then
            ((pass_count++)) || true
            log_pass "  ${criterion_id}: ${detail}"
        else
            ((fail_count++)) || true
            log_fail "  ${criterion_id}: ${detail}"
        fi
    done

    CRITERIA_RESULT="${pass_count}:${fail_count}:${criteria_count}"
}

main() {
    parse_args "$@"
    check_dependencies

    local timestamp
    timestamp=$(date +%Y-%m-%dT%H:%M:%S)
    local run_dir="${RESULTS_DIR}/${timestamp}"

    if ! $DRY_RUN; then
        mkdir -p "$run_dir"
    fi

    echo ""
    log_bold "========================================"
    log_bold " HyperShift Agent/Skill Eval"
    if $BASELINE_MODE; then
        log_bold " Mode: BASELINE CAPTURE"
    elif $DRY_RUN; then
        log_bold " Mode: DRY RUN"
    else
        log_bold " Mode: EVALUATE"
    fi
    [[ -n "$MODEL_OVERRIDE" ]] && log_bold " Model override: $MODEL_OVERRIDE"
    log_bold " $(date)"
    log_bold "========================================"
    echo ""

    local scenario_files
    scenario_files=$(find_scenarios "$SCENARIO_FILTER")

    local total_scenarios=0
    local total_pass=0
    local total_fail=0
    local total_criteria=0
    local summary_lines=()
    local baseline_summary_lines=()

    while IFS= read -r yaml_file; do
        [[ -z "$yaml_file" ]] && continue
        ((total_scenarios++)) || true

        local rel_path
        rel_path=$(scenario_rel_path "$yaml_file")
        local name
        name=$(yq '.name' "$yaml_file")

        log_bold "--- ${name} ---"
        log_info "Scenario: ${rel_path}"

        # Set up output paths
        local output_dir="${run_dir}/$(dirname "$rel_path")"
        local output_file="${run_dir}/${rel_path}.output.md"
        local baseline_file="${BASELINES_DIR}/${rel_path}.baseline.md"

        if ! $DRY_RUN; then
            mkdir -p "$output_dir"
        fi

        # Run the scenario
        run_claude_scenario "$yaml_file" "$output_file"

        if $DRY_RUN; then
            echo ""
            continue
        fi

        # Baseline mode: save and move on
        if $BASELINE_MODE; then
            mkdir -p "$(dirname "$baseline_file")"
            cp "$output_file" "$baseline_file"
            log_info "Baseline saved: ${baseline_file#${SCRIPT_DIR}/}"
            echo ""
            continue
        fi

        # Run criteria checks (result in CRITERIA_RESULT global)
        run_criteria_checks "$yaml_file" "$output_file"
        local pass_count="${CRITERIA_RESULT%%:*}"
        local rest="${CRITERIA_RESULT#*:}"
        local fail_count="${rest%%:*}"
        local criteria_count="${rest#*:}"

        ((total_pass += pass_count)) || true
        ((total_fail += fail_count)) || true
        ((total_criteria += criteria_count)) || true

        local scenario_verdict="PASS"
        if [[ "$fail_count" -gt 0 ]]; then
            scenario_verdict="FAIL"
        fi

        summary_lines+=("${scenario_verdict}|${rel_path}|${pass_count}/${criteria_count}")

        # Baseline comparison if baseline exists
        if [[ -f "$baseline_file" ]]; then
            compare_baseline "$output_file" "$baseline_file"
            local compare_verdict="${BASELINE_RESULT%%|*}"
            local compare_detail="${BASELINE_RESULT#*|}"

            case "$compare_verdict" in
                EQUIVALENT) log_info "  Baseline: ${GREEN}EQUIVALENT${NC} - ${compare_detail}" ;;
                IMPROVED)   log_info "  Baseline: ${GREEN}IMPROVED${NC} - ${compare_detail}" ;;
                REGRESSED)  log_warn "  Baseline: ${RED}REGRESSED${NC} - ${compare_detail}" ;;
                *)          log_warn "  Baseline: ${compare_verdict} - ${compare_detail}" ;;
            esac
            baseline_summary_lines+=("${compare_verdict}|${rel_path}")
        else
            baseline_summary_lines+=("N/A|${rel_path}")
        fi

        echo ""
    done <<< "$scenario_files"

    if $DRY_RUN || $BASELINE_MODE; then
        echo ""
        if $BASELINE_MODE; then
            log_bold "Baselines captured for ${total_scenarios} scenario(s)."
        else
            log_bold "Dry run complete. ${total_scenarios} scenario(s) found."
        fi
        return 0
    fi

    # Print summary table
    echo ""
    log_bold "========================================"
    log_bold " RESULTS SUMMARY"
    log_bold "========================================"
    echo ""

    printf "  %-50s  %-8s  %s\n" "SCENARIO" "RESULT" "CRITERIA"
    printf "  %-50s  %-8s  %s\n" "--------" "------" "--------"

    for line in "${summary_lines[@]}"; do
        local verdict="${line%%|*}"
        local rest="${line#*|}"
        local path="${rest%%|*}"
        local criteria="${rest#*|}"

        local color="$GREEN"
        [[ "$verdict" == "FAIL" ]] && color="$RED"

        printf "  %-50s  ${color}%-8s${NC}  %s\n" "$path" "$verdict" "$criteria"
    done

    if [[ ${#baseline_summary_lines[@]} -gt 0 ]]; then
        echo ""
        log_bold "BASELINE COMPARISON"
        for line in "${baseline_summary_lines[@]}"; do
            local verdict="${line%%|*}"
            local path="${line#*|}"

            local color="$YELLOW"
            case "$verdict" in
                EQUIVALENT|IMPROVED) color="$GREEN" ;;
                REGRESSED) color="$RED" ;;
            esac

            printf "  %-50s  ${color}%s${NC}\n" "$path" "$verdict"
        done
    fi

    echo ""
    log_bold "========================================"
    local overall_color="$GREEN"
    [[ "$total_fail" -gt 0 ]] && overall_color="$RED"
    echo -e "  ${overall_color}${BOLD}OVERALL: ${total_pass}/${total_criteria} criteria passed | ${total_fail} failed${NC}"
    log_bold "  Results: ${run_dir#${REPO_ROOT}/}"
    log_bold "========================================"
    echo ""

    if [[ "$total_fail" -gt 0 ]]; then
        return 1
    fi
    return 0
}

main "$@"
