#!/usr/bin/env python3
"""Mine HostedCluster bring-up and node-join timings out of e2e artifact dumps.

Every HyperShift e2e job already dumps the full resource tree via `hypershift dump cluster`
(`oc adm inspect` layout), so the timings we care about are recoverable after the fact from the
`creationTimestamp` and condition `lastTransitionTime` fields of resources that CI has been
collecting all along. Nothing has to be instrumented in the test code, and the same extraction
works for the v1 suite (`$ARTIFACT_DIR/<TestName>/namespaces/...`) and for the v2 suite
(`$ARTIFACT_DIR/<clusterName>/namespaces/...`), since both go through the same dump command.

Resources mined, and the metric each produces:

  HostedCluster    hypershift.openshift.io/hostedclusters   control_plane_bring_up
                                                            cluster_version_rollout
  NodePool         hypershift.openshift.io/nodepools        nodepool_node_join
  CAPI Machine     cluster.x-k8s.io/machines                machine_provision

Usage:

  # Human readable report for one job's artifacts, in the style of a dump walkthrough
  ./hack/perf/hcp-timings.py extract $ARTIFACT_DIR

  # Same, but also append machine readable records for later aggregation
  ./hack/perf/hcp-timings.py extract $ARTIFACT_DIR --json records/run-1234.json

  # Aggregate many runs into P50/P95/P99 distributions, grouped by platform
  ./hack/perf/hcp-timings.py compare 'records/*.json'

  # Capture a baseline, then gate later runs against it
  ./hack/perf/hcp-timings.py compare 'records/*.json' --write-baseline hack/perf/baselines/aws.json
  ./hack/perf/hcp-timings.py compare 'records/new-*.json' \\
      --baseline hack/perf/baselines/aws.json --threshold 20

Caveats, which matter when reading the numbers:

  * A condition's lastTransitionTime records the *last* transition, not the first. If a condition
    flapped (Available going True -> False -> True during a test that breaks things on purpose),
    the reported duration is an upper bound. Records carry "flapReason"/"observedGeneration" hints
    where the API provides them, and machine level metrics are the more reliable signal.
  * Dumps are taken once, at the end of a test, so resources deleted before the dump (for example
    machines replaced during an upgrade) are not represented.
  * Deletion timings cannot be recovered this way at all: the objects are gone by the time the
    dump runs.

Exit codes: 0 = success / no regression, 1 = regression detected, 2 = usage or input error.
"""

from __future__ import annotations

import argparse
import glob
import json
import os
import sys
from datetime import datetime, timezone
from pathlib import Path

try:
    import yaml
except ImportError:
    print("ERROR: PyYAML is required but not installed.")
    print("Install with: pip install pyyaml")
    sys.exit(2)

SCHEMA_VERSION = 1
PERCENTILES = (50, 95, 99)
DEFAULT_THRESHOLD_PERCENT = 20.0
DEFAULT_COMPARE_STAT = "p50"

EXIT_OK = 0
EXIT_REGRESSION = 1
EXIT_ERROR = 2

METRIC_BRING_UP = "control_plane_bring_up"
METRIC_VERSION_ROLLOUT = "cluster_version_rollout"
METRIC_NODE_JOIN = "nodepool_node_join"
METRIC_MACHINE_PROVISION = "machine_provision"

# Milestones reported as phases of control plane bring-up, in the order they are expected.
BRING_UP_PHASES = (
    "InfrastructureReady",
    "EtcdAvailable",
    "KubeAPIServerAvailable",
    "Available",
)

# Milestones reported as phases of a NodePool reaching its desired node count.
NODE_JOIN_PHASES = (
    "ValidMachineTemplate",
    "ReachedIgnitionEndpoint",
    "AllMachinesReady",
    "AllNodesHealthy",
    "Ready",
)

# Milestones reported as phases of an individual CAPI Machine. InfrastructureReady is when the
# cloud provider finished creating the instance, NodeHealthy is when the node joined and became
# healthy, so the gap between them separates "the cloud is slow" from "bootstrap is slow".
MACHINE_PHASES = (
    "BootstrapReady",
    "InfrastructureReady",
    "NodeHealthy",
    "Ready",
)

