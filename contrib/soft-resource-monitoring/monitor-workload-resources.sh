#!/bin/bash
# monitor-workload-resources.sh
# Generic Kubernetes workload resource monitoring script

set -euo pipefail

# Configuration defaults
DEFAULT_DURATION=300  # 5 minutes default
DEFAULT_INTERVAL=5    # Sample every 5 seconds
DEFAULT_NAMESPACE="default"
DEFAULT_WORKLOAD_TYPE="deployment"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Supported workload types
SUPPORTED_WORKLOADS=(
    "deployment"
    "daemonset"
    "statefulset"
    "job"
    "cronjob"
    "replicaset"
    "pod"
)

# Function to display usage
usage() {
    local script_name
    script_name=$(basename "$0")
    cat << EOF
Usage: $script_name [OPTIONS] <WORKLOAD_NAME>

Required Arguments:
  WORKLOAD_NAME      Name of the workload to monitor

Options:
  -t, --type         Workload type (default: deployment)
                     Supported: ${SUPPORTED_WORKLOADS[*]}
  -n, --namespace    Kubernetes namespace (default: default)
  -d, --duration     Monitoring duration in seconds (default: 300)
  -i, --interval     Sample interval in seconds (default: 5)
  -o, --output       Output directory (default: auto-generated)
  -l, --label        Label selector instead of workload name
  -c, --container    Specific container name to monitor (optional)
  -h, --help         Show this help message

Examples:
  # Monitor a Deployment (default type)
  $script_name my-app

  # Monitor a DaemonSet
  $script_name -t daemonset global-pull-secret-syncer -n kube-system

  # Monitor a StatefulSet
  $script_name -t statefulset -n database postgres-cluster

  # Monitor using label selector
  $script_name -l "app=my-app,version=v1.0" -n production

  # Monitor for 10 minutes with 2-second intervals
  $script_name -d 600 -i 2 -t deployment my-app

  # Monitor specific container in workload
  $script_name -c main-container -t deployment my-app

  # Monitor a Job
  $script_name -t job -n batch-jobs data-processing-job

  # Monitor pods directly
  $script_name -t pod -l "run=test-pod" -n testing

Workload Types:
$(printf "  %-12s - %s\n" \
    "deployment" "Monitor Deployment pods" \
    "daemonset" "Monitor DaemonSet pods" \
    "statefulset" "Monitor StatefulSet pods" \
    "job" "Monitor Job pods" \
    "cronjob" "Monitor CronJob pods (latest execution)" \
    "replicaset" "Monitor ReplicaSet pods" \
    "pod" "Monitor pods directly")

EOF
}

# Function to log with timestamp and colors
log_with_timestamp() {
    local message=$1
    local level=${2:-INFO}
    local color=$NC

    case $level in
        ERROR) color=$RED ;;
        WARN)  color=$YELLOW ;;
        INFO)  color=$GREEN ;;
        DEBUG) color=$BLUE ;;
        SUCCESS) color=$PURPLE ;;
        HEADER) color=$CYAN ;;
    esac

    echo -e "[$(date '+%Y-%m-%d %H:%M:%S')] ${color}$message${NC}"
}

# Validate workload type
validate_workload_type() {
    local workload_type="$1"

    for supported in "${SUPPORTED_WORKLOADS[@]}"; do
        if [[ "$workload_type" == "$supported" ]]; then
            return 0
        fi
    done

    log_with_timestamp "❌ Unsupported workload type: $workload_type" ERROR
    log_with_timestamp "💡 Supported types: ${SUPPORTED_WORKLOADS[*]}" INFO
    exit 1
}

