#!/usr/bin/env python3
"""Track HostedCluster bring-up and NodePool node-join performance baselines.

The e2e suite emits one structured timing record per measurement (see
test/e2e/util/perftiming.go). Records land in $ARTIFACT_DIR/perf-timings/timings.json as JSON
Lines, and are also echoed to the test log prefixed with "PERF_TIMING ".

This script aggregates those records into P50/P95/P99 distributions grouped by platform and
metric, compares them against a committed baseline, and exits non-zero when a metric regresses
beyond the allowed threshold (20% by default).

Usage:

  # Summarize a run
  ./hack/track-perf-baselines.py --input $ARTIFACT_DIR/perf-timings/timings.json

  # Capture the v1 baseline for a platform
  ./hack/track-perf-baselines.py --input timings.json --platform AWS \\
      --write-baseline hack/perf-baselines/aws.json

  # Gate CI on regressions against the committed baseline
  ./hack/track-perf-baselines.py --input $ARTIFACT_DIR/perf-timings/timings.json \\
      --baseline hack/perf-baselines/aws.json --threshold 20

The platform can also be supplied via the HYPERSHIFT_PERF_PLATFORM environment variable, and the
input via HYPERSHIFT_PERF_TIMING_DIR (the directory the e2e suite writes to).

Baseline file format:

  {
    "schemaVersion": 1,
    "generatedAt": "2026-01-01T00:00:00Z",
    "baselines": {
      "AWS": {
        "hosted_control_plane_bring_up": {
          "sampleCount": 25,
          "p50": 610.2, "p95": 812.5, "p99": 905.0,
          "subPhases": {"EtcdAvailable": {"p50": 120.0, "p95": 150.0, "p99": 180.0}}
        }
      }
    }
  }

Exit codes: 0 = no regression, 1 = regression detected, 2 = usage or input error.
"""

import argparse
import glob
import json
import os
import sys
from datetime import datetime, timezone

SCHEMA_VERSION = 1
LOG_PREFIX = "PERF_TIMING "
PERCENTILES = (50, 95, 99)
DEFAULT_THRESHOLD_PERCENT = 20.0
# Statistic compared against the baseline. P50 is the default because it is stable at the sample
# counts a single CI run produces; tail percentiles need many more runs to be meaningful.
DEFAULT_COMPARE_STAT = "p50"

EXIT_OK = 0
EXIT_REGRESSION = 1
EXIT_ERROR = 2


def percentile(sorted_values, pct):
    """Linear-interpolated percentile, matching the numpy default method."""
    if not sorted_values:
        raise ValueError("percentile of empty sample")
    if len(sorted_values) == 1:
        return float(sorted_values[0])
    rank = (len(sorted_values) - 1) * (pct / 100.0)
    low = int(rank)
    high = min(low + 1, len(sorted_values) - 1)
    weight = rank - low
    return float(sorted_values[low] * (1.0 - weight) + sorted_values[high] * weight)


def distribution(values):
    """Summarize a list of durations into the percentiles we track."""
    ordered = sorted(float(value) for value in values)
    summary = {
        "sampleCount": len(ordered),
        "min": ordered[0],
        "max": ordered[-1],
        "mean": sum(ordered) / len(ordered),
    }
    for pct in PERCENTILES:
        summary["p{}".format(pct)] = percentile(ordered, pct)
    return summary


def iter_record_lines(text):
    """Yield JSON payloads from JSON Lines content, a JSON array, or raw test log output."""
    stripped = text.strip()
    if stripped.startswith("["):
        for record in json.loads(stripped):
            yield record
        return
    for line in text.splitlines():
        line = line.strip()
        if not line:
            continue
        if LOG_PREFIX in line:
            # Test logs prefix each record with a timestamp and the marker.
            line = line[line.index(LOG_PREFIX) + len(LOG_PREFIX):].strip()
        if not line.startswith("{"):
            continue
        try:
            yield json.loads(line)
        except json.JSONDecodeError:
            continue


