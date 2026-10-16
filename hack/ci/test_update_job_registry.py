#!/usr/bin/env python3
"""Tests for the HyperShift CI job registry generator."""

from __future__ import annotations

import importlib.util
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("update-job-registry.py")
SPEC = importlib.util.spec_from_file_location("update_job_registry", MODULE_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"Unable to load {MODULE_PATH}")
REGISTRY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(REGISTRY)


def periodic_job(version: str, suffix: str) -> str:
    return f"periodic-ci-openshift-hypershift-release-{version}-periodics-{suffix}"


class TestCategorizeJob(unittest.TestCase):
    def test_classifies_jobs_by_test_framework(self) -> None:
        cases = [
            (
                periodic_job("5.1", "e2e-v2-aws"),
                ("aws", "5.1", REGISTRY.FRAMEWORK_V2),
            ),
            (
                periodic_job("5.1", "e2e-aws-upgrade"),
                ("aws", "5.1", REGISTRY.FRAMEWORK_V1),
            ),
            (
                periodic_job("5.1", "azure-perf-azure-self-managed-performance"),
                ("azure", "5.1", REGISTRY.FRAMEWORK_V1),
            ),
        ]

        for job_name, expected in cases:
            with self.subTest(job_name=job_name):
                self.assertEqual(REGISTRY.categorize_job(job_name), expected)

    def test_returns_none_for_unrecognized_job(self) -> None:
        self.assertIsNone(REGISTRY.categorize_job("not-a-hypershift-periodic"))


class TestBuildCategories(unittest.TestCase):
    def test_splits_frameworks_and_orders_by_version_then_platform(self) -> None:
        jobs = [
            periodic_job("5.1", "e2e-aks"),
            periodic_job("5.1", "e2e-aws-upgrade"),
            periodic_job("5.1", "e2e-v2-aws"),
            periodic_job("5.0", "e2e-aws-upgrade"),
            periodic_job("5.0", "e2e-v2-aws"),
            periodic_job("5.1", "e2e-v2-gke"),
            periodic_job("5.0", "e2e-v2-gke"),
            periodic_job("4.23", "e2e-v2-gke"),
            periodic_job("5.1", "e2e-powervs-ovn"),
            periodic_job("4.22", "e2e-powervs-ovn"),
        ]

        categories = REGISTRY.build_categories(
            jobs, {"5.1", "5.0", "4.23", "4.22"}
        )

        self.assertEqual(
            [category["name"] for category in categories],
            [
                "ARO HCP (AKS) (5.1) (v1)",
                "AWS (5.1) (v1)",
                "AWS (5.1) (v2)",
                "GKE (v2)",
                "IBM / PowerVS (v1)",
                "AWS (5.0) (v1)",
                "AWS (5.0) (v2)",
            ],
        )

        gke = next(category for category in categories if category["name"] == "GKE (v2)")
        self.assertEqual(gke["ocp_versions"], ["5.1", "5.0", "4.23"])

        aws_v1 = next(category for category in categories if category["name"] == "AWS (5.1) (v1)")
        aws_v2 = next(category for category in categories if category["name"] == "AWS (5.1) (v2)")
        self.assertEqual(aws_v1["jobs"], [periodic_job("5.1", "e2e-aws-upgrade")])
        self.assertEqual(aws_v2["jobs"], [periodic_job("5.1", "e2e-v2-aws")])

        categorized_jobs = [job for category in categories for job in category["jobs"]]
        self.assertCountEqual(categorized_jobs, jobs)
        self.assertEqual(len(categorized_jobs), len(set(categorized_jobs)))


class TestRenderYaml(unittest.TestCase):
    def test_preserves_category_metadata_in_yaml(self) -> None:
        category = {
            "name": "AWS (5.1) (v2)",
            "description": "AWS hosted control planes — OCP 5.1 — v2",
            "platform": "AWS",
            "ocp_versions": ["5.1"],
            "test_framework": "v2",
            "jobs": [periodic_job("5.1", "e2e-v2-aws")],
        }

        rendered = REGISTRY.render_yaml([category])
        loaded = REGISTRY.yaml.safe_load(rendered)

        self.assertEqual(loaded["categories"], [category])


if __name__ == "__main__":
    unittest.main()
