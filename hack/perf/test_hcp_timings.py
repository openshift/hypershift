#!/usr/bin/env python3
"""Tests for the HyperShift e2e artifact timing extractor."""

from __future__ import annotations

import importlib.util
import io
import json
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from datetime import datetime, timedelta, timezone
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("hcp-timings.py")
SPEC = importlib.util.spec_from_file_location("hcp_timings", MODULE_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"Unable to load {MODULE_PATH}")
TIMINGS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(TIMINGS)

ARTIFACTS = Path(__file__).with_name("testdata") / "artifacts"
ARTIFACTS_CAPI_V1BETA2 = Path(__file__).with_name("testdata") / "artifacts-capi-v1beta2"


def records_by_metric(records, metric):
    return [record for record in records if record["metric"] == metric]


class TestParseTimestamp(unittest.TestCase):
    def test_normalizes_supported_timestamp_shapes_to_utc(self) -> None:
        expected = datetime(2026, 9, 11, 14, 50, 33, tzinfo=timezone.utc)
        cases = [
            "2026-09-11T14:50:33Z",
            "2026-09-11T14:50:33+00:00",
            "2026-09-11T16:50:33+02:00",
            # PyYAML parses unquoted RFC 3339 scalars into datetime objects directly.
            datetime(2026, 9, 11, 14, 50, 33),
            datetime(2026, 9, 11, 14, 50, 33, tzinfo=timezone.utc),
        ]
        for value in cases:
            with self.subTest(value=value):
                self.assertEqual(TIMINGS.parse_timestamp(value), expected)

    def test_returns_none_for_missing_or_unparseable_values(self) -> None:
        for value in [None, "", "   ", "not-a-timestamp", {}]:
            with self.subTest(value=value):
                self.assertIsNone(TIMINGS.parse_timestamp(value))


class TestTrueConditionTimes(unittest.TestCase):
    def test_keeps_only_conditions_that_are_currently_true(self) -> None:
        obj = {
            "status": {
                "conditions": [
                    {"type": "Available", "status": "True", "lastTransitionTime": "2026-09-11T15:00:00Z"},
                    {"type": "Degraded", "status": "False", "lastTransitionTime": "2026-09-11T15:00:00Z"},
                    {"type": "Progressing", "status": "Unknown", "lastTransitionTime": "2026-09-11T15:00:00Z"},
                    {"type": "NoTimestamp", "status": "True"},
                ]
            }
        }
        self.assertEqual(list(TIMINGS.true_condition_times(obj)), ["Available"])

    def test_tolerates_objects_without_status_or_conditions(self) -> None:
        for obj in [{}, {"status": None}, {"status": {"conditions": None}}]:
            with self.subTest(obj=obj):
                self.assertEqual(TIMINGS.true_condition_times(obj), {})

    def test_renames_v1beta1_condition_types_to_their_v1beta2_names(self) -> None:
        obj = {
            "status": {
                "conditions": [
                    {
                        "type": "BootstrapReady",
                        "status": "True",
                        "lastTransitionTime": "2026-09-11T14:51:46Z",
                    }
                ]
            }
        }
        self.assertEqual(list(TIMINGS.true_condition_times(obj)), ["BootstrapConfigReady"])

    def test_reads_conditions_a_v1beta2_object_keeps_under_status_deprecated(self) -> None:
        obj = {
            "status": {
                "conditions": [
                    {
                        "type": "InfrastructureReady",
                        "status": "True",
                        "lastTransitionTime": "2026-09-11T14:54:58Z",
                    }
                ],
                "deprecated": {
                    "v1beta1": {
                        "conditions": [
                            {
                                "type": "BootstrapReady",
                                "status": "True",
                                "lastTransitionTime": "2026-09-11T14:51:46Z",
                            }
                        ]
                    }
                },
            }
        }
        self.assertEqual(
            sorted(TIMINGS.true_condition_times(obj)),
            ["BootstrapConfigReady", "InfrastructureReady"],
        )

    def test_current_conditions_win_over_the_deprecated_copy(self) -> None:
        obj = {
            "status": {
                "conditions": [
                    {
                        "type": "BootstrapConfigReady",
                        "status": "True",
                        "lastTransitionTime": "2026-09-11T14:51:46Z",
                    }
                ],
                "deprecated": {
                    "v1beta1": {
                        "conditions": [
                            {
                                "type": "BootstrapReady",
                                "status": "True",
                                "lastTransitionTime": "2026-09-11T20:00:00Z",
                            }
                        ]
                    }
                },
            }
        }
        self.assertEqual(
            TIMINGS.true_condition_times(obj)["BootstrapConfigReady"],
            datetime(2026, 9, 11, 14, 51, 46, tzinfo=timezone.utc),
        )