# Parse command line arguments
parse_args() {
    NAMESPACE="$DEFAULT_NAMESPACE"
    DURATION="$DEFAULT_DURATION"
    INTERVAL="$DEFAULT_INTERVAL"
    OUTPUT_DIR=""
    WORKLOAD_NAME=""
    WORKLOAD_TYPE="$DEFAULT_WORKLOAD_TYPE"
    LABEL_SELECTOR=""
    CONTAINER_NAME=""

    while [[ $# -gt 0 ]]; do
        case $1 in
            -t|--type)
                WORKLOAD_TYPE="$2"
                shift 2
                ;;
            -n|--namespace)
                NAMESPACE="$2"
                shift 2
                ;;
            -d|--duration)
                DURATION="$2"
                shift 2
                ;;
            -i|--interval)
                INTERVAL="$2"
                shift 2
                ;;
            -o|--output)
                OUTPUT_DIR="$2"
                shift 2
                ;;
            -l|--label)
                LABEL_SELECTOR="$2"
                shift 2
                ;;
            -c|--container)
                CONTAINER_NAME="$2"
                shift 2
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            -*)
                echo "❌ Unknown option: $1"
                usage
                exit 1
                ;;
            *)
                if [[ -z "$WORKLOAD_NAME" ]]; then
                    WORKLOAD_NAME="$1"
                else
                    echo "❌ Multiple workload names provided: $WORKLOAD_NAME and $1"
                    usage
                    exit 1
                fi
                shift
                ;;
        esac
    done

    # Validate workload type
    validate_workload_type "$WORKLOAD_TYPE"

    # Validate arguments
    if [[ -z "$WORKLOAD_NAME" && -z "$LABEL_SELECTOR" ]]; then
        echo "❌ Either workload name or label selector must be provided"
        usage
        exit 1
    fi

    if [[ -n "$WORKLOAD_NAME" && -n "$LABEL_SELECTOR" ]]; then
        echo "❌ Cannot specify both workload name and label selector"
        usage
        exit 1
    fi

    # Set default output directory
    if [[ -z "$OUTPUT_DIR" ]]; then
        local target_name="${WORKLOAD_NAME:-${LABEL_SELECTOR//[^a-zA-Z0-9]/-}}"
        OUTPUT_DIR="workload-monitoring-${WORKLOAD_TYPE}-${target_name}-$(date +%Y%m%d-%H%M%S)"
    fi

    # Validate numeric inputs
    if ! [[ "$DURATION" =~ ^[0-9]+$ ]] || [[ $DURATION -lt 10 ]]; then
        echo "❌ Duration must be a positive integer >= 10 seconds"
        exit 1
    fi

    if ! [[ "$INTERVAL" =~ ^[0-9]+$ ]] || [[ $INTERVAL -lt 1 ]]; then
        echo "❌ Interval must be a positive integer >= 1 second"
        exit 1
    fi
}

# Function to detect kubectl/oc command
detect_k8s_command() {
    if command -v oc &> /dev/null; then
        K8S_CMD="oc"
        TOP_CMD="oc adm top"
    elif command -v kubectl &> /dev/null; then
        K8S_CMD="kubectl"
        TOP_CMD="kubectl top"
    else
        log_with_timestamp "❌ Neither 'oc' nor 'kubectl' found in PATH" ERROR
        exit 1
    fi
    log_with_timestamp "🔧 Using command: $K8S_CMD" DEBUG
}

# Function to build pod selector based on workload type
build_pod_selector() {
    if [[ -n "$LABEL_SELECTOR" ]]; then
        echo "$LABEL_SELECTOR"
        return 0
    fi

    local selector=""

    case "$WORKLOAD_TYPE" in
        "deployment")
            # Get deployment's replica set and then its pod selector
            local replicaset
            replicaset=$($K8S_CMD get deployment "$WORKLOAD_NAME" -n "$NAMESPACE" -o jsonpath='{.metadata.labels.app}' 2>/dev/null || echo "")
            if [[ -n "$replicaset" ]]; then
                selector="app=$replicaset"
            else
                # Fallback to pod template labels
                local labels
                labels=$($K8S_CMD get deployment "$WORKLOAD_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.template.metadata.labels}' 2>/dev/null || echo "")
                if [[ -n "$labels" ]]; then
                    selector=$(echo "$labels" | jq -r 'to_entries | map("\(.key)=\(.value)") | join(",")' 2>/dev/null || echo "app=$WORKLOAD_NAME")
                else
                    selector="app=$WORKLOAD_NAME"
                fi
            fi
            ;;
        "daemonset")
            local labels
            labels=$($K8S_CMD get daemonset "$WORKLOAD_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.selector.matchLabels}' 2>/dev/null || echo "")
            if [[ -n "$labels" ]]; then
                selector=$(echo "$labels" | jq -r 'to_entries | map("\(.key)=\(.value)") | join(",")' 2>/dev/null || echo "name=$WORKLOAD_NAME")
            else
                selector="name=$WORKLOAD_NAME"
            fi
            ;;
        "statefulset")
            local labels
            labels=$($K8S_CMD get statefulset "$WORKLOAD_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.selector.matchLabels}' 2>/dev/null || echo "")
            if [[ -n "$labels" ]]; then
                selector=$(echo "$labels" | jq -r 'to_entries | map("\(.key)=\(.value)") | join(",")' 2>/dev/null || echo "app=$WORKLOAD_NAME")
            else
                selector="app=$WORKLOAD_NAME"
            fi
            ;;
        "job")
            selector="job-name=$WORKLOAD_NAME"
            ;;
        "cronjob")
            # Get the latest job from the cronjob
            local latest_job
            latest_job=$($K8S_CMD get jobs -n "$NAMESPACE" -l "cronjob=$WORKLOAD_NAME" --sort-by=.metadata.creationTimestamp -o jsonpath='{.items[-1].metadata.name}' 2>/dev/null || echo "")
            if [[ -n "$latest_job" ]]; then
                selector="job-name=$latest_job"
            else
                selector="cronjob=$WORKLOAD_NAME"
            fi
            ;;
        "replicaset")
            local labels
            labels=$($K8S_CMD get replicaset "$WORKLOAD_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.selector.matchLabels}' 2>/dev/null || echo "")
            if [[ -n "$labels" ]]; then
                selector=$(echo "$labels" | jq -r 'to_entries | map("\(.key)=\(.value)") | join(",")' 2>/dev/null || echo "app=$WORKLOAD_NAME")
            else
                selector="app=$WORKLOAD_NAME"
            fi
            ;;
        "pod")
            selector="metadata.name=$WORKLOAD_NAME"
            ;;
    esac

    echo "$selector"
}