def load_records(paths):
    """Read timing records from files, directories or glob patterns."""
    records = []
    for path in paths:
        matches = sorted(glob.glob(path)) if any(c in path for c in "*?[") else [path]
        if not matches:
            raise SystemExit("no input matched {}".format(path))
        for match in matches:
            if os.path.isdir(match):
                files = sorted(glob.glob(os.path.join(match, "**", "*.json"), recursive=True))
                files += sorted(glob.glob(os.path.join(match, "**", "*.jsonl"), recursive=True))
            else:
                files = [match]
            for file_path in files:
                with open(file_path, "r", encoding="utf-8") as handle:
                    records.extend(iter_record_lines(handle.read()))
    return records


def group_records(records, platform_filter, metric_filter):
    """Group durations by platform then metric, keeping sub-phase samples alongside."""
    grouped = {}
    for record in records:
        if not isinstance(record, dict) or "metric" not in record:
            continue
        platform = record.get("platform") or "unknown"
        metric = record["metric"]
        if platform_filter and platform.lower() != platform_filter.lower():
            continue
        if metric_filter and metric != metric_filter:
            continue
        duration = record.get("durationSeconds")
        if duration is None:
            continue
        entry = grouped.setdefault(platform, {}).setdefault(metric, {"durations": [], "subPhases": {}})
        entry["durations"].append(float(duration))
        for phase, elapsed in (record.get("subPhases") or {}).items():
            entry["subPhases"].setdefault(phase, []).append(float(elapsed))
    return grouped


def summarize(grouped):
    """Turn grouped samples into the nested distribution structure used by baseline files."""
    summary = {}
    for platform, metrics in sorted(grouped.items()):
        summary[platform] = {}
        for metric, samples in sorted(metrics.items()):
            metric_summary = distribution(samples["durations"])
            sub_phases = {
                phase: distribution(values)
                for phase, values in sorted(samples["subPhases"].items())
            }
            if sub_phases:
                metric_summary["subPhases"] = sub_phases
            summary[platform][metric] = metric_summary
    return summary


def print_summary(summary):
    for platform, metrics in summary.items():
        print("platform: {}".format(platform))
        for metric, stats in metrics.items():
            print(
                "  {metric}: n={n} p50={p50:.1f}s p95={p95:.1f}s p99={p99:.1f}s "
                "(min={min:.1f}s max={max:.1f}s)".format(
                    metric=metric,
                    n=stats["sampleCount"],
                    p50=stats["p50"],
                    p95=stats["p95"],
                    p99=stats["p99"],
                    min=stats["min"],
                    max=stats["max"],
                )
            )
            for phase, phase_stats in stats.get("subPhases", {}).items():
                print(
                    "    {phase}: p50={p50:.1f}s p95={p95:.1f}s p99={p99:.1f}s".format(
                        phase=phase,
                        p50=phase_stats["p50"],
                        p95=phase_stats["p95"],
                        p99=phase_stats["p99"],
                    )
                )


def load_baseline(path):
    with open(path, "r", encoding="utf-8") as handle:
        baseline = json.load(handle)
    version = baseline.get("schemaVersion")
    if version != SCHEMA_VERSION:
        raise SystemExit(
            "baseline {} has schemaVersion {}, expected {}".format(path, version, SCHEMA_VERSION)
        )
    if "baselines" not in baseline:
        raise SystemExit("baseline {} is missing the 'baselines' key".format(path))
    return baseline