# CAPI infrastructure references, used to recover the platform of a Machine when its HostedCluster
# was not part of the same dump.
INFRA_KIND_PLATFORMS = {
    "AWSMachine": "AWS",
    "AzureMachine": "Azure",
    "KubevirtMachine": "KubeVirt",
    "OpenStackMachine": "OpenStack",
    "AgentMachine": "Agent",
    "IBMVPCMachine": "IBMCloud",
    "IBMPowerVSMachine": "PowerVS",
}


# ---------------------------------------------------------------------------------------------
# Artifact parsing
# ---------------------------------------------------------------------------------------------


def parse_timestamp(value):
    """Normalize a Kubernetes timestamp to an aware UTC datetime.

    PyYAML already turns unquoted RFC 3339 timestamps into datetime objects, but quoted ones stay
    strings, and naive datetimes have to be treated as UTC the way the API server means them.
    """
    if value is None:
        return None
    if isinstance(value, datetime):
        parsed = value
    else:
        text = str(value).strip()
        if not text:
            return None
        try:
            parsed = datetime.fromisoformat(text.replace("Z", "+00:00"))
        except ValueError:
            return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(timezone.utc)


def true_condition_times(obj):
    """Map condition type to the time it last transitioned, for conditions that are currently True.

    Conditions that are not True are deliberately dropped rather than reported as zero: a missing
    milestone must not look like an instant one.
    """
    conditions = (obj.get("status") or {}).get("conditions") or []
    times = {}
    for condition in conditions:
        if not isinstance(condition, dict):
            continue
        if str(condition.get("status")) != "True":
            continue
        transition = parse_timestamp(condition.get("lastTransitionTime"))
        if transition is None:
            continue
        times[condition.get("type")] = transition
    return times


def elapsed_phases(start, condition_times, phase_names):
    """Seconds from `start` to each named milestone, skipping milestones that were not reached."""
    phases = {}
    for name in phase_names:
        transition = condition_times.get(name)
        if transition is None:
            continue
        phases[name] = max((transition - start).total_seconds(), 0.0)
    return phases


def iter_dumped_resources(root, group, plural):
    """Yield (path, object) for every dumped resource of a kind under an `oc adm inspect` tree.

    Both namespaced (`namespaces/<ns>/<group>/<plural>/<name>.yaml`) and cluster scoped
    (`cluster-scoped-resources/<group>/<plural>/<name>.yaml`) layouts are searched, anywhere in the
    tree, so a whole job's artifact directory containing many per-test dumps can be passed at once.
    """
    for path in sorted(Path(root).rglob(f"{group}/{plural}/*.yaml")):
        try:
            with open(path, "r", encoding="utf-8") as handle:
                doc = yaml.safe_load(handle)
        except (OSError, yaml.YAMLError) as err:
            print(f"WARNING: skipping {path}: {err}", file=sys.stderr)
            continue
        if not isinstance(doc, dict):
            continue
        # `oc adm inspect` writes one object per file, but tolerate List documents too.
        if doc.get("kind", "").endswith("List") and isinstance(doc.get("items"), list):
            for item in doc["items"]:
                if isinstance(item, dict):
                    yield path, item
            continue
        yield path, doc


def prow_metadata(root):
    """Recover Prow job identity by walking up from the artifact dir looking for prowjob.json."""
    metadata = {}
    for env_key, name in (("JOB_NAME", "jobName"), ("BUILD_ID", "buildID")):
        value = os.getenv(env_key)
        if value:
            metadata[name] = value
    if "jobName" in metadata and "buildID" in metadata:
        return metadata

    current = Path(root).resolve()
    for candidate in [current, *current.parents][:8]:
        prowjob = candidate / "prowjob.json"
        if not prowjob.is_file():
            continue
        try:
            with open(prowjob, "r", encoding="utf-8") as handle:
                doc = json.load(handle)
        except (OSError, ValueError):
            break
        metadata.setdefault("jobName", (doc.get("spec") or {}).get("job", ""))
        metadata.setdefault("buildID", (doc.get("status") or {}).get("build_id", ""))
        break
    return {key: value for key, value in metadata.items() if value}


