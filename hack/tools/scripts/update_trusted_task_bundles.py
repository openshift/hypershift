#!/usr/bin/env python3
"""
Update Tekton Pipeline Task Bundles to Latest Trusted Versions

Fetches the trusted tasks data from the Konflux data-acceptable-bundles OCI artifact
and updates pipeline YAML files to use the latest trusted task bundle digests.

Usage:
    ./update_trusted_task_bundles.py [pipeline_files...] [options]

Examples:
    # Update pipeline task bundles to latest trusted versions
    ./update_trusted_task_bundles.py .tekton/pipelines/common-operator-build.yaml

    # Check what would be updated without making changes
    ./update_trusted_task_bundles.py .tekton/pipelines/*.yaml --dry-run

    # Show diff of changes (without applying)
    ./update_trusted_task_bundles.py .tekton/pipelines/*.yaml --dry-run --diff

    # Use a different data source
    ./update_trusted_task_bundles.py pipeline.yaml --data-source quay.io/other/data:latest

Requirements:
    - Python 3.8+
    - PyYAML (pip install pyyaml)
    - skopeo (for fetching OCI artifacts)
"""

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
from dataclasses import dataclass, field
from pathlib import Path
from typing import Dict, List, Optional, Tuple

try:
    import yaml
    HAS_YAML = True
except ImportError:
    HAS_YAML = False

# Default data sources for trusted tasks
DEFAULT_DATA_SOURCES = [
    "quay.io/konflux-ci/tekton-catalog/data-acceptable-bundles:latest",
]

# Bundle reference pattern: quay.io/konflux-ci/tekton-catalog/task-name:version@sha256:digest
BUNDLE_PATTERN = re.compile(
    r'^(?P<registry>[^/]+/[^/]+/[^/]+)/(?P<task>task-[^:]+):(?P<version>[^@]+)@(?P<digest>sha256:[a-f0-9]+)$'
)


@dataclass
class TaskUpdate:
    """Represents a task bundle that needs updating."""
    task_name: str
    current_version: str
    current_digest: str
    latest_version: str
    latest_digest: str
    registry: str
    location: str  # file:line description
    # Optional: info about newer version available (when not applying version upgrades)
    newer_version_available: Optional[str] = None
    newer_version_digest: Optional[str] = None

    @property
    def current_ref(self) -> str:
        return f"{self.registry}/{self.task_name}:{self.current_version}@{self.current_digest}"

    @property
    def latest_ref(self) -> str:
        return f"{self.registry}/{self.task_name}:{self.latest_version}@{self.latest_digest}"

    @property
    def needs_update(self) -> bool:
        return self.current_digest != self.latest_digest

    @property
    def is_version_bump(self) -> bool:
        return self.current_version != self.latest_version

    @property
    def has_newer_version(self) -> bool:
        return self.newer_version_available is not None


@dataclass
class TrustedTasksData:
    """Container for trusted tasks data from OCI artifact."""
    trusted_tasks: Dict[str, List[dict]] = field(default_factory=dict)
    source: str = ""

    def get_latest_trusted(self, task_key: str) -> Optional[Tuple[str, str]]:
        """Get the latest trusted (version, digest) for a task key.

        Args:
            task_key: Key in format 'oci://registry/repo/task-name:version'

        Returns:
            Tuple of (version, digest) or None if not found.
            The latest trusted entry is the first one without expires_on.
        """
        records = self.trusted_tasks.get(task_key, [])
        if not records:
            return None

        # Find first record without expires_on (that's the current latest)
        for record in records:
            if 'expires_on' not in record:
                ref = record.get('ref', '')
                if ref.startswith('sha256:'):
                    # Extract version from key
                    version_match = re.search(r':([0-9.]+)$', task_key)
                    version = version_match.group(1) if version_match else ''
                    return (version, ref)

        # If all have expires_on, return the first one (most recent)
        if records:
            ref = records[0].get('ref', '')
            if ref.startswith('sha256:'):
                version_match = re.search(r':([0-9.]+)$', task_key)
                version = version_match.group(1) if version_match else ''
                return (version, ref)

        return None

    def find_latest_version(self, registry: str, task_name: str) -> Optional[Tuple[str, str]]:
        """Find the latest version and digest for a task across all versions.

        Args:
            registry: Registry prefix (e.g., 'quay.io/konflux-ci/tekton-catalog')
            task_name: Task name (e.g., 'task-init')

        Returns:
            Tuple of (version, digest) for the highest version, or None.
        """
        prefix = f"oci://{registry}/{task_name}:"

        # Find all versions for this task
        versions = []
        for key in self.trusted_tasks.keys():
            if key.startswith(prefix):
                version_str = key[len(prefix):]
                try:
                    version_parts = [int(p) for p in version_str.split('.')]
                    versions.append((version_parts, version_str, key))
                except ValueError:
                    continue

        if not versions:
            return None

        # Sort by version (highest first)
        versions.sort(reverse=True, key=lambda x: x[0])

        # Get the latest trusted digest for the highest version
        _, latest_version, latest_key = versions[0]
        result = self.get_latest_trusted(latest_key)
        if result:
            return (latest_version, result[1])

        return None

    def is_newer_version(self, current: str, candidate: str) -> bool:
        """Check if candidate version is newer than current version.

        Args:
            current: Current version string (e.g., '0.2')
            candidate: Candidate version string (e.g., '0.3')

        Returns:
            True if candidate is newer than current
        """
        try:
            current_parts = [int(p) for p in current.split('.')]
            candidate_parts = [int(p) for p in candidate.split('.')]
            return candidate_parts > current_parts
        except ValueError:
            return False