# Function to validate target exists
validate_target() {
    local selector="$1"

    log_with_timestamp "🔍 Validating $WORKLOAD_TYPE '$WORKLOAD_NAME' with selector: $selector" DEBUG

    # First check if the workload resource exists (except for pod type)
    if [[ "$WORKLOAD_TYPE" != "pod" && -n "$WORKLOAD_NAME" ]]; then
        if ! $K8S_CMD get "$WORKLOAD_TYPE" "$WORKLOAD_NAME" -n "$NAMESPACE" &>/dev/null; then
            log_with_timestamp "❌ $WORKLOAD_TYPE '$WORKLOAD_NAME' not found in namespace '$NAMESPACE'" ERROR
            log_with_timestamp "💡 Try: $K8S_CMD get $WORKLOAD_TYPE -n $NAMESPACE" INFO
            exit 1
        fi
        log_with_timestamp "✅ Found $WORKLOAD_TYPE '$WORKLOAD_NAME'" SUCCESS
    fi

    # Check if pods exist
    local pods
    if [[ "$WORKLOAD_TYPE" == "pod" && -n "$WORKLOAD_NAME" ]]; then
        pods=$($K8S_CMD get pod "$WORKLOAD_NAME" -n "$NAMESPACE" -o name 2>/dev/null || echo "")
    else
        pods=$($K8S_CMD get pods -n "$NAMESPACE" -l "$selector" -o name 2>/dev/null || echo "")
    fi

    if [[ -z "$pods" ]]; then
        if [[ -n "$WORKLOAD_NAME" ]]; then
            log_with_timestamp "❌ No pods found for $WORKLOAD_TYPE '$WORKLOAD_NAME' in namespace '$NAMESPACE'" ERROR
            case "$WORKLOAD_TYPE" in
                "deployment"|"statefulset"|"daemonset")
                    log_with_timestamp "💡 Check if the workload is running: $K8S_CMD get $WORKLOAD_TYPE $WORKLOAD_NAME -n $NAMESPACE" INFO
                    ;;
                "job")
                    log_with_timestamp "💡 Check job status: $K8S_CMD get job $WORKLOAD_NAME -n $NAMESPACE" INFO
                    ;;
                "cronjob")
                    log_with_timestamp "💡 Check recent jobs: $K8S_CMD get jobs -n $NAMESPACE -l cronjob=$WORKLOAD_NAME" INFO
                    ;;
            esac
        else
            log_with_timestamp "❌ No pods found with label selector '$LABEL_SELECTOR' in namespace '$NAMESPACE'" ERROR
            log_with_timestamp "💡 Try: $K8S_CMD get pods -n $NAMESPACE -l '$LABEL_SELECTOR'" INFO
        fi
        exit 1
    fi

    local pod_count
    pod_count=$(echo "$pods" | wc -l)
    log_with_timestamp "✅ Found $pod_count pod(s) for monitoring" SUCCESS

    # List the pods
    echo "$pods" | while read -r pod; do
        local pod_name
        pod_name=$(basename "$pod")
        log_with_timestamp "   📦 $pod_name" DEBUG
    done
}