def record(metric, platform, namespace, name, created, duration, phases=None, metadata=None):
    return {
        "schemaVersion": SCHEMA_VERSION,
        "metric": metric,
        "platform": platform or "unknown",
        "namespace": namespace,
        "name": name,
        "createdAt": created.isoformat().replace("+00:00", "Z") if created else None,
        "durationSeconds": duration,
        "phases": phases or {},
        "metadata": {key: value for key, value in (metadata or {}).items() if value},
    }


def extract_hosted_clusters(root):
    """Extract bring-up and version rollout records, plus an index used to resolve platforms."""
    records = []
    index = {}
    for _, doc in iter_dumped_resources(root, "hypershift.openshift.io", "hostedclusters"):
        meta = doc.get("metadata") or {}
        namespace, name = meta.get("namespace"), meta.get("name")
        created = parse_timestamp(meta.get("creationTimestamp"))
        platform = ((doc.get("spec") or {}).get("platform") or {}).get("type") or "unknown"
        release = ((doc.get("spec") or {}).get("release") or {}).get("image", "")
        if not namespace or not name:
            continue

        index[(namespace, name)] = platform
        # Control plane resources live in a namespace derived from the HostedCluster coordinates.
        index[f"{namespace}-{name}"] = platform

        if created is None:
            print(f"WARNING: HostedCluster {namespace}/{name} has no creationTimestamp", file=sys.stderr)
            continue

        conditions = true_condition_times(doc)
        phases = elapsed_phases(created, conditions, BRING_UP_PHASES)
        records.append(
            record(
                METRIC_BRING_UP,
                platform,
                namespace,
                name,
                created,
                phases.get("Available"),
                phases,
                {"releaseImage": release},
            )
        )

        for entry in ((doc.get("status") or {}).get("version") or {}).get("history") or []:
            if not isinstance(entry, dict) or entry.get("state") != "Completed":
                continue
            started = parse_timestamp(entry.get("startedTime"))
            completed = parse_timestamp(entry.get("completionTime"))
            if started is None or completed is None:
                continue
            records.append(
                record(
                    METRIC_VERSION_ROLLOUT,
                    platform,
                    namespace,
                    name,
                    started,
                    max((completed - started).total_seconds(), 0.0),
                    {},
                    {"version": entry.get("version", ""), "releaseImage": entry.get("image", "")},
                )
            )
    return records, index


def extract_node_pools(root, platform_index):
    records = []
    for _, doc in iter_dumped_resources(root, "hypershift.openshift.io", "nodepools"):
        meta = doc.get("metadata") or {}
        spec = doc.get("spec") or {}
        namespace, name = meta.get("namespace"), meta.get("name")
        created = parse_timestamp(meta.get("creationTimestamp"))
        if not namespace or not name or created is None:
            continue

        cluster = spec.get("clusterName", "")
        platform_spec = spec.get("platform") or {}
        platform = platform_spec.get("type") or platform_index.get((namespace, cluster), "unknown")
        conditions = true_condition_times(doc)
        phases = elapsed_phases(created, conditions, NODE_JOIN_PHASES)
        status = doc.get("status") or {}

        records.append(
            record(
                METRIC_NODE_JOIN,
                platform,
                namespace,
                name,
                created,
                phases.get("Ready"),
                phases,
                {
                    "cluster": cluster,
                    "nodeCount": str(spec.get("replicas", "")),
                    "readyReplicas": str(status.get("replicas", "")),
                    "instanceType": node_pool_instance_type(platform_spec),
                    "releaseImage": (spec.get("release") or {}).get("image", ""),
                },
            )
        )
    return records


def node_pool_instance_type(platform_spec):
    """Platform specific machine size, which has to match for two node-join numbers to compare."""
    platform = platform_spec.get("type")
    if platform == "AWS":
        return (platform_spec.get("aws") or {}).get("instanceType", "")
    if platform == "Azure":
        return (platform_spec.get("azure") or {}).get("vmSize", "")
    if platform == "OpenStack":
        return (platform_spec.get("openstack") or {}).get("flavor", "")
    if platform == "KubeVirt":
        compute = ((platform_spec.get("kubevirt") or {}).get("compute") or {})
        if compute:
            return f"{compute.get('cores', '')}cpu/{compute.get('memory', '')}"
    return ""