class TestElapsedPhases(unittest.TestCase):
    def test_reports_seconds_per_reached_milestone_and_skips_the_rest(self) -> None:
        start = datetime(2026, 9, 11, 14, 50, 33, tzinfo=timezone.utc)
        condition_times = {
            "EtcdAvailable": start + timedelta(seconds=77),
            "Available": start + timedelta(seconds=1911),
        }
        phases = TIMINGS.elapsed_phases(start, condition_times, TIMINGS.BRING_UP_PHASES)
        self.assertEqual(phases, {"EtcdAvailable": 77.0, "Available": 1911.0})

    def test_clamps_transitions_that_predate_creation(self) -> None:
        start = datetime(2026, 9, 11, 14, 50, 33, tzinfo=timezone.utc)
        condition_times = {"EtcdAvailable": start - timedelta(seconds=30)}
        phases = TIMINGS.elapsed_phases(start, condition_times, TIMINGS.BRING_UP_PHASES)
        self.assertEqual(phases, {"EtcdAvailable": 0.0})


class TestExtract(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.records = TIMINGS.extract(ARTIFACTS)

    def test_recovers_control_plane_bring_up_phases_from_the_hosted_cluster_dump(self) -> None:
        bring_ups = records_by_metric(self.records, TIMINGS.METRIC_BRING_UP)
        self.assertEqual(len(bring_ups), 1)
        bring_up = bring_ups[0]

        self.assertEqual(bring_up["platform"], "AWS")
        self.assertEqual(bring_up["namespace"], "e2e-clusters-f92vm")
        self.assertEqual(bring_up["name"], "create-cluster-km2vj")
        self.assertEqual(bring_up["durationSeconds"], 1911.0)
        self.assertEqual(
            bring_up["phases"],
            {
                "InfrastructureReady": 60.0,
                "EtcdAvailable": 77.0,
                "KubeAPIServerAvailable": 100.0,
                "Available": 1911.0,
            },
        )
        self.assertEqual(
            bring_up["metadata"]["releaseImage"],
            "quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64",
        )

    def test_recovers_cluster_version_rollout_from_the_version_history(self) -> None:
        rollouts = records_by_metric(self.records, TIMINGS.METRIC_VERSION_ROLLOUT)
        self.assertEqual(len(rollouts), 1)
        self.assertEqual(rollouts[0]["durationSeconds"], 1704.0)
        self.assertEqual(rollouts[0]["metadata"]["version"], "4.21.10")

    def test_recovers_node_join_timing_and_pool_shape_from_the_nodepool_dump(self) -> None:
        joins = records_by_metric(self.records, TIMINGS.METRIC_NODE_JOIN)
        self.assertEqual(len(joins), 1)
        join = joins[0]

        self.assertEqual(join["platform"], "AWS")
        self.assertEqual(join["durationSeconds"], 660.0)
        self.assertEqual(join["phases"]["AllMachinesReady"], 270.0)
        self.assertEqual(join["phases"]["AllNodesHealthy"], 648.0)
        self.assertEqual(join["metadata"]["cluster"], "create-cluster-km2vj")
        self.assertEqual(join["metadata"]["nodeCount"], "2")
        self.assertEqual(join["metadata"]["instanceType"], "m5.large")

    def test_emits_one_record_per_machine_so_percentiles_span_machines(self) -> None:
        machines = records_by_metric(self.records, TIMINGS.METRIC_MACHINE_PROVISION)
        self.assertEqual(len(machines), 2)
        for machine in machines:
            self.assertEqual(machine["platform"], "AWS")
            self.assertEqual(machine["metadata"]["nodePool"], "create-cluster-km2vj")
            self.assertEqual(machine["metadata"]["infrastructureKind"], "AWSMachine")
            self.assertIn("InfrastructureReady", machine["phases"])
            self.assertIn("NodeHealthy", machine["phases"])
        # Instance provisioning versus node join, which is the split that tells a slow cloud
        # apart from a slow bootstrap.
        self.assertEqual(sorted(m["phases"]["InfrastructureReady"] for m in machines), [194.0, 200.0])
        self.assertEqual(sorted(m["durationSeconds"] for m in machines), [548.0, 578.0])

    def test_tags_every_record_with_its_source_artifact_directory(self) -> None:
        for record in self.records:
            self.assertEqual(record["metadata"]["source"], str(ARTIFACTS))
            self.assertEqual(record["schemaVersion"], TIMINGS.SCHEMA_VERSION)

    def test_returns_nothing_for_an_artifact_tree_with_no_dumps(self) -> None:
        with tempfile.TemporaryDirectory() as empty:
            self.assertEqual(TIMINGS.extract(empty), [])


class TestExtractCAPIV1Beta2(unittest.TestCase):
    """cluster.x-k8s.io v1beta2 dumps must mine identically to the v1beta1 ones they replace."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.v1beta1 = records_by_metric(
            TIMINGS.extract(ARTIFACTS), TIMINGS.METRIC_MACHINE_PROVISION
        )
        cls.v1beta2 = records_by_metric(
            TIMINGS.extract(ARTIFACTS_CAPI_V1BETA2), TIMINGS.METRIC_MACHINE_PROVISION
        )

    def test_recovers_the_same_machine_timings_from_either_api_version(self) -> None:
        self.assertEqual(len(self.v1beta2), 2)
        for records in (self.v1beta1, self.v1beta2):
            self.assertEqual(
                sorted((r["name"], r["durationSeconds"]) for r in records),
                [
                    ("create-cluster-km2vj-7c9d4-abcde", 548.0),
                    ("create-cluster-km2vj-7c9d4-fghij", 578.0),
                ],
            )
        self.assertEqual(
            sorted((r["name"], sorted(r["phases"].items())) for r in self.v1beta1),
            sorted((r["name"], sorted(r["phases"].items())) for r in self.v1beta2),
        )

    def test_reports_the_renamed_bootstrap_milestone_under_its_v1beta2_name(self) -> None:
        for record in self.v1beta1 + self.v1beta2:
            with self.subTest(name=record["name"]):
                self.assertIn("BootstrapConfigReady", record["phases"])
                self.assertNotIn("BootstrapReady", record["phases"])

    def test_recovers_the_platform_from_the_infrastructure_ref_without_a_hosted_cluster(
        self,
    ) -> None:
        # The v1beta2 tree holds machines only, so the platform has to come from the
        # infrastructureRef kind rather than from a HostedCluster in the same dump.
        for record in self.v1beta2:
            self.assertEqual(record["platform"], "AWS")
            self.assertEqual(record["metadata"]["infrastructureKind"], "AWSMachine")


class TestPrintReport(unittest.TestCase):
    def test_prints_phases_in_the_order_they_were_reached(self) -> None:
        buffer = io.StringIO()
        with redirect_stdout(buffer):
            TIMINGS.print_report(TIMINGS.extract(ARTIFACTS))
        output = buffer.getvalue()

        self.assertIn("HostedCluster: e2e-clusters-f92vm/create-cluster-km2vj", output)
        self.assertIn("InfrastructureReady           : +  1.0m (60s)", output)
        self.assertIn("Available                     : + 31.9m (1911s)", output)
        self.assertIn("NodePool: create-cluster-km2vj (2 replicas, m5.large)", output)
        self.assertIn("Machines (2):", output)
        # AllMachinesReady transitioned before ReachedIgnitionEndpoint, so it must print first
        # even though the canonical phase list orders them the other way around.
        self.assertLess(output.index("AllMachinesReady"), output.index("ReachedIgnitionEndpoint"))

    def test_reports_machines_whose_hosted_cluster_was_not_in_the_dump(self) -> None:
        buffer = io.StringIO()
        with redirect_stdout(buffer):
            TIMINGS.print_report(TIMINGS.extract(ARTIFACTS_CAPI_V1BETA2))
        output = buffer.getvalue()

        self.assertIn(
            "Machines without a HostedCluster dump: e2e-clusters-f92vm-create-cluster-km2vj",
            output,
        )
        self.assertIn("Machines (2):", output)
        self.assertIn("BootstrapConfigReady", output)
        self.assertNotIn("No HostedCluster", output.replace("without a HostedCluster", ""))

    def test_says_so_when_a_tree_holds_no_timings_at_all(self) -> None:
        buffer = io.StringIO()
        with redirect_stdout(buffer):
            TIMINGS.print_report([])
        self.assertIn("No HostedCluster or Machine dumps found.", buffer.getvalue())

    def test_reports_milestones_that_were_never_reached(self) -> None:
        buffer = io.StringIO()
        with redirect_stdout(buffer):
            TIMINGS.print_report(
                [
                    TIMINGS.record(
                        TIMINGS.METRIC_BRING_UP,
                        "Azure",
                        "clusters",
                        "stuck",
                        datetime(2026, 9, 11, 14, 50, 33, tzinfo=timezone.utc),
                        None,
                        {"InfrastructureReady": 60.0},
                        {},
                    )
                ]
            )
        output = buffer.getvalue()
        self.assertIn("Available                     : not reached", output)
        self.assertIn("Total                         :       n/a", output)


class TestNodePoolInstanceType(unittest.TestCase):
    def test_reads_the_machine_size_for_each_platform(self) -> None:
        cases = [
            ({"type": "AWS", "aws": {"instanceType": "m5.large"}}, "m5.large"),
            ({"type": "Azure", "azure": {"vmSize": "Standard_D4s_v4"}}, "Standard_D4s_v4"),
            ({"type": "OpenStack", "openstack": {"flavor": "m1.xlarge"}}, "m1.xlarge"),
            ({"type": "KubeVirt", "kubevirt": {"compute": {"cores": 2, "memory": "8Gi"}}}, "2cpu/8Gi"),
            ({"type": "Agent"}, ""),
            ({"type": "AWS"}, ""),
        ]
        for platform_spec, expected in cases:
            with self.subTest(platform=platform_spec.get("type")):
                self.assertEqual(TIMINGS.node_pool_instance_type(platform_spec), expected)


class TestPercentile(unittest.TestCase):
    def test_interpolates_between_samples(self) -> None:
        values = [float(v) for v in range(1, 11)]
        self.assertEqual(TIMINGS.percentile(values, 50), 5.5)
        self.assertAlmostEqual(TIMINGS.percentile(values, 95), 9.55)
        self.assertAlmostEqual(TIMINGS.percentile(values, 99), 9.91)

    def test_handles_a_single_sample(self) -> None:
        self.assertEqual(TIMINGS.percentile([42.0], 99), 42.0)

    def test_rejects_an_empty_sample(self) -> None:
        with self.assertRaises(ValueError):
            TIMINGS.percentile([], 50)


class TestSummarize(unittest.TestCase):
    def test_groups_distributions_by_platform_and_metric(self) -> None:
        records = [
            TIMINGS.record(TIMINGS.METRIC_BRING_UP, "AWS", "ns", "a", None, 600.0, {"EtcdAvailable": 60.0}),
            TIMINGS.record(TIMINGS.METRIC_BRING_UP, "AWS", "ns", "b", None, 800.0, {"EtcdAvailable": 80.0}),
            TIMINGS.record(TIMINGS.METRIC_BRING_UP, "Azure", "ns", "c", None, 900.0),
        ]
        summary = TIMINGS.summarize(TIMINGS.group_records(records, "", ""))

        self.assertEqual(summary["AWS"][TIMINGS.METRIC_BRING_UP]["sampleCount"], 2)
        self.assertEqual(summary["AWS"][TIMINGS.METRIC_BRING_UP]["p50"], 700.0)
        self.assertEqual(summary["AWS"][TIMINGS.METRIC_BRING_UP]["phases"]["EtcdAvailable"]["p50"], 70.0)
        self.assertEqual(summary["Azure"][TIMINGS.METRIC_BRING_UP]["sampleCount"], 1)

    def test_platform_filter_keeps_baselines_provider_specific(self) -> None:
        records = [
            TIMINGS.record(TIMINGS.METRIC_BRING_UP, "AWS", "ns", "a", None, 600.0),
            TIMINGS.record(TIMINGS.METRIC_BRING_UP, "Azure", "ns", "b", None, 900.0),
        ]
        summary = TIMINGS.summarize(TIMINGS.group_records(records, "aws", ""))
        self.assertEqual(list(summary), ["AWS"])

    def test_drops_records_without_a_duration(self) -> None:
        records = [TIMINGS.record(TIMINGS.METRIC_BRING_UP, "AWS", "ns", "a", None, None)]
        self.assertEqual(TIMINGS.group_records(records, "", ""), {})


class TestCompareToBaseline(unittest.TestCase):
    def baseline(self, duration, phase_duration=100.0):
        return {
            "schemaVersion": TIMINGS.SCHEMA_VERSION,
            "baselines": {
                "AWS": {
                    TIMINGS.METRIC_BRING_UP: {
                        "sampleCount": 20,
                        "p50": duration,
                        "phases": {"EtcdAvailable": {"p50": phase_duration}},
                    }
                }
            },
        }

    def summary(self, duration, phase_duration=100.0):
        records = [
            TIMINGS.record(
                TIMINGS.METRIC_BRING_UP, "AWS", "ns", "a", None, duration, {"EtcdAvailable": phase_duration}
            )
        ]
        return TIMINGS.summarize(TIMINGS.group_records(records, "", ""))

    def test_flags_a_metric_that_regressed_past_the_threshold(self) -> None:
        _, regressions = TIMINGS.compare_to_baseline(
            self.summary(1260.0), self.baseline(1000.0), 20.0, "p50"
        )
        self.assertEqual(len(regressions), 1)
        self.assertAlmostEqual(regressions[0]["deltaPercent"], 26.0)

    def test_accepts_a_metric_inside_the_threshold(self) -> None:
        _, regressions = TIMINGS.compare_to_baseline(
            self.summary(1150.0), self.baseline(1000.0), 20.0, "p50"
        )
        self.assertEqual(regressions, [])

    def test_accepts_an_improvement(self) -> None:
        _, regressions = TIMINGS.compare_to_baseline(
            self.summary(500.0), self.baseline(1000.0), 20.0, "p50"
        )
        self.assertEqual(regressions, [])

    def test_flags_a_phase_regression_even_when_the_total_is_flat(self) -> None:
        _, regressions = TIMINGS.compare_to_baseline(
            self.summary(1000.0, phase_duration=200.0), self.baseline(1000.0, phase_duration=100.0), 20.0, "p50"
        )
        self.assertEqual(len(regressions), 1)
        self.assertEqual(regressions[0]["phase"], "EtcdAvailable")

    def test_reports_metrics_that_have_no_baseline_entry(self) -> None:
        summary = TIMINGS.summarize(
            TIMINGS.group_records(
                [TIMINGS.record(TIMINGS.METRIC_NODE_JOIN, "Azure", "ns", "a", None, 600.0)], "", ""
            )
        )
        comparisons, regressions = TIMINGS.compare_to_baseline(summary, self.baseline(1000.0), 20.0, "p50")
        self.assertEqual(regressions, [])
        self.assertEqual([c["status"] for c in comparisons], ["no-baseline"])


class TestRunCommands(unittest.TestCase):
    def test_extract_then_compare_gates_on_a_regression(self) -> None:
        with tempfile.TemporaryDirectory() as workdir:
            records_path = Path(workdir) / "records.json"
            baseline_path = Path(workdir) / "aws.json"

            with redirect_stdout(io.StringIO()):
                self.assertEqual(
                    TIMINGS.main(["extract", str(ARTIFACTS), "--json", str(records_path)]),
                    TIMINGS.EXIT_OK,
                )
                self.assertEqual(
                    TIMINGS.main(
                        ["compare", str(records_path), "--write-baseline", str(baseline_path)]
                    ),
                    TIMINGS.EXIT_OK,
                )
                # Same data against its own baseline is stable.
                self.assertEqual(
                    TIMINGS.main(["compare", str(records_path), "--baseline", str(baseline_path)]),
                    TIMINGS.EXIT_OK,
                )

            # A 50% slower node join must fail the gate.
            slow = []
            for line in records_path.read_text(encoding="utf-8").splitlines():
                item = json.loads(line)
                if item["metric"] == TIMINGS.METRIC_NODE_JOIN:
                    item["durationSeconds"] *= 1.5
                slow.append(json.dumps(item))
            slow_path = Path(workdir) / "slow.json"
            slow_path.write_text("\n".join(slow) + "\n", encoding="utf-8")

            with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
                self.assertEqual(
                    TIMINGS.main(["compare", str(slow_path), "--baseline", str(baseline_path)]),
                    TIMINGS.EXIT_REGRESSION,
                )

    def test_report_lists_exactly_the_regressions_the_exit_code_gates_on(self) -> None:
        # A single-sample run under --min-samples must not be reported as a regression either,
        # otherwise the dashboard and the gate disagree about what failed.
        with tempfile.TemporaryDirectory() as workdir:
            baseline_path = Path(workdir) / "aws.json"
            records_path = Path(workdir) / "records.json"
            report_path = Path(workdir) / "report.json"

            TIMINGS.write_json(
                baseline_path,
                {
                    "schemaVersion": TIMINGS.SCHEMA_VERSION,
                    "baselines": {
                        "AWS": {TIMINGS.METRIC_BRING_UP: {"sampleCount": 20, "p50": 1000.0}}
                    },
                },
            )
            records_path.write_text(
                json.dumps(
                    TIMINGS.record(TIMINGS.METRIC_BRING_UP, "AWS", "ns", "a", None, 2000.0)
                )
                + "\n",
                encoding="utf-8",
            )

            with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
                exit_code = TIMINGS.main(
                    [
                        "compare",
                        str(records_path),
                        "--baseline",
                        str(baseline_path),
                        "--min-samples",
                        "5",
                        "--report",
                        str(report_path),
                    ]
                )

            self.assertEqual(exit_code, TIMINGS.EXIT_OK)
            report = json.loads(report_path.read_text(encoding="utf-8"))
            self.assertEqual(report["regressions"], [])


class TestInputErrors(unittest.TestCase):
    """Unusable input must exit 2, never 1, so a broken job is not read as a regression."""

    def compare(self, argv):
        stderr = io.StringIO()
        with redirect_stdout(io.StringIO()), redirect_stderr(stderr):
            return TIMINGS.main(argv), stderr.getvalue()

    def test_a_glob_that_matches_nothing_is_an_input_error(self) -> None:
        with tempfile.TemporaryDirectory() as workdir:
            exit_code, stderr = self.compare(["compare", str(Path(workdir) / "*.json")])
            self.assertEqual(exit_code, TIMINGS.EXIT_ERROR)
            self.assertIn("no input matched", stderr)

    def test_a_missing_records_file_is_an_input_error(self) -> None:
        with tempfile.TemporaryDirectory() as workdir:
            exit_code, stderr = self.compare(["compare", str(Path(workdir) / "absent.json")])
            self.assertEqual(exit_code, TIMINGS.EXIT_ERROR)
            self.assertIn("failed to read timing records", stderr)

    def test_a_malformed_json_array_is_an_input_error(self) -> None:
        with tempfile.TemporaryDirectory() as workdir:
            records_path = Path(workdir) / "records.json"
            records_path.write_text("[{\"metric\": ", encoding="utf-8")
            exit_code, stderr = self.compare(["compare", str(records_path)])
            self.assertEqual(exit_code, TIMINGS.EXIT_ERROR)
            self.assertIn("not valid JSON", stderr)

    def test_an_unreadable_or_stale_baseline_is_an_input_error(self) -> None:
        with tempfile.TemporaryDirectory() as workdir:
            records_path = Path(workdir) / "records.json"
            records_path.write_text(
                json.dumps(
                    TIMINGS.record(TIMINGS.METRIC_BRING_UP, "AWS", "ns", "a", None, 600.0)
                )
                + "\n",
                encoding="utf-8",
            )
            cases = {
                "absent.json": None,
                "stale.json": {"schemaVersion": TIMINGS.SCHEMA_VERSION + 1, "baselines": {}},
                "incomplete.json": {"schemaVersion": TIMINGS.SCHEMA_VERSION},
                "notanobject.json": [],
            }
            for name, payload in cases.items():
                baseline_path = Path(workdir) / name
                if payload is not None:
                    baseline_path.write_text(json.dumps(payload), encoding="utf-8")
                with self.subTest(baseline=name):
                    exit_code, stderr = self.compare(
                        ["compare", str(records_path), "--baseline", str(baseline_path)]
                    )
                    self.assertEqual(exit_code, TIMINGS.EXIT_ERROR)
                    self.assertIn("failed to read baseline", stderr)


if __name__ == "__main__":
    unittest.main()
