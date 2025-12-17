#!/bin/bash
set -euo pipefail

# OADP Recovery Script for HyperShift Clusters
# This script automatically recovers HyperShift clusters that were paused by OADP
# when their associated Velero backups reach terminal states.

# Default values
DRY_RUN="${DRY_RUN:-false}"
OADP_NAMESPACE="${OADP_NAMESPACE:-openshift-adp}"
LOG_LEVEL="${LOG_LEVEL:-info}"

# Global flag to track if any operation failed
MARK_AS_FAILED=false

# OADP annotations
OADP_PAUSED_BY_ANNOTATION="oadp.openshift.io/paused-by"
OADP_PAUSED_AT_ANNOTATION="oadp.openshift.io/paused-at"
OADP_PLUGIN_AUTHOR="hypershift-oadp-plugin"

# Terminal states for Velero backups
TERMINAL_STATES=("Completed" "Failed" "PartiallyFailed" "Deleted")

# Logging functions
log_info() {
    echo "[INFO] $(date '+%Y-%m-%d %H:%M:%S') $*"
}

log_verbose() {
    case "$LOG_LEVEL" in
        verbose|debug)
            echo "[VERBOSE] $(date '+%Y-%m-%d %H:%M:%S') $*"
            ;;
    esac
}

log_debug() {
    case "$LOG_LEVEL" in
        debug)
            echo "[DEBUG] $(date '+%Y-%m-%d %H:%M:%S') $*"
            ;;
    esac
}

log_error() {
    echo "[ERROR] $(date '+%Y-%m-%d %H:%M:%S') $*" >&2
}

# Function to check if kubectl is available
check_kubectl() {
    if ! command -v kubectl &> /dev/null; then
        log_error "kubectl is not available. Please install it."
        exit 1
    fi
}

# Function to check if cluster is paused by OADP
is_cluster_paused_by_oadp() {
    local cluster_name="$1"
    local cluster_namespace="$2"

    local paused_by paused_at
    paused_by=$(kubectl get hostedcluster "$cluster_name" -n "$cluster_namespace" \
        -o jsonpath="{.metadata.annotations.oadp\.openshift\.io/paused-by}" 2>/dev/null || echo "")
    paused_at=$(kubectl get hostedcluster "$cluster_name" -n "$cluster_namespace" \
        -o jsonpath="{.metadata.annotations.oadp\.openshift\.io/paused-at}" 2>/dev/null || echo "")

    if [[ "$paused_by" == "$OADP_PLUGIN_AUTHOR" && -n "$paused_at" ]]; then
        log_debug "Cluster $cluster_name is paused by OADP plugin (paused-at: $paused_at)"
        return 0
    fi

    return 1
}

# Function to check if a backup is in terminal state
is_backup_in_terminal_state() {
    local backup_name="$1"
    local backup_namespace="$2"

    local phase
    phase=$(kubectl get backup "$backup_name" -n "$backup_namespace" \
        -o jsonpath="{.status.phase}" 2>/dev/null || echo "")

    if [[ -z "$phase" ]]; then
        log_debug "Could not get phase for backup $backup_name"
        return 1
    fi

    for terminal_state in "${TERMINAL_STATES[@]}"; do
        if [[ "$phase" == "$terminal_state" ]]; then
            log_debug "Backup $backup_name is in terminal state: $phase"
            echo "$phase"
            return 0
        fi
    done

    log_debug "Backup $backup_name is not in terminal state: $phase"
    return 1
}

# Function to check if a backup is related to a cluster
is_backup_related_to_cluster() {
    local backup_name="$1"
    local backup_namespace="$2"
    local cluster_name="$3"
    local cluster_namespace="$4"

    # Strategy 1: Check backup name for cluster name patterns (more specific matching)
    # Match exact cluster name or cluster-specific patterns to reduce false positives
    if [[ "$backup_name" == *"${cluster_namespace}-${cluster_name}"* ||
          "$backup_name" == "${cluster_name}-"* ||
          "$backup_name" == *"-${cluster_name}-"* ||
          "$backup_name" == *"-${cluster_name}" ]]; then
        log_debug "Backup $backup_name is related to cluster $cluster_name (name pattern match)"
        return 0
    fi

    # Strategy 2: Check includedNamespaces
    local included_namespaces
    included_namespaces=$(kubectl get backup "$backup_name" -n "$backup_namespace" \
        -o jsonpath="{.spec.includedNamespaces[*]}" 2>/dev/null || echo "")

    if [[ -n "$included_namespaces" ]]; then
        for ns in $included_namespaces; do
            # More specific namespace matching to reduce false positives
            if [[ "$ns" == "$cluster_namespace" ||
                  "$ns" == "${cluster_namespace}-${cluster_name}" ||
                  "$ns" == "${cluster_name}-"* ]]; then
                log_debug "Backup $backup_name is related to cluster $cluster_name (includedNamespaces match: $ns)"
                return 0
            fi
        done
    fi

    return 1
}