def extract_machines(root, platform_index):
    """One record per CAPI Machine, so percentiles across machines are meaningful on their own."""
    records = []
    for _, doc in iter_dumped_resources(root, "cluster.x-k8s.io", "machines"):
        meta = doc.get("metadata") or {}
        spec = doc.get("spec") or {}
        namespace, name = meta.get("namespace"), meta.get("name")
        created = parse_timestamp(meta.get("creationTimestamp"))
        if not namespace or not name or created is None:
            continue

        infra_kind = (spec.get("infrastructureRef") or {}).get("kind", "")
        platform = platform_index.get(namespace) or INFRA_KIND_PLATFORMS.get(infra_kind, "unknown")
        conditions = true_condition_times(doc)
        phases = elapsed_phases(created, conditions, MACHINE_PHASES)
        status = doc.get("status") or {}
        labels = meta.get("labels") or {}

        records.append(
            record(
                METRIC_MACHINE_PROVISION,
                platform,
                namespace,
                name,
                created,
                phases.get("Ready"),
                phases,
                {
                    "cluster": spec.get("clusterName", ""),
                    "nodePool": labels.get("hypershift.openshift.io/nodePool", ""),
                    "node": (status.get("nodeRef") or {}).get("name", ""),
                    "phase": status.get("phase", ""),
                    "infrastructureKind": infra_kind,
                },
            )
        )
    return records


def extract(root):
    """Extract every timing record from one artifact tree."""
    hosted_clusters, platform_index = extract_hosted_clusters(root)
    records = hosted_clusters
    records += extract_node_pools(root, platform_index)
    records += extract_machines(root, platform_index)

    source = str(root)
    metadata = prow_metadata(root)
    for item in records:
        item["metadata"].setdefault("source", source)
        for key, value in metadata.items():
            item["metadata"].setdefault(key, value)
    return records


# ---------------------------------------------------------------------------------------------
# Reporting
# ---------------------------------------------------------------------------------------------


def format_duration(seconds):
    if seconds is None:
        return "     n/a"
    return f"{seconds / 60:5.1f}m ({seconds:.0f}s)"


def print_phases(phases, known_phases):
    """Print milestones in the order they were actually reached, then the ones that never were.

    Conditions do not always transition in their documented order (a NodePool can report
    AllMachinesReady before ReachedIgnitionEndpoint), and printing them out of order makes the
    timeline hard to read.
    """
    for name, elapsed in sorted(phases.items(), key=lambda item: item[1]):
        print(f"  {name:<30}: +{format_duration(elapsed)}")
    for name in known_phases:
        if name not in phases:
            print(f"  {name:<30}: not reached")


def print_report(records):
    """Print a per-cluster walkthrough of the timings recovered from a dump."""
    bring_ups = [r for r in records if r["metric"] == METRIC_BRING_UP]
    if not bring_ups:
        print("No HostedCluster dumps found.")

    for bring_up in bring_ups:
        namespace, name = bring_up["namespace"], bring_up["name"]
        print("=" * 70)
        print(f"HostedCluster: {namespace}/{name}")
        print("=" * 70)
        print(f"Created: {bring_up['createdAt']}")
        print(f"Platform: {bring_up['platform']}")
        print("")
        print("Control Plane Bring-Up Phases:")
        print_phases(bring_up["phases"], BRING_UP_PHASES)
        print(f"  {'Total':<30}:  {format_duration(bring_up['durationSeconds'])}")

        rollouts = [
            r
            for r in records
            if r["metric"] == METRIC_VERSION_ROLLOUT
            and (r["namespace"], r["name"]) == (namespace, name)
        ]
        if rollouts:
            print("")
            print("Cluster Version Rollout:")
            for rollout in rollouts:
                version = rollout["metadata"].get("version", "unknown")
                print(f"  {version:<30}:  {format_duration(rollout['durationSeconds'])}")

        for node_pool in [
            r
            for r in records
            if r["metric"] == METRIC_NODE_JOIN
            and r["namespace"] == namespace
            and r["metadata"].get("cluster") == name
        ]:
            scale = node_pool["metadata"].get("nodeCount", "?")
            instance = node_pool["metadata"].get("instanceType", "")
            suffix = f", {instance}" if instance else ""
            print("")
            print(f"NodePool: {node_pool['name']} ({scale} replicas{suffix})")
            print_phases(node_pool["phases"], NODE_JOIN_PHASES)

        machines = [
            r
            for r in records
            if r["metric"] == METRIC_MACHINE_PROVISION
            and r["namespace"] == f"{namespace}-{name}"
        ]
        if machines:
            print("")
            print(f"Machines ({len(machines)}):")
            for phase in MACHINE_PHASES:
                values = sorted(m["phases"][phase] for m in machines if phase in m["phases"])
                if not values:
                    continue
                print(
                    f"  {phase:<30}: p50 {percentile(values, 50) / 60:5.1f}m  "
                    f"min {values[0] / 60:5.1f}m  max {values[-1] / 60:5.1f}m"
                )
        print("")