def compare(summary, baseline, threshold_percent, compare_stat):
    """Compare a run summary against a baseline, returning (comparisons, regressions)."""
    comparisons = []
    regressions = []
    baselines = baseline.get("baselines", {})
    for platform, metrics in summary.items():
        platform_baseline = baselines.get(platform, {})
        for metric, stats in metrics.items():
            metric_baseline = platform_baseline.get(metric)
            if metric_baseline is None:
                comparisons.append(
                    {
                        "platform": platform,
                        "metric": metric,
                        "phase": None,
                        "status": "no-baseline",
                        "current": stats[compare_stat],
                    }
                )
                continue
            targets = [(None, stats, metric_baseline)]
            for phase, phase_stats in stats.get("subPhases", {}).items():
                phase_baseline = (metric_baseline.get("subPhases") or {}).get(phase)
                if phase_baseline is not None:
                    targets.append((phase, phase_stats, phase_baseline))
            for phase, current_stats, expected_stats in targets:
                current = current_stats.get(compare_stat)
                expected = expected_stats.get(compare_stat)
                if current is None or expected is None:
                    continue
                if expected <= 0:
                    continue
                delta_percent = ((current - expected) / expected) * 100.0
                regressed = delta_percent > threshold_percent
                comparison = {
                    "platform": platform,
                    "metric": metric,
                    "phase": phase,
                    "status": "regression" if regressed else "ok",
                    "stat": compare_stat,
                    "current": current,
                    "baseline": expected,
                    "deltaPercent": delta_percent,
                    "sampleCount": current_stats.get("sampleCount"),
                }
                comparisons.append(comparison)
                if regressed:
                    regressions.append(comparison)
    return comparisons, regressions


def print_comparisons(comparisons, threshold_percent):
    for comparison in comparisons:
        label = comparison["metric"]
        if comparison.get("phase"):
            label = "{}/{}".format(label, comparison["phase"])
        if comparison["status"] == "no-baseline":
            print(
                "NO BASELINE {platform} {label}: current={current:.1f}s".format(
                    platform=comparison["platform"], label=label, current=comparison["current"]
                )
            )
            continue
        print(
            "{status} {platform} {label}: {stat} current={current:.1f}s baseline={baseline:.1f}s "
            "delta={delta:+.1f}% (threshold {threshold:+.1f}%)".format(
                status="REGRESSION" if comparison["status"] == "regression" else "OK        ",
                platform=comparison["platform"],
                label=label,
                stat=comparison["stat"],
                current=comparison["current"],
                baseline=comparison["baseline"],
                delta=comparison["deltaPercent"],
                threshold=threshold_percent,
            )
        )


def default_inputs():
    timing_dir = os.getenv("HYPERSHIFT_PERF_TIMING_DIR")
    if timing_dir:
        return [timing_dir]
    artifact_dir = os.getenv("ARTIFACT_DIR")
    if artifact_dir:
        return [os.path.join(artifact_dir, "perf-timings")]
    return []


def parse_args(argv):
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument(
        "--input",
        action="append",
        default=[],
        metavar="PATH",
        help="timing records file, directory or glob; repeatable. Defaults to "
        "$HYPERSHIFT_PERF_TIMING_DIR or $ARTIFACT_DIR/perf-timings",
    )
    parser.add_argument(
        "--platform",
        default=os.getenv("HYPERSHIFT_PERF_PLATFORM", ""),
        help="only consider records for this platform, e.g. AWS, Azure, KubeVirt "
        "(env: HYPERSHIFT_PERF_PLATFORM)",
    )
    parser.add_argument("--metric", default="", help="only consider this metric")
    parser.add_argument("--baseline", default="", help="baseline file to compare against")
    parser.add_argument(
        "--write-baseline",
        default="",
        metavar="PATH",
        help="write the computed distributions to PATH as a new baseline",
    )
    parser.add_argument(
        "--threshold",
        type=float,
        default=float(os.getenv("HYPERSHIFT_PERF_THRESHOLD", DEFAULT_THRESHOLD_PERCENT)),
        help="percent regression tolerated before failing (default: {:.0f})".format(
            DEFAULT_THRESHOLD_PERCENT
        ),
    )
    parser.add_argument(
        "--compare-stat",
        default=DEFAULT_COMPARE_STAT,
        choices=["p{}".format(pct) for pct in PERCENTILES] + ["mean"],
        help="statistic compared against the baseline (default: {})".format(DEFAULT_COMPARE_STAT),
    )
    parser.add_argument(
        "--min-samples",
        type=int,
        default=1,
        help="minimum records required per metric before comparing (default: 1)",
    )
    parser.add_argument(
        "--report",
        default="",
        metavar="PATH",
        help="write the JSON summary and comparison report to PATH",
    )
    parser.add_argument(
        "--fail-on-missing-baseline",
        action="store_true",
        help="treat metrics without a baseline entry as a failure",
    )
    return parser.parse_args(argv)


