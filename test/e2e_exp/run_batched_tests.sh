#!/bin/bash

set -euo pipefail

# Default values
BATCH_COUNT=3
REPEAT_COUNT=1
VERBOSE=false
FAILURE_PROB=0.0

# Parse command line arguments
while getopts "b:r:f:vh" opt; do
  case $opt in
    b)
      BATCH_COUNT=$OPTARG
      ;;
    r)
      REPEAT_COUNT=$OPTARG
      ;;
    f)
      FAILURE_PROB=$OPTARG
      ;;
    v)
      VERBOSE=true
      ;;
    h)
      echo "Usage: $0 [-b batch_count] [-r repeat_count] [-f failure_probability] [-v] [-h]"
      echo "  -b: Number of batches to split tests into (default: 3)"
      echo "  -r: Number of times to repeat the test run (default: 1)"
      echo "  -f: Probability of test failure between 0.0 and 1.0 (default: 0.0)"
      echo "  -v: Verbose mode"
      echo "  -h: Show this help message"
      exit 0
      ;;
    \?)
      echo "Invalid option: -$OPTARG" >&2
      exit 1
      ;;
  esac
done

# Function to run a single batch
run_batch() {
    local batch_num=$1
    local total_batches=$2
    local test_args="-e2e.batch-total=$total_batches -e2e.batch-number=$batch_num -e2e.failure-probability=$FAILURE_PROB"

    if [ "$VERBOSE" = true ]; then
        test_args="$test_args -test.v"
    fi

    echo "Running batch $batch_num of $total_batches (failure probability: $FAILURE_PROB)"
    cd "$(dirname "$0")" && go test -tags e2e_batch -timeout 30m . -args $test_args
}

# Main execution
for ((r=1; r<=REPEAT_COUNT; r++)); do
    if [ "$REPEAT_COUNT" -gt 1 ]; then
        echo "=== Starting test run $r of $REPEAT_COUNT ==="
    fi

    # Run all batches in parallel
    pids=()
    for ((b=1; b<=BATCH_COUNT; b++)); do
        run_batch $b $BATCH_COUNT &
        pids+=($!)
    done

    # Wait for all batches to complete
    for pid in "${pids[@]}"; do
        wait $pid || exit 1
    done
done