#!/usr/bin/env python3
"""Discover HyperShift periodic CI jobs from openshift/release and regenerate
.chai-bot/ci-status-jobs.yaml.

Categorization rules live here — this script is the single source of truth for
how jobs map to report categories. A nightly GitHub Action runs it and opens a
PR when the registry changes.

Usage:
    python3 hack/ci/update-job-registry.py [--versions N] [--output PATH]

Output defaults to stdout. Use --output to write directly to a file.

The script does a sparse checkout of openshift/release (tree:0 filter) to fetch
only the periodic job config files for openshift/hypershift.
"""

from __future__ import annotations

import argparse
import glob
import os
import re
import subprocess
import sys
import tempfile
from collections import defaultdict
from datetime import datetime, timezone
from typing import Callable, TypedDict

try:
    import yaml
except ImportError:
    sys.exit("PyYAML is required: pip install pyyaml")

RELEASE_REPO = "https://github.com/openshift/release.git"
JOBS_DIR = "ci-operator/jobs/openshift/hypershift"
DEFAULT_OUTPUT = None
DEFAULT_NUM_VERSIONS = 3

# ---------------------------------------------------------------------------
# Categorization rules — first match wins
# ---------------------------------------------------------------------------

PlatformRule = tuple[str, str, Callable[[str], bool]]

# Each entry: (platform_key, display_name, matcher)
# matcher receives the job suffix after "release-{VERSION}-periodics-".
PLATFORM_RULES: list[PlatformRule] = [
    ("mce",       "MCE Agent",        lambda s: s.startswith("mce-")),
    ("hcm",       "HCM & Other",      lambda s: s.startswith("hcm-") or "gke" in s or "backuprestore" in s),
    ("openstack", "OpenStack",        lambda s: "openstack" in s),
    ("azure",     "Azure & KubeVirt", lambda s: any(k in s for k in ("aks", "azure", "kubevirt"))),
    ("ibm",       "IBM / PowerVS",    lambda s: any(k in s for k in ("ibmcloud", "powervs"))),
    ("aws",       "AWS",              lambda s: "aws" in s),
]

# Platforms with enough jobs per version to warrant splitting into separate
# per-version categories. Others are combined across versions.
VERSION_SPLIT_PLATFORMS: set[str] = {"aws", "azure"}

# Descriptions per platform key — {version} is substituted for split platforms.
DESCRIPTIONS: dict[str, str] = {
    "aws":       "AWS hosted control planes — OCP {version}",
    "azure":     "Azure AKS, Azure v2, and KubeVirt platforms — OCP {version}",
    "openstack": "OpenStack hosted control planes",
    "ibm":       "IBM Cloud and PowerVS platforms",
    "mce":       "Agent-based bare metal installs via MCE",
    "hcm":       "Hosted Cluster Manager, GKE, and backup/restore tests",
}


class Category(TypedDict):
    name: str
    description: str
    jobs: list[str]


def sparse_checkout(dest: str) -> str:
    """Sparse-checkout only the hypershift periodic job configs from
    openshift/release. Uses tree:0 filter so only the trees and blobs along
    the target path are fetched."""
    subprocess.run(
        ["git", "clone", "--depth=1", "--filter=tree:0", "--no-checkout", "--sparse",
         RELEASE_REPO, dest],
        check=True, text=True, timeout=120,
    )
    subprocess.run(
        ["git", "sparse-checkout", "set", JOBS_DIR],
        cwd=dest, check=True, text=True, timeout=30,
    )
    subprocess.run(
        ["git", "checkout"],
        cwd=dest, check=True, text=True, timeout=30,
    )
    result = subprocess.run(
        ["git", "rev-parse", "--short", "HEAD"],
        cwd=dest, check=True, capture_output=True, text=True, timeout=10,
    )
    return result.stdout.strip()


def discover_periodic_files(tmpdir: str) -> list[str]:
    """Return sorted list of release periodic config file paths."""
    pattern = os.path.join(tmpdir, JOBS_DIR, "*-release-*-periodics.yaml")
    return sorted(glob.glob(pattern))


def parse_version(filepath: str) -> str | None:
    """Extract version string from a periodic config filename."""
    m = re.search(r"release-([\d.]+)-periodics", os.path.basename(filepath))
    return m.group(1) if m else None


def version_sort_key(v: str) -> tuple[int, ...]:
    """Sort version strings numerically (e.g., 4.22 < 4.23 < 5.0)."""
    return tuple(int(x) for x in v.split("."))


def extract_job_names(filepath: str) -> list[str]:
    """Parse job names from a periodic config file."""
    with open(filepath) as f:
        data = yaml.safe_load(f)
    return [
        p["name"]
        for p in data.get("periodics", [])
        if "name" in p
        and re.search(r"release-[\d.]+-periodics-", p["name"])
    ]


def categorize_job(job_name: str) -> tuple[str, str] | None:
    """Return (platform_key, version) for a job name, or None if uncategorized."""
    m = re.match(
        r"periodic-ci-openshift-hypershift-release-([\d.]+)-periodics-(.*)",
        job_name,
    )
    if not m:
        return None
    version, suffix = m.group(1), m.group(2)
    for key, _display, matcher in PLATFORM_RULES:
        if matcher(suffix):
            return key, version
    return None