# Function to find the most recent related backup for a cluster
find_last_related_backup() {
    local cluster_name="$1"
    local cluster_namespace="$2"

    local backups
    if ! backups=$(kubectl get backups -n "$OADP_NAMESPACE" --no-headers -o custom-columns=NAME:.metadata.name,CREATED:.metadata.creationTimestamp 2>/dev/null); then
        log_debug "No backups found in namespace $OADP_NAMESPACE"
        return 1
    fi

    local last_backup=""
    local last_backup_time=""

    # Process backups line by line to avoid issues with complex parsing
    echo "$backups" | while IFS=' ' read -r backup_name backup_time || [[ -n "$backup_name" ]]; do
        if [[ -z "$backup_name" ]]; then
            continue
        fi

        if is_backup_related_to_cluster "$backup_name" "$OADP_NAMESPACE" "$cluster_name" "$cluster_namespace"; then
            # Compare timestamps to find the newest backup
            if [[ -z "$last_backup_time" ]] || [[ "$backup_time" > "$last_backup_time" ]]; then
                last_backup="$backup_name"
                last_backup_time="$backup_time"
                log_debug "Found newer related backup: $backup_name (created: $backup_time)"
            fi
        fi
    done

    if [[ -n "$last_backup" ]]; then
        echo "$last_backup"
        return 0
    fi

    return 1
}

# Function to check if OADP recovery is needed for a cluster
check_oadp_recovery() {
    local cluster_name="$1"
    local cluster_namespace="$2"

    # Check if cluster is paused by OADP
    if ! is_cluster_paused_by_oadp "$cluster_name" "$cluster_namespace"; then
        log_debug "Cluster $cluster_name is not paused by OADP plugin"
        return 1
    fi

    log_verbose "Cluster $cluster_name is paused by OADP plugin, checking backup status"

    # Find related backups
    local last_backup
    if ! last_backup=$(find_last_related_backup "$cluster_name" "$cluster_namespace"); then
        log_verbose "No related backups found for OADP-paused cluster $cluster_name, should unpause"
        return 0
    fi

    log_debug "Found last related backup: $last_backup"

    # Check if the backup is in terminal state
    local backup_phase
    if backup_phase=$(is_backup_in_terminal_state "$last_backup" "$OADP_NAMESPACE"); then
        log_verbose "Last backup $last_backup is in terminal state ($backup_phase) - should unpause cluster $cluster_name"
        return 0
    fi

    log_verbose "Last backup $last_backup is still in progress - keeping cluster $cluster_name paused"
    return 1
}

# Function to resume cluster from OADP pause
resume_cluster_from_oadp() {
    local cluster_name="$1"
    local cluster_namespace="$2"

    log_info "Resuming cluster $cluster_name from OADP pause"

    # Remove OADP annotations from HostedCluster
    if [[ "$DRY_RUN" == "true" ]]; then
        log_info "DRY RUN: Would remove OADP annotations and unpause HostedCluster $cluster_name"
    else
        if ! kubectl annotate hostedcluster "$cluster_name" -n "$cluster_namespace" \
            "${OADP_PAUSED_BY_ANNOTATION}-" "${OADP_PAUSED_AT_ANNOTATION}-"; then
            log_error "Failed to remove OADP annotations from HostedCluster $cluster_name"
            MARK_AS_FAILED=true
            return 1
        fi

        # Clear pausedUntil field
        if ! kubectl patch hostedcluster "$cluster_name" -n "$cluster_namespace" \
            --type='merge' -p='{"spec":{"pausedUntil":null}}'; then
            log_error "Failed to clear pausedUntil field for HostedCluster $cluster_name"
            MARK_AS_FAILED=true
            return 1
        fi

        log_info "Successfully resumed HostedCluster $cluster_name"
    fi

    # Get NodePools for this cluster
    local nodepools
    if nodepools=$(kubectl get nodepools -n "$cluster_namespace" \
        -o jsonpath="{.items[?(@.spec.clusterName=='$cluster_name')].metadata.name}" 2>/dev/null); then

        for nodepool in $nodepools; do
            if [[ -z "$nodepool" ]]; then
                continue
            fi

            log_info "Resuming NodePool $nodepool for cluster $cluster_name"

            if [[ "$DRY_RUN" == "true" ]]; then
                log_info "DRY RUN: Would remove OADP annotations and unpause NodePool $nodepool"
            else
                # Remove OADP annotations from NodePool
                kubectl annotate nodepool "$nodepool" -n "$cluster_namespace" \
                    "${OADP_PAUSED_BY_ANNOTATION}-" "${OADP_PAUSED_AT_ANNOTATION}-" 2>/dev/null || true

                # Clear pausedUntil field
                if ! kubectl patch nodepool "$nodepool" -n "$cluster_namespace" \
                    --type='merge' -p='{"spec":{"pausedUntil":null}}'; then
                    log_error "Failed to clear pausedUntil field for NodePool $nodepool"
                    MARK_AS_FAILED=true
                    continue  # Continue with next NodePool instead of failing completely
                fi

                log_info "Successfully resumed NodePool $nodepool"
            fi
        done
    fi

    return 0
}