# ---------------------------------------------------------------------------------------------
# Aggregation and regression detection
# ---------------------------------------------------------------------------------------------


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
    ordered = sorted(float(value) for value in values)
    summary = {
        "sampleCount": len(ordered),
        "min": ordered[0],
        "max": ordered[-1],
        "mean": sum(ordered) / len(ordered),
    }
    for pct in PERCENTILES:
        summary[f"p{pct}"] = percentile(ordered, pct)
    return summary


def load_records(paths):
    """Read records from files, directories or glob patterns, in JSON Lines or JSON array form."""
    records = []
    for path in paths:
        matches = sorted(glob.glob(path)) if any(c in path for c in "*?[") else [path]
        if not matches:
            raise SystemExit(f"no input matched {path}")
        for match in matches:
            files = (
                sorted(str(p) for p in Path(match).rglob("*.json"))
                if os.path.isdir(match)
                else [match]
            )
            for file_path in files:
                with open(file_path, "r", encoding="utf-8") as handle:
                    content = handle.read()
                stripped = content.strip()
                if stripped.startswith("["):
                    records.extend(json.loads(stripped))
                    continue
                for line in stripped.splitlines():
                    line = line.strip()
                    if not line.startswith("{"):
                        continue
                    try:
                        records.append(json.loads(line))
                    except json.JSONDecodeError:
                        continue
    return records


def group_records(records, platform_filter, metric_filter):
    grouped = {}
    for item in records:
        if not isinstance(item, dict) or "metric" not in item:
            continue
        platform = item.get("platform") or "unknown"
        metric = item["metric"]
        if platform_filter and platform.lower() != platform_filter.lower():
            continue
        if metric_filter and metric != metric_filter:
            continue
        duration = item.get("durationSeconds")
        if duration is None:
            continue
        entry = grouped.setdefault(platform, {}).setdefault(metric, {"durations": [], "phases": {}})
        entry["durations"].append(float(duration))
        for phase, elapsed in (item.get("phases") or {}).items():
            entry["phases"].setdefault(phase, []).append(float(elapsed))
    return grouped


def summarize(grouped):
    summary = {}
    for platform, metrics in sorted(grouped.items()):
        summary[platform] = {}
        for metric, samples in sorted(metrics.items()):
            metric_summary = distribution(samples["durations"])
            phases = {
                phase: distribution(values) for phase, values in sorted(samples["phases"].items())
            }
            if phases:
                metric_summary["phases"] = phases
            summary[platform][metric] = metric_summary
    return summary


def print_summary(summary):
    for platform, metrics in summary.items():
        print(f"platform: {platform}")
        for metric, stats in metrics.items():
            print(
                f"  {metric}: n={stats['sampleCount']} p50={stats['p50']:.1f}s "
                f"p95={stats['p95']:.1f}s p99={stats['p99']:.1f}s "
                f"(min={stats['min']:.1f}s max={stats['max']:.1f}s)"
            )
            for phase, phase_stats in stats.get("phases", {}).items():
                print(
                    f"    {phase}: p50={phase_stats['p50']:.1f}s "
                    f"p95={phase_stats['p95']:.1f}s p99={phase_stats['p99']:.1f}s"
                )