def build_categories(all_jobs: list[str], selected_versions: set[str]) -> list[Category]:
    """Group jobs into categories, returning ordered list of category dicts."""
    grouped: dict[str, dict[str, list[str]]] = defaultdict(lambda: defaultdict(list))

    for job in sorted(all_jobs):
        result = categorize_job(job)
        if result is None:
            print(f"::error::Uncategorized job: {job}")
            continue
        key, version = result
        if version not in selected_versions:
            continue
        grouped[key][version].append(job)

    categories: list[Category] = []

    for key, display_name, _ in PLATFORM_RULES:
        if key not in grouped:
            continue
        versions = sorted(grouped[key].keys(), key=version_sort_key, reverse=True)
        desc_template = DESCRIPTIONS.get(key, "")

        if key in VERSION_SPLIT_PLATFORMS and len(versions) > 1:
            for v in versions:
                jobs = grouped[key][v]
                if not jobs:
                    continue
                categories.append(Category(
                    name=f"{display_name} ({v})",
                    description=desc_template.format(version=v),
                    jobs=sorted(jobs),
                ))
        else:
            all_platform_jobs: list[str] = []
            version_strs: list[str] = []
            for v in versions:
                all_platform_jobs.extend(grouped[key][v])
                version_strs.append(v)
            if not all_platform_jobs:
                continue
            desc = desc_template
            if "{version}" in desc:
                desc = desc.format(version=" + ".join(version_strs))
            elif version_strs:
                desc += f" — OCP {' + '.join(version_strs)}"
            categories.append(Category(
                name=display_name,
                description=desc,
                jobs=sorted(all_platform_jobs),
            ))

    return categories


def render_yaml(categories: list[Category]) -> str:
    """Render categories as the registry YAML string."""
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    lines: list[str] = [
        "# HyperShift CI Periodic Job Registry",
        "#",
        "# Auto-generated by hack/ci/update-job-registry.py from openshift/release.",
        "# Do not edit manually — changes will be overwritten by the nightly update.",
        f"# Last generated: {now}",
        "#",
        "# To change categorization rules, edit hack/ci/update-job-registry.py.",
        "# To add/remove jobs, update the periodic configs in openshift/release.",
        "",
        "categories:",
    ]
    for i, cat in enumerate(categories):
        if i > 0:
            lines.append("")
        lines.append(f'  - name: "{cat["name"]}"')
        lines.append(f'    description: "{cat["description"]}"')
        lines.append("    jobs:")
        for job in cat["jobs"]:
            lines.append(f"      - {job}")
    lines.append("")
    return "\n".join(lines)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--versions", type=int, default=DEFAULT_NUM_VERSIONS,
        help=f"Number of most recent release versions to include (default: {DEFAULT_NUM_VERSIONS})",
    )
    parser.add_argument(
        "--output", default=DEFAULT_OUTPUT,
        help="Output file path (default: stdout)",
    )
    args = parser.parse_args()

    with tempfile.TemporaryDirectory() as tmpdir:
        clone_dir = os.path.join(tmpdir, "release")
        print("Sparse-checkout of openshift/release (tree:0 filter)...", file=sys.stderr)
        release_sha = sparse_checkout(clone_dir)

        periodic_files = discover_periodic_files(clone_dir)
        if not periodic_files:
            sys.exit("ERROR: no periodic job config files found")

        all_versions: list[str] = []
        for f in periodic_files:
            v = parse_version(f)
            if v:
                all_versions.append(v)
        all_versions = sorted(set(all_versions), key=version_sort_key, reverse=True)

        selected: set[str] = set(all_versions[:args.versions])
        print(f"Versions available: {', '.join(all_versions)}", file=sys.stderr)
        print(f"Selected (latest {args.versions}): {', '.join(sorted(selected, key=version_sort_key, reverse=True))}", file=sys.stderr)

        all_jobs: list[str] = []
        for f in periodic_files:
            v = parse_version(f)
            if v not in selected:
                continue
            jobs = extract_job_names(f)
            print(f"  {os.path.basename(f)}: {len(jobs)} jobs", file=sys.stderr)
            all_jobs.extend(jobs)

    print(f"Total jobs discovered: {len(all_jobs)}", file=sys.stderr)

    categories = build_categories(all_jobs, selected)
    output = render_yaml(categories)

    total_categorized = sum(len(c["jobs"]) for c in categories)
    print(f"Categorized {total_categorized} jobs into {len(categories)} categories", file=sys.stderr)

    uncategorized = len(all_jobs) - total_categorized
    if uncategorized > 0:
        sys.exit(f"::error::{uncategorized} uncategorized jobs — update PLATFORM_RULES")

    if args.output:
        with open(args.output, "w") as f:
            f.write(output)
        print(f"Wrote {args.output}", file=sys.stderr)
    else:
        sys.stdout.write(output)
    print(f"release_sha={release_sha}", file=sys.stderr)


if __name__ == "__main__":
    main()