# Function to process all hosted clusters
process_clusters() {
    local total_clusters=0
    local processed_clusters=0
    local recovered_clusters=0
    local error_count=0
    local -a recovered_cluster_names=()

    log_info "Starting OADP recovery check (oadp-namespace: $OADP_NAMESPACE, dry-run: $DRY_RUN)"

    # Get all hosted clusters - simplified approach
    local clusters_output
    if ! clusters_output=$(kubectl get hostedclusters --all-namespaces --no-headers \
        -o custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name 2>/dev/null); then
        log_error "Failed to list hosted clusters"
        return 1
    fi

    if [[ -z "$clusters_output" ]]; then
        log_info "No hosted clusters found"
        return 0
    fi

    # Process each cluster
    while read -r line; do
        if [[ -z "$line" ]]; then
            continue
        fi

        # Parse namespace and name with explicit field extraction
        local cluster_namespace cluster_name
        cluster_namespace=$(echo "$line" | awk '{print $1}')
        cluster_name=$(echo "$line" | awk '{print $2}')

        if [[ -z "$cluster_name" ]]; then
            continue
        fi

        total_clusters=$((total_clusters + 1))
        log_verbose "Processing hosted cluster: $cluster_name (namespace: $cluster_namespace)"

        if check_oadp_recovery "$cluster_name" "$cluster_namespace"; then
            log_info "Cluster $cluster_name needs to be unpaused"

            if resume_cluster_from_oadp "$cluster_name" "$cluster_namespace"; then
                recovered_clusters=$((recovered_clusters + 1))
                recovered_cluster_names+=("$cluster_name")
                log_info "Successfully recovered cluster $cluster_name from OADP backup issue"
            else
                log_error "Failed to recover cluster $cluster_name"
                error_count=$((error_count + 1))
            fi
        fi

        processed_clusters=$((processed_clusters + 1))

    done <<< "$clusters_output"

    log_info "OADP recovery completed: total=$total_clusters, processed=$processed_clusters, recovered=$recovered_clusters, errors=$error_count"

    if [[ ${#recovered_cluster_names[@]} -gt 0 ]]; then
        log_info "Recovered clusters: ${recovered_cluster_names[*]}"
    fi

    # Check if any operation failed during recovery
    if [[ "$MARK_AS_FAILED" == "true" ]]; then
        log_error "Some recovery operations failed. Check logs above for details."
        return 1
    fi

    return 0
}

# Parse command line arguments
parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --dry-run)
                DRY_RUN="true"
                shift
                ;;
            --oadp-namespace)
                OADP_NAMESPACE="$2"
                shift 2
                ;;
            --log-level)
                LOG_LEVEL="$2"
                shift 2
                ;;
            --help|-h)
                cat << 'EOF'
OADP Recovery Script for HyperShift Clusters

This script automatically recovers HyperShift clusters that were paused by OADP
when their associated Velero backups reach terminal states.

Usage:
    $0 [options]

Options:
    --dry-run                   Enable dry-run mode (no changes made)
    --oadp-namespace NAMESPACE  OADP/Velero namespace (default: openshift-adp)
    --log-level LEVEL           Log verbosity: info, verbose, debug (default: info)
    --help, -h                  Show this help message

Environment Variables:
    DRY_RUN         - Set to "true" to enable dry-run mode (default: false)
    OADP_NAMESPACE  - OADP/Velero namespace (default: openshift-adp)
    LOG_LEVEL       - Log verbosity: info, verbose, debug (default: info)

EOF
                exit 0
                ;;
            *)
                log_error "Unknown option: $1"
                exit 1
                ;;
        esac
    done
}

# Main function
main() {
    parse_args "$@"

    # Validate log level
    case "$LOG_LEVEL" in
        info|verbose|debug)
            ;;
        *)
            log_error "Invalid log level: $LOG_LEVEL. Use: info, verbose, debug"
            exit 1
            ;;
    esac

    check_kubectl

    # Check if we can access the cluster
    if ! kubectl get --raw /version &>/dev/null; then
        log_error "Cannot connect to Kubernetes cluster. Please check your permissions."
        exit 1
    fi

    # Check if OADP namespace exists
    if ! kubectl get namespace "$OADP_NAMESPACE" &>/dev/null; then
        log_error "OADP namespace '$OADP_NAMESPACE' does not exist"
        exit 1
    fi

    process_clusters
}

# Execute main function with all arguments
main "$@"