# Function to monitor resources
monitor_resources() {
    local selector="$1"
    local samples=0
    local cpu_sum=0
    local mem_sum=0
    local cpu_max=0
    local mem_max=0
    local cpu_min=999999
    local mem_min=999999

    # CSV files
    local resources_csv="$OUTPUT_DIR/resources.csv"
    local summary_file="$OUTPUT_DIR/summary.txt"

    log_with_timestamp "📊 Starting resource monitoring..." INFO

    # Initialize CSV
    echo "timestamp,pod_name,container_name,cpu_millicores,memory_mb,memory_bytes" > "$resources_csv"

    local total_samples=$((DURATION / INTERVAL))

    for ((i=1; i<=total_samples; i++)); do
        local timestamp
        timestamp=$(date '+%Y-%m-%d %H:%M:%S')

        # Get all pods matching our selector
        local pods
        if [[ "$WORKLOAD_TYPE" == "pod" && -n "$WORKLOAD_NAME" ]]; then
            pods="$WORKLOAD_NAME"
        else
            pods=$($K8S_CMD get pods -n "$NAMESPACE" -l "$selector" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")
        fi

        if [[ -n "$pods" ]]; then
            for pod in $pods; do
                # Get resource metrics
                local metrics_cmd="$TOP_CMD pod $pod -n $NAMESPACE --containers --no-headers"
                local metrics
                metrics=$($metrics_cmd 2>/dev/null || echo "")

                if [[ -n "$metrics" ]]; then
                    # Parse metrics: POD CONTAINER CPU(cores) MEMORY(bytes)
                    while IFS= read -r line; do
                        if [[ -n "$line" ]]; then
                            local pod_name container_name cpu_raw mem_raw
                            read -r pod_name container_name cpu_raw mem_raw <<< "$line"

                            # Skip if container filter is specified and doesn't match
                            if [[ -n "$CONTAINER_NAME" && "$container_name" != "$CONTAINER_NAME" ]]; then
                                continue
                            fi

                            # Parse CPU (remove 'm' suffix, handle different formats)
                            local cpu_milli
                            cpu_milli=$(echo "$cpu_raw" | sed 's/m$//' | sed 's/[^0-9]//g')
                            [[ -z "$cpu_milli" ]] && cpu_milli=0

                            # Parse Memory (remove 'Mi' suffix, convert to MB)
                            local mem_mb
                            mem_mb=$(echo "$mem_raw" | sed 's/Mi$//' | sed 's/[^0-9]//g')
                            [[ -z "$mem_mb" ]] && mem_mb=0
                            local mem_bytes=$((mem_mb * 1024 * 1024))

                            # Record to CSV
                            echo "$timestamp,$pod_name,$container_name,$cpu_milli,$mem_mb,$mem_bytes" >> "$resources_csv"

                            # Update statistics
                            samples=$((samples + 1))
                            cpu_sum=$((cpu_sum + cpu_milli))
                            mem_sum=$((mem_sum + mem_mb))

                            [[ $cpu_milli -gt $cpu_max ]] && cpu_max=$cpu_milli
                            [[ $mem_mb -gt $mem_max ]] && mem_max=$mem_mb
                            [[ $cpu_milli -lt $cpu_min ]] && cpu_min=$cpu_milli
                            [[ $mem_mb -lt $mem_min ]] && mem_min=$mem_mb

                            log_with_timestamp "📊 $pod_name/$container_name: CPU=${cpu_milli}m, Memory=${mem_mb}Mi" DEBUG
                        fi
                    done <<< "$metrics"
                else
                    log_with_timestamp "⚠️  No metrics available for pod $pod" WARN
                fi
            done
        else
            log_with_timestamp "⚠️  No pods found with selector $selector" WARN
        fi

        # Progress indicator
        local progress=$((i * 100 / total_samples))
        printf "\r📈 Progress: %d%% (%d/%d samples)" $progress $i $total_samples

        sleep $INTERVAL
    done

    echo "" # New line after progress

    # Generate summary
    if [[ $samples -gt 0 ]]; then
        local cpu_avg=$((cpu_sum / samples))
        local mem_avg=$((mem_sum / samples))

        cat > "$summary_file" << EOF
Kubernetes Workload Resource Analysis Summary
=============================================
Workload Type: $WORKLOAD_TYPE
Target: ${WORKLOAD_NAME:-$LABEL_SELECTOR}
Namespace: $NAMESPACE
Monitoring Duration: ${DURATION}s
Sample Interval: ${INTERVAL}s
Total Samples: $samples

CPU Usage (millicores):
  Average: ${cpu_avg}m
  Maximum: ${cpu_max}m
  Minimum: ${cpu_min}m

Memory Usage (MB):
  Average: ${mem_avg}Mi
  Maximum: ${mem_max}Mi
  Minimum: ${mem_min}Mi

Resource Recommendations:
  Conservative CPU Request: $((cpu_max + 2))m
  Conservative Memory Request: $((mem_max + 5))Mi
  Optimized CPU Request: $((cpu_avg + 1))m
  Optimized Memory Request: $((mem_avg + 3))Mi

Workload-Specific Notes:
EOF
        case "$WORKLOAD_TYPE" in
            "deployment")
                echo "- Deployment resources apply to all replicas" >> "$summary_file"
                echo "- Consider horizontal scaling if CPU/memory consistently high" >> "$summary_file"
                echo "- Set requests and limits in deployment spec" >> "$summary_file"
                ;;
            "daemonset")
                echo "- DaemonSet runs one pod per node - resources multiply by node count" >> "$summary_file"
                echo "- Ensure node resources can accommodate the pod" >> "$summary_file"
                echo "- Consider node selectors/tolerations if needed" >> "$summary_file"
                ;;
            "statefulset")
                echo "- StatefulSet pods may have different resource patterns based on role" >> "$summary_file"
                echo "- Consider persistent volume storage in resource planning" >> "$summary_file"
                echo "- Monitor startup and scaling patterns" >> "$summary_file"
                ;;
            "job")
                echo "- Job resources are for batch workloads - consider completion time" >> "$summary_file"
                echo "- Factor in parallelism settings when planning cluster resources" >> "$summary_file"
                echo "- Monitor for memory leaks in long-running jobs" >> "$summary_file"
                ;;
            "cronjob")
                echo "- CronJob resource usage varies by schedule frequency" >> "$summary_file"
                echo "- Plan for concurrent job executions if needed" >> "$summary_file"
                echo "- Consider resource quotas for scheduled workloads" >> "$summary_file"
                ;;
            *)
                echo "- Monitor resource patterns specific to your workload type" >> "$summary_file"
                ;;
        esac

        cat >> "$summary_file" << EOF