def load_baseline(path):
    with open(path, "r", encoding="utf-8") as handle:
        baseline = json.load(handle)
    if baseline.get("schemaVersion") != SCHEMA_VERSION:
        raise SystemExit(
            f"baseline {path} has schemaVersion {baseline.get('schemaVersion')}, "
            f"expected {SCHEMA_VERSION}"
        )
    if "baselines" not in baseline:
        raise SystemExit(f"baseline {path} is missing the 'baselines' key")
    return baseline


def compare_to_baseline(summary, baseline, threshold_percent, compare_stat):
    """Compare a summary against a baseline; returns (all comparisons, regressions)."""
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
                        "sampleCount": stats["sampleCount"],
                    }
                )
                continue
            targets = [(None, stats, metric_baseline)]
            for phase, phase_stats in stats.get("phases", {}).items():
                phase_baseline = (metric_baseline.get("phases") or {}).get(phase)
                if phase_baseline is not None:
                    targets.append((phase, phase_stats, phase_baseline))
            for phase, current_stats, expected_stats in targets:
                current = current_stats.get(compare_stat)
                expected = expected_stats.get(compare_stat)
                if current is None or expected is None or expected <= 0:
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
            label = f"{label}/{comparison['phase']}"
        if comparison["status"] == "no-baseline":
            print(f"NO BASELINE {comparison['platform']} {label}: current={comparison['current']:.1f}s")
            continue
        status = "REGRESSION" if comparison["status"] == "regression" else "OK        "
        print(
            f"{status} {comparison['platform']} {label}: {comparison['stat']} "
            f"current={comparison['current']:.1f}s baseline={comparison['baseline']:.1f}s "
            f"delta={comparison['deltaPercent']:+.1f}% (threshold {threshold_percent:+.1f}%)"
        )


def write_json(path, payload):
    directory = os.path.dirname(os.path.abspath(path))
    if directory:
        os.makedirs(directory, exist_ok=True)
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(payload, handle, indent=2, sort_keys=True)
        handle.write("\n")


# ---------------------------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------------------------


def run_extract(args):
    records = []
    for root in args.artifact_dir:
        if not os.path.isdir(root):
            print(f"not a directory: {root}", file=sys.stderr)
            return EXIT_ERROR
        records.extend(extract(root))

    if args.platform:
        records = [r for r in records if r["platform"].lower() == args.platform.lower()]

    if not records:
        print("no timing data recovered from the given artifacts", file=sys.stderr)
        return EXIT_ERROR

    if args.format in ("text", "both"):
        print_report(records)
    if args.format in ("json", "both") and not args.json:
        for item in records:
            print(json.dumps(item, sort_keys=True))
    if args.json:
        directory = os.path.dirname(os.path.abspath(args.json))
        if directory:
            os.makedirs(directory, exist_ok=True)
        with open(args.json, "a" if args.append else "w", encoding="utf-8") as handle:
            for item in records:
                handle.write(json.dumps(item, sort_keys=True) + "\n")
        print(f"wrote {len(records)} timing records to {args.json}")
    return EXIT_OK