def fetch_trusted_tasks_data(data_source: str, cache_dir: Optional[str] = None) -> TrustedTasksData:
    """Fetch trusted tasks data from OCI artifact using skopeo.

    Args:
        data_source: OCI image reference (e.g., 'quay.io/konflux-ci/tekton-catalog/data-acceptable-bundles:latest')
        cache_dir: Optional directory to cache the fetched data

    Returns:
        TrustedTasksData object with parsed trusted_tasks
    """
    # Create temp directory for skopeo output
    with tempfile.TemporaryDirectory() as tmpdir:
        dest_dir = Path(tmpdir) / "data"

        # Use skopeo to copy the OCI artifact
        cmd = [
            "skopeo", "copy", "--preserve-digests",
            f"docker://{data_source}",
            f"dir:{dest_dir}"
        ]

        print(f"Fetching trusted tasks data from {data_source}...", file=sys.stderr)
        try:
            result = subprocess.run(cmd, capture_output=True, text=True, check=True)
        except subprocess.CalledProcessError as e:
            print(f"Error fetching data: {e.stderr}", file=sys.stderr)
            raise RuntimeError(f"Failed to fetch trusted tasks data: {e}")
        except FileNotFoundError:
            raise RuntimeError("skopeo not found. Please install skopeo.")

        # Parse the manifest to find the data layer
        manifest_path = dest_dir / "manifest.json"
        if not manifest_path.exists():
            raise RuntimeError("No manifest.json found in fetched data")

        with open(manifest_path) as f:
            manifest = json.load(f)

        # Find the layer with trusted_tekton_tasks.yml
        data_layer = None
        for layer in manifest.get('layers', []):
            annotations = layer.get('annotations', {})
            title = annotations.get('org.opencontainers.image.title', '')
            if 'trusted_tekton_tasks' in title:
                digest = layer.get('digest', '')
                if digest.startswith('sha256:'):
                    data_layer = digest.split(':')[1]
                break

        if not data_layer:
            raise RuntimeError("Could not find trusted_tekton_tasks layer in manifest")

        # Read and parse the YAML data
        data_path = dest_dir / data_layer
        if not data_path.exists():
            raise RuntimeError(f"Data layer {data_layer} not found")

        with open(data_path) as f:
            data = yaml.safe_load(f)

        trusted_tasks = data.get('trusted_tasks', {})
        print(f"Loaded {len(trusted_tasks)} trusted task entries", file=sys.stderr)

        return TrustedTasksData(trusted_tasks=trusted_tasks, source=data_source)


def parse_pipeline_file(filepath: Path) -> Tuple[dict, str]:
    """Parse a pipeline YAML file.

    Returns:
        Tuple of (parsed_data, original_content)
    """
    with open(filepath) as f:
        content = f.read()

    data = yaml.safe_load(content)
    return data, content


def find_bundle_refs_in_pipeline(filepath: Path, content: str) -> List[Tuple[int, str, str]]:
    """Find all bundle references in a pipeline file.

    Returns:
        List of (line_number, full_line, bundle_value) tuples
    """
    refs = []
    lines = content.splitlines()

    for i, line in enumerate(lines, 1):
        # Look for bundle value lines
        if 'value:' in line and ('quay.io' in line or 'registry' in line):
            # Extract the bundle reference
            match = re.search(r'value:\s*(.+)$', line.strip())
            if match:
                bundle_value = match.group(1).strip()
                if BUNDLE_PATTERN.match(bundle_value):
                    refs.append((i, line, bundle_value))

    return refs


@dataclass
class AnalysisResult:
    """Result of analyzing a pipeline file."""
    updates: List[TaskUpdate] = field(default_factory=list)
    # Tasks that have newer versions available but weren't upgraded
    available_upgrades: List[TaskUpdate] = field(default_factory=list)


def analyze_pipeline(
    filepath: Path,
    trusted_data: TrustedTasksData,
    upgrade_versions: bool = False
) -> AnalysisResult:
    """Analyze a pipeline file and find tasks that need updating.

    Args:
        filepath: Path to pipeline YAML file
        trusted_data: Trusted tasks data
        upgrade_versions: If True, upgrade to latest versions (not just digest updates)

    Returns:
        AnalysisResult with updates to apply and available upgrades info
    """
    with open(filepath) as f:
        content = f.read()

    result = AnalysisResult()
    bundle_refs = find_bundle_refs_in_pipeline(filepath, content)

    for line_num, line, bundle_value in bundle_refs:
        match = BUNDLE_PATTERN.match(bundle_value)
        if not match:
            continue

        registry = match.group('registry')
        task_name = match.group('task')
        current_version = match.group('version')
        current_digest = match.group('digest')

        # Build the key for lookup
        task_key = f"oci://{registry}/{task_name}:{current_version}"

        # Get latest trusted digest for this version
        version_result = trusted_data.get_latest_trusted(task_key)

        # Also check if there's a newer version available
        latest_version_result = trusted_data.find_latest_version(registry, task_name)
        newer_version_available = None
        newer_version_digest = None

        if latest_version_result:
            latest_available_version, latest_available_digest = latest_version_result
            if trusted_data.is_newer_version(current_version, latest_available_version):
                newer_version_available = latest_available_version
                newer_version_digest = latest_available_digest

        if upgrade_versions and newer_version_available:
            # Upgrade to the latest version
            update = TaskUpdate(
                task_name=task_name,
                current_version=current_version,
                current_digest=current_digest,
                latest_version=newer_version_available,
                latest_digest=newer_version_digest,
                registry=registry,
                location=f"{filepath.name}:{line_num}"
            )
            result.updates.append(update)
        elif version_result:
            # Update digest for current version
            latest_version, latest_digest = version_result

            update = TaskUpdate(
                task_name=task_name,
                current_version=current_version,
                current_digest=current_digest,
                latest_version=latest_version,
                latest_digest=latest_digest,
                registry=registry,
                location=f"{filepath.name}:{line_num}",
                newer_version_available=newer_version_available,
                newer_version_digest=newer_version_digest
            )

            if update.needs_update:
                result.updates.append(update)
            elif newer_version_available:
                # No digest update needed, but newer version is available
                result.available_upgrades.append(update)
        elif newer_version_available:
            # Current version not in trusted data, but newer version exists
            update = TaskUpdate(
                task_name=task_name,
                current_version=current_version,
                current_digest=current_digest,
                latest_version=current_version,  # Keep current for now
                latest_digest=current_digest,
                registry=registry,
                location=f"{filepath.name}:{line_num}",
                newer_version_available=newer_version_available,
                newer_version_digest=newer_version_digest
            )
            result.available_upgrades.append(update)

    return result


def update_pipeline_content(content: str, updates: List[TaskUpdate]) -> str:
    """Apply updates to pipeline content.

    Args:
        content: Original pipeline file content
        updates: List of TaskUpdate objects

    Returns:
        Updated content string
    """
    result = content

    for update in updates:
        # Replace old reference with new reference
        old_ref = update.current_ref
        new_ref = update.latest_ref
        result = result.replace(old_ref, new_ref)

    return result


def print_diff(filepath: Path, original: str, updated: str):
    """Print a unified diff of the changes."""
    import difflib

    diff = difflib.unified_diff(
        original.splitlines(keepends=True),
        updated.splitlines(keepends=True),
        fromfile=f"a/{filepath.name}",
        tofile=f"b/{filepath.name}"
    )

    for line in diff:
        if line.startswith('+') and not line.startswith('+++'):
            print(f"\033[32m{line}\033[0m", end='')
        elif line.startswith('-') and not line.startswith('---'):
            print(f"\033[31m{line}\033[0m", end='')
        else:
            print(line, end='')


def print_summary(
    all_results: Dict[Path, AnalysisResult],
    dry_run: bool = False
):
    """Print a summary of all updates and available upgrades."""
    total_updates = sum(len(r.updates) for r in all_results.values())
    total_available_upgrades = sum(len(r.available_upgrades) for r in all_results.values())

    if total_updates == 0 and total_available_upgrades == 0:
        print("\n✅ All task bundles are up to date!")
        return

    if total_updates > 0:
        mode_str = " (dry-run)" if dry_run else ""
        print(f"\n📦 Found {total_updates} task(s) needing updates{mode_str}:\n")

        for filepath, result in all_results.items():
            if not result.updates:
                continue

            print(f"📄 {filepath}")

            version_bumps = [u for u in result.updates if u.is_version_bump]
            digest_updates = [u for u in result.updates if not u.is_version_bump]

            if version_bumps:
                print("  Version upgrades:")
                for u in version_bumps:
                    print(f"    ⬆️  {u.task_name}: {u.current_version} → {u.latest_version}")

            if digest_updates:
                print("  Digest updates:")
                for u in digest_updates:
                    print(f"    🔄 {u.task_name}:{u.current_version} @...{u.current_digest[-12:]} → @...{u.latest_digest[-12:]}")
                    if u.has_newer_version:
                        print(f"        ℹ️  newer version available: {u.newer_version_available}")

            print()

        if dry_run:
            print("ℹ️  Run without --dry-run to apply these changes.\n")

    # Show tasks that are up-to-date but have newer versions available
    upgrades_available = []
    for filepath, result in all_results.items():
        for u in result.available_upgrades:
            upgrades_available.append((filepath, u))

    if upgrades_available:
        print(f"📢 Newer versions available (use --upgrade-versions to apply):\n")
        for filepath, u in upgrades_available:
            print(f"  ⬆️  {u.task_name}: {u.current_version} → {u.newer_version_available}")
        print()


def main():
    parser = argparse.ArgumentParser(
        description='Update Tekton pipeline task bundles to latest trusted versions',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__
    )
    parser.add_argument(
        'pipeline_files',
        nargs='*',
        type=Path,
        help='Pipeline YAML files to update (default: .tekton/pipelines/*.yaml)'
    )
    parser.add_argument(
        '--data-source', '-d',
        default=DEFAULT_DATA_SOURCES[0],
        help=f'OCI image reference for trusted tasks data (default: {DEFAULT_DATA_SOURCES[0]})'
    )
    parser.add_argument(
        '--dry-run', '-n',
        action='store_true',
        help='Show what would be updated without making changes'
    )
    parser.add_argument(
        '--diff',
        action='store_true',
        help='Show diff of changes'
    )
    parser.add_argument(
        '--upgrade-versions', '-u',
        action='store_true',
        help='Also upgrade to newer task versions (not just digest updates)'
    )
    parser.add_argument(
        '--json',
        action='store_true',
        help='Output results as JSON'
    )
    parser.add_argument(
        '--quiet', '-q',
        action='store_true',
        help='Only show errors'
    )

    args = parser.parse_args()

    if not HAS_YAML:
        print("Error: PyYAML is required. Install with: pip install pyyaml", file=sys.stderr)
        sys.exit(1)

    # Find pipeline files
    pipeline_files = args.pipeline_files
    if not pipeline_files:
        # Default: look for .tekton/pipelines/*.yaml
        tekton_dir = Path('.tekton/pipelines')
        if tekton_dir.exists():
            pipeline_files = list(tekton_dir.glob('*.yaml'))

        if not pipeline_files:
            print("No pipeline files specified and no .tekton/pipelines/*.yaml found", file=sys.stderr)
            sys.exit(1)

    # Validate files exist
    for f in pipeline_files:
        if not f.exists():
            print(f"Error: File not found: {f}", file=sys.stderr)
            sys.exit(1)

    # Fetch trusted tasks data
    try:
        trusted_data = fetch_trusted_tasks_data(args.data_source)
    except RuntimeError as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)

    # Analyze each pipeline
    all_results: Dict[Path, AnalysisResult] = {}

    for filepath in pipeline_files:
        result = analyze_pipeline(filepath, trusted_data, args.upgrade_versions)
        all_results[filepath] = result

    # Output results
    if args.json:
        output = {
            str(path): {
                'updates': [
                    {
                        'task_name': u.task_name,
                        'current_version': u.current_version,
                        'current_digest': u.current_digest,
                        'latest_version': u.latest_version,
                        'latest_digest': u.latest_digest,
                        'location': u.location,
                        'is_version_bump': u.is_version_bump,
                        'newer_version_available': u.newer_version_available,
                    }
                    for u in result.updates
                ],
                'available_upgrades': [
                    {
                        'task_name': u.task_name,
                        'current_version': u.current_version,
                        'newer_version_available': u.newer_version_available,
                    }
                    for u in result.available_upgrades
                ]
            }
            for path, result in all_results.items()
        }
        print(json.dumps(output, indent=2))
    else:
        if not args.quiet:
            print_summary(all_results, dry_run=args.dry_run)

    # Show diff or write changes
    for filepath, result in all_results.items():
        if not result.updates:
            continue

        with open(filepath) as f:
            original = f.read()

        updated = update_pipeline_content(original, result.updates)

        if args.diff:
            print_diff(filepath, original, updated)

        if not args.dry_run:
            with open(filepath, 'w') as f:
                f.write(updated)
            if not args.quiet:
                print(f"✅ Updated {filepath}")

    # Exit with status based on whether updates were found
    total_updates = sum(len(r.updates) for r in all_results.values())
    if total_updates > 0 and args.dry_run:
        sys.exit(1)  # Changes needed but not applied (useful for CI)
    sys.exit(0)


if __name__ == '__main__':
    main()