Analysis Notes:
- Conservative recommendations include a buffer for peak usage
- Optimized recommendations are closer to average usage
- Consider workload patterns and cluster resources when setting final values
- Monitor during different load conditions for comprehensive analysis

Generated on: $(date)
EOF

        log_with_timestamp "✅ Resource monitoring complete. Summary written to $summary_file" SUCCESS
        echo ""
        cat "$summary_file"
    else
        log_with_timestamp "⚠️  No resource data collected" WARN
    fi
}

# Main execution
main() {
    echo "🔍 Generic Kubernetes Workload Resource Monitor"
    echo "================================================"

    # Parse arguments
    parse_args "$@"

    # Detect k8s command
    detect_k8s_command

    # Build selector and validate
    local selector
    selector=$(build_pod_selector)
    validate_target "$selector"

    # Display configuration
    echo ""
    log_with_timestamp "📋 Monitoring Configuration:" HEADER
    log_with_timestamp "   Workload Type: $WORKLOAD_TYPE" INFO
    log_with_timestamp "   Target: ${WORKLOAD_NAME:-$LABEL_SELECTOR}" INFO
    log_with_timestamp "   Namespace: $NAMESPACE" INFO
    log_with_timestamp "   Duration: ${DURATION}s" INFO
    log_with_timestamp "   Interval: ${INTERVAL}s" INFO
    log_with_timestamp "   Output: $OUTPUT_DIR" INFO
    log_with_timestamp "   Selector: $selector" DEBUG
    [[ -n "$CONTAINER_NAME" ]] && log_with_timestamp "   Container: $CONTAINER_NAME" INFO
    echo ""

    # Create output directory
    mkdir -p "$OUTPUT_DIR"

    # Start resource monitoring (blocking)
    monitor_resources "$selector"

    log_with_timestamp "🎉 Monitoring complete. Results saved to: $OUTPUT_DIR" SUCCESS
    echo ""
    echo "📁 Generated files:"
    echo "   📊 $OUTPUT_DIR/resources.csv - Resource usage data"
    echo "   📋 $OUTPUT_DIR/summary.txt - Analysis summary"
    echo ""
    echo "🔗 Quick analysis commands:"
    echo "   cat $OUTPUT_DIR/summary.txt"
    echo "   head $OUTPUT_DIR/resources.csv"
}

# Run main function with all arguments
main "$@"