def main(argv):
    args = parse_args(argv)

    inputs = args.input or default_inputs()
    if not inputs:
        print(
            "no input specified: pass --input or set HYPERSHIFT_PERF_TIMING_DIR / ARTIFACT_DIR",
            file=sys.stderr,
        )
        return EXIT_ERROR

    try:
        records = load_records(inputs)
    except OSError as err:
        print("failed to read timing records: {}".format(err), file=sys.stderr)
        return EXIT_ERROR

    grouped = group_records(records, args.platform, args.metric)
    if not grouped:
        print("no timing records found in {}".format(", ".join(inputs)), file=sys.stderr)
        return EXIT_ERROR

    summary = summarize(grouped)
    print_summary(summary)

    if args.write_baseline:
        baseline = {
            "schemaVersion": SCHEMA_VERSION,
            "generatedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "thresholdPercent": args.threshold,
            "baselines": summary,
        }
        directory = os.path.dirname(os.path.abspath(args.write_baseline))
        os.makedirs(directory, exist_ok=True)
        with open(args.write_baseline, "w", encoding="utf-8") as handle:
            json.dump(baseline, handle, indent=2, sort_keys=True)
            handle.write("\n")
        print("wrote baseline to {}".format(args.write_baseline))

    if not args.baseline:
        return EXIT_OK

    baseline = load_baseline(args.baseline)
    comparisons, regressions = compare(summary, baseline, args.threshold, args.compare_stat)
    print("")
    print_comparisons(comparisons, args.threshold)

    if args.report:
        directory = os.path.dirname(os.path.abspath(args.report))
        os.makedirs(directory, exist_ok=True)
        with open(args.report, "w", encoding="utf-8") as handle:
            json.dump(
                {
                    "schemaVersion": SCHEMA_VERSION,
                    "generatedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
                    "thresholdPercent": args.threshold,
                    "compareStat": args.compare_stat,
                    "summary": summary,
                    "comparisons": comparisons,
                    "regressions": regressions,
                },
                handle,
                indent=2,
                sort_keys=True,
            )
            handle.write("\n")
        print("wrote report to {}".format(args.report))

    underpowered = [
        comparison
        for comparison in comparisons
        if comparison.get("sampleCount") is not None
        and comparison["sampleCount"] < args.min_samples
    ]
    for comparison in underpowered:
        print(
            "skipping {} {}: only {} samples, {} required".format(
                comparison["platform"],
                comparison["metric"],
                comparison["sampleCount"],
                args.min_samples,
            )
        )
    regressions = [
        regression
        for regression in regressions
        if regression.get("sampleCount") is None or regression["sampleCount"] >= args.min_samples
    ]

    missing = [c for c in comparisons if c["status"] == "no-baseline"]
    if missing and args.fail_on_missing_baseline:
        print(
            "{} metric(s) have no baseline entry and --fail-on-missing-baseline is set".format(
                len(missing)
            ),
            file=sys.stderr,
        )
        return EXIT_REGRESSION

    if regressions:
        print(
            "{} metric(s) regressed more than {:.1f}% against {}".format(
                len(regressions), args.threshold, args.baseline
            ),
            file=sys.stderr,
        )
        return EXIT_REGRESSION

    print("no regressions beyond {:.1f}% against {}".format(args.threshold, args.baseline))
    return EXIT_OK


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