def run_compare(args):
    try:
        records = load_records(args.records)
    except OSError as err:
        print(f"failed to read timing records: {err}", file=sys.stderr)
        return EXIT_ERROR

    grouped = group_records(records, args.platform, args.metric)
    if not grouped:
        print(f"no timing records found in {', '.join(args.records)}", file=sys.stderr)
        return EXIT_ERROR

    summary = summarize(grouped)
    print_summary(summary)

    if args.write_baseline:
        write_json(
            args.write_baseline,
            {
                "schemaVersion": SCHEMA_VERSION,
                "generatedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
                "thresholdPercent": args.threshold,
                "baselines": summary,
            },
        )
        print(f"wrote baseline to {args.write_baseline}")

    if not args.baseline:
        return EXIT_OK

    baseline = load_baseline(args.baseline)
    comparisons, regressions = compare_to_baseline(
        summary, baseline, args.threshold, args.compare_stat
    )
    print("")
    print_comparisons(comparisons, args.threshold)

    if args.report:
        write_json(
            args.report,
            {
                "schemaVersion": SCHEMA_VERSION,
                "generatedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
                "thresholdPercent": args.threshold,
                "compareStat": args.compare_stat,
                "summary": summary,
                "comparisons": comparisons,
                "regressions": regressions,
            },
        )
        print(f"wrote report to {args.report}")

    for comparison in comparisons:
        count = comparison.get("sampleCount")
        if count is not None and count < args.min_samples:
            print(
                f"skipping {comparison['platform']} {comparison['metric']}: "
                f"only {count} samples, {args.min_samples} required"
            )
    regressions = [
        regression
        for regression in regressions
        if regression.get("sampleCount") is None or regression["sampleCount"] >= args.min_samples
    ]

    missing = [c for c in comparisons if c["status"] == "no-baseline"]
    if missing and args.fail_on_missing_baseline:
        print(
            f"{len(missing)} metric(s) have no baseline entry and --fail-on-missing-baseline is set",
            file=sys.stderr,
        )
        return EXIT_REGRESSION

    if regressions:
        print(
            f"{len(regressions)} metric(s) regressed more than {args.threshold:.1f}% "
            f"against {args.baseline}",
            file=sys.stderr,
        )
        return EXIT_REGRESSION

    print(f"no regressions beyond {args.threshold:.1f}% against {args.baseline}")
    return EXIT_OK


def parse_args(argv):
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    extract_parser = subparsers.add_parser(
        "extract", help="recover timing records from dumped e2e artifacts"
    )
    extract_parser.add_argument(
        "artifact_dir", nargs="+", help="artifact directory containing dumped resource YAML"
    )
    extract_parser.add_argument(
        "--format",
        choices=["text", "json", "both"],
        default="text",
        help="report format written to stdout (default: text)",
    )
    extract_parser.add_argument("--json", default="", metavar="PATH", help="write records to PATH")
    extract_parser.add_argument(
        "--append", action="store_true", help="append to --json instead of overwriting it"
    )
    extract_parser.add_argument(
        "--platform",
        default=os.getenv("HYPERSHIFT_PERF_PLATFORM", ""),
        help="only report this platform, e.g. AWS, Azure, KubeVirt "
        "(env: HYPERSHIFT_PERF_PLATFORM)",
    )
    extract_parser.set_defaults(func=run_extract)

    compare_parser = subparsers.add_parser(
        "compare", help="aggregate records into percentiles and check them against a baseline"
    )
    compare_parser.add_argument(
        "records", nargs="+", help="record files, directories or globs produced by extract"
    )
    compare_parser.add_argument(
        "--platform",
        default=os.getenv("HYPERSHIFT_PERF_PLATFORM", ""),
        help="only consider this platform (env: HYPERSHIFT_PERF_PLATFORM)",
    )
    compare_parser.add_argument("--metric", default="", help="only consider this metric")
    compare_parser.add_argument("--baseline", default="", help="baseline file to compare against")
    compare_parser.add_argument(
        "--write-baseline", default="", metavar="PATH", help="write the distributions to PATH"
    )
    compare_parser.add_argument(
        "--threshold",
        type=float,
        default=float(os.getenv("HYPERSHIFT_PERF_THRESHOLD", DEFAULT_THRESHOLD_PERCENT)),
        help=f"percent regression tolerated before failing (default: {DEFAULT_THRESHOLD_PERCENT:.0f})",
    )
    compare_parser.add_argument(
        "--compare-stat",
        default=DEFAULT_COMPARE_STAT,
        choices=[f"p{pct}" for pct in PERCENTILES] + ["mean"],
        help=f"statistic compared against the baseline (default: {DEFAULT_COMPARE_STAT})",
    )
    compare_parser.add_argument(
        "--min-samples",
        type=int,
        default=1,
        help="minimum records required per metric before a regression counts (default: 1)",
    )
    compare_parser.add_argument(
        "--report", default="", metavar="PATH", help="write a JSON summary and comparison report"
    )
    compare_parser.add_argument(
        "--fail-on-missing-baseline",
        action="store_true",
        help="treat metrics without a baseline entry as a failure",
    )
    compare_parser.set_defaults(func=run_compare)

    return parser.parse_args(argv)


def main(argv):
    args = parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
