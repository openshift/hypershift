#!/usr/bin/env python3
"""
Konflux Tekton Task Version Lookup

Parses enterprise contract verification logs to identify outdated Tekton tasks
and uses the quay.io registry API to map digests to version tags.

Usage:
    ./konflux_task_version_lookup.py <ec-log-file> [--output json|summary] [--verbose] [--debug]

Example:
    ./konflux_task_version_lookup.py /path/to/enterprise-contract-verify.log
    ./konflux_task_version_lookup.py /path/to/ec-verify.log --output summary
    ./konflux_task_version_lookup.py /path/to/ec-verify.log --verbose
    ./konflux_task_version_lookup.py /path/to/ec-verify.log --debug

Authentication:
    The script will try to get registry auth in this order:
    1. QUAY_REGISTRY_TOKEN environment variable
    2. Skopeo/Podman auth file (~/.config/containers/auth.json or $XDG_RUNTIME_DIR/containers/auth.json)
    3. Docker config file (~/.docker/config.json)
    4. Anonymous access (works for public repos like konflux-ci)

Requirements:
    - Python 3.8+
    - aiohttp (pip install aiohttp)
"""

import asyncio
import argparse
import json
import logging
import os
import re
import sys
from typing import Dict, List, Optional, Tuple

try:
    import aiohttp
    HAS_AIOHTTP = True
except ImportError:
    HAS_AIOHTTP = False

# Quay.io registry constants
QUAY_REGISTRY = "quay.io"
TASK_CATALOG_REPO = "konflux-ci/tekton-catalog"

# Concurrency limit
MAX_CONCURRENT_REQUESTS = 20

# Configure logging
logger = logging.getLogger(__name__)


def setup_logging(verbose: bool = False, debug: bool = False):
    """Configure logging based on verbosity level."""
    if debug:
        level = logging.DEBUG
    elif verbose:
        level = logging.INFO
    else:
        level = logging.WARNING

    logging.basicConfig(
        level=level,
        format='%(levelname)s: %(message)s',
        stream=sys.stderr
    )


def get_auth_token_from_env() -> Optional[str]:
    """Get auth token from environment variable."""
    token = os.environ.get('QUAY_REGISTRY_TOKEN')
    if token:
        logger.info("Found auth token in QUAY_REGISTRY_TOKEN environment variable")
    return token


def get_auth_from_containers_config() -> Tuple[Optional[str], Optional[str]]:
    """Get auth from skopeo/podman containers auth.json.

    Returns:
        Tuple of (auth_token, source_path) or (None, None) if not found.
    """
    # Check XDG_RUNTIME_DIR first (podman default)
    xdg_runtime = os.environ.get('XDG_RUNTIME_DIR', '')
    if xdg_runtime:
        auth_file = os.path.join(xdg_runtime, 'containers', 'auth.json')
        if os.path.exists(auth_file):
            logger.debug(f"Checking containers auth file: {auth_file}")
            try:
                with open(auth_file) as f:
                    data = json.load(f)
                    auths = data.get('auths', {})
                    if QUAY_REGISTRY in auths:
                        logger.info(f"Found auth for {QUAY_REGISTRY} in {auth_file}")
                        return auths[QUAY_REGISTRY].get('auth'), auth_file
            except (json.JSONDecodeError, IOError) as e:
                logger.debug(f"Failed to read {auth_file}: {e}")

    # Check ~/.config/containers/auth.json
    config_auth = os.path.join(os.path.expanduser('~'), '.config', 'containers', 'auth.json')
    if os.path.exists(config_auth):
        logger.debug(f"Checking containers auth file: {config_auth}")
        try:
            with open(config_auth) as f:
                data = json.load(f)
                auths = data.get('auths', {})
                if QUAY_REGISTRY in auths:
                    logger.info(f"Found auth for {QUAY_REGISTRY} in {config_auth}")
                    return auths[QUAY_REGISTRY].get('auth'), config_auth
        except (json.JSONDecodeError, IOError) as e:
            logger.debug(f"Failed to read {config_auth}: {e}")

    return None, None


def get_auth_from_docker_config() -> Tuple[Optional[str], Optional[str]]:
    """Get auth from Docker config.json.

    Returns:
        Tuple of (auth_token, source_path) or (None, None) if not found.
    """
    docker_config = os.path.join(os.path.expanduser('~'), '.docker', 'config.json')
    if os.path.exists(docker_config):
        logger.debug(f"Checking Docker config file: {docker_config}")
        try:
            with open(docker_config) as f:
                data = json.load(f)
                auths = data.get('auths', {})
                if QUAY_REGISTRY in auths:
                    logger.info(f"Found auth for {QUAY_REGISTRY} in {docker_config}")
                    return auths[QUAY_REGISTRY].get('auth'), docker_config
        except (json.JSONDecodeError, IOError) as e:
            logger.debug(f"Failed to read {docker_config}: {e}")
    return None, None


def get_registry_auth() -> Tuple[Optional[str], str]:
    """Get registry auth token from available sources.

    Returns:
        Tuple of (auth_token, source_description).
    """
    # 1. Environment variable (already a bearer token)
    token = get_auth_token_from_env()
    if token:
        return token, "QUAY_REGISTRY_TOKEN environment variable"

    # 2. Containers auth (base64 encoded user:pass)
    auth, path = get_auth_from_containers_config()
    if auth:
        return auth, f"containers auth ({path})"

    # 3. Docker config (base64 encoded user:pass)
    auth, path = get_auth_from_docker_config()
    if auth:
        return auth, f"Docker config ({path})"

    logger.info("No registry auth found, using anonymous access")
    return None, "anonymous access"


class KonfluxTaskLookup:
    """Async client for looking up Konflux task versions from quay.io registry."""

    def __init__(self, auth_token: Optional[str] = None):
        self.session: Optional[aiohttp.ClientSession] = None
        self.semaphore = asyncio.Semaphore(MAX_CONCURRENT_REQUESTS)
        self.base_auth = auth_token
        self.token_cache: Dict[str, str] = {}
        self.request_count = 0

    async def get_bearer_token(self, repo: str) -> str:
        """Get bearer token for registry API access."""
        if repo in self.token_cache:
            logger.debug(f"Using cached token for {repo}")
            return self.token_cache[repo]

        # Build auth URL
        url = f"https://{QUAY_REGISTRY}/v2/auth?service={QUAY_REGISTRY}&scope=repository:{repo}:pull"
        logger.debug(f"GET {url}")

        headers = {}
        if self.base_auth:
            # If we have basic auth, use it to get a bearer token
            if self.base_auth.startswith('Bearer '):
                # Already a bearer token
                self.token_cache[repo] = self.base_auth.replace('Bearer ', '')
                return self.token_cache[repo]
            else:
                # Base64 encoded user:pass - use as Basic auth
                headers['Authorization'] = f'Basic {self.base_auth}'
                logger.debug("Using Basic auth to get bearer token")

        try:
            self.request_count += 1
            async with self.session.get(url, headers=headers) as resp:
                logger.debug(f"  Response: {resp.status}")
                if resp.status == 200:
                    data = await resp.json()
                    token = data.get('token', '')
                    self.token_cache[repo] = token
                    logger.debug(f"  Got bearer token for {repo}")
                    return token
                else:
                    logger.warning(f"Failed to get token for {repo}: HTTP {resp.status}")
        except Exception as e:
            logger.warning(f"Failed to get token for {repo}: {e}")

        return ''

    async def list_tags(self, task_name: str) -> List[str]:
        """List semver tags for a task, handling pagination."""
        repo = f"{TASK_CATALOG_REPO}/task-{task_name}"
        token = await self.get_bearer_token(repo)

        headers = {}
        if token:
            headers["Authorization"] = f"Bearer {token}"

        all_tags = []
        url = f"https://{QUAY_REGISTRY}/v2/{repo}/tags/list"

        # Follow pagination to get all tags
        while url:
            logger.debug(f"GET {url}")

            async with self.semaphore:
                try:
                    self.request_count += 1
                    async with self.session.get(url, headers=headers) as resp:
                        logger.debug(f"  Response: {resp.status}")
                        if resp.status == 200:
                            data = await resp.json()
                            tags = data.get('tags', [])
                            all_tags.extend(tags)

                            # Check for pagination Link header
                            link_header = resp.headers.get('Link', '')
                            next_url = None
                            if link_header:
                                # Parse Link header: </v2/.../tags/list?n=100&last=...>; rel="next"
                                match = re.search(r'<([^>]+)>;\s*rel="next"', link_header)
                                if match:
                                    next_path = match.group(1)
                                    next_url = f"https://{QUAY_REGISTRY}{next_path}"
                                    logger.debug(f"  Found next page: {next_path}")
                            url = next_url
                        else:
                            logger.warning(f"Failed to list tags for {task_name}: HTTP {resp.status}")
                            break
                except Exception as e:
                    logger.warning(f"Failed to list tags for {task_name}: {e}")
                    break

        # Filter to semver tags only (e.g., 0.1, 0.2, 1.0.0)
        semver_pattern = re.compile(r'^[0-9]+\.[0-9]+(\.[0-9]+)?$')
        semver_tags = sorted(
            [t for t in all_tags if semver_pattern.match(t)],
            key=lambda x: [int(p) for p in x.split('.')]
        )
        logger.debug(f"  Found {len(semver_tags)} semver tags for {task_name}: {semver_tags}")
        return semver_tags

    async def get_manifest_digest(self, task_name: str, tag: str) -> str:
        """Get digest for a specific tag."""
        repo = f"{TASK_CATALOG_REPO}/task-{task_name}"
        token = await self.get_bearer_token(repo)

        headers = {
            "Accept": "application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json"
        }
        if token:
            headers["Authorization"] = f"Bearer {token}"

        url = f"https://{QUAY_REGISTRY}/v2/{repo}/manifests/{tag}"
        logger.debug(f"GET {url}")

        async with self.semaphore:
            try:
                self.request_count += 1
                async with self.session.get(url, headers=headers) as resp:
                    logger.debug(f"  Response: {resp.status}")
                    if resp.status == 200:
                        digest = resp.headers.get('Docker-Content-Digest', '')
                        logger.debug(f"  Digest for {task_name}:{tag} = {digest[:20]}...")
                        return digest
                    else:
                        logger.warning(f"Failed to get manifest for {task_name}:{tag}: HTTP {resp.status}")
            except Exception as e:
                logger.warning(f"Failed to get digest for {task_name}:{tag}: {e}")
        return ''

    async def process_task(self, task: dict) -> dict:
        """Process a single task to find version info.

        Version resolution priority:
        1. If task is untrusted (trusted_task.trusted violation) -> use the required digest
        2. If EC provided a recommended_version (from tasks.unsupported) -> use that
        3. If latest_digest matches a semver tag -> use that version
        4. Fallback: use highest available semver version (not current!)
        """
        task_name = task['task_name']
        current_version = task['current_version']
        recommended_version = task.get('recommended_version')
        is_untrusted = task.get('is_untrusted', False)

        logger.info(f"Processing task: {task_name}")
        if is_untrusted:
            logger.info(f"  Task is UNTRUSTED - needs specific digest")
        if recommended_version:
            logger.info(f"  EC recommends version: {recommended_version}")

        # Get available tags
        tags = await self.list_tags(task_name)
        task['available_versions'] = tags

        if not tags:
            task['latest_version'] = current_version
            task['is_version_bump'] = False
            task['error'] = 'No semver tags found'
            logger.warning(f"  {task_name}: No semver tags found in registry!")
            return task

        # Check all tags in parallel to find their digests
        async def check_tag(tag: str) -> tuple:
            digest = await self.get_manifest_digest(task_name, tag)
            return (tag, digest)

        results = await asyncio.gather(*[check_tag(tag) for tag in tags])

        # Build digest -> version map
        digest_to_version = {digest: tag for tag, digest in results if digest}

        # Determine the highest available version
        highest_version = tags[-1] if tags else current_version

        # Also find what version the latest_digest maps to (for informational purposes)
        digest_matched_version = digest_to_version.get(task['latest_digest'])

        # Build version -> digest map (reverse of digest_to_version)
        version_to_digest = {tag: digest for tag, digest in results if digest}

        # Resolution priority:
        # 0. Untrusted task - we have the required digest, find its version
        if is_untrusted:
            # For untrusted tasks, the latest_digest is the REQUIRED digest from EC
            required_version = digest_matched_version
            if required_version:
                task['latest_version'] = required_version
                task['version_source'] = 'untrusted_required'
                logger.info(f"  {task_name}: untrusted task, required digest maps to version {required_version}")
            else:
                # Required digest doesn't match any semver tag - this is unusual
                logger.warning(
                    f"  {task_name}: untrusted task, but required digest doesn't match any semver tag! "
                    f"Digest: {task['latest_digest'][:20]}..."
                )
                task['latest_version'] = highest_version
                task['version_source'] = 'highest_available'
                task['warning'] = f"Required digest not in semver tags, using highest available {highest_version}"
                if highest_version in version_to_digest:
                    task['latest_digest'] = version_to_digest[highest_version]
        # 1. EC recommended version takes precedence (from tasks.unsupported violation)
        elif recommended_version and recommended_version in tags:
            task['latest_version'] = recommended_version
            task['version_source'] = 'ec_recommended'
            # Update latest_digest to match the recommended version
            if recommended_version in version_to_digest:
                task['latest_digest'] = version_to_digest[recommended_version]
            logger.info(f"  {task_name}: using EC recommended version {recommended_version}")
            if digest_matched_version and digest_matched_version != recommended_version:
                logger.info(f"    (digest matched {digest_matched_version}, but EC recommendation takes priority)")
        elif recommended_version:
            # EC recommended a version but it doesn't exist - warning
            logger.warning(
                f"  {task_name}: EC recommended version {recommended_version} not found in registry! "
                f"Available: {tags}"
            )
            # Fall back to highest available
            task['latest_version'] = highest_version
            task['version_source'] = 'highest_available'
            task['warning'] = f"EC recommended version {recommended_version} not found, using {highest_version}"
            # Update digest to match highest_version
            if highest_version in version_to_digest:
                task['latest_digest'] = version_to_digest[highest_version]
            logger.warning(f"  {task_name}: falling back to highest available version {highest_version}")
        elif digest_matched_version:
            # 2. Latest digest matches a semver tag
            task['latest_version'] = digest_matched_version
            task['version_source'] = 'digest_match'
            logger.info(f"  {task_name}: found version {digest_matched_version} matching latest digest")
        else:
            # 3. Fallback: latest digest not in any semver tag
            # This means the digest might be from a commit-hash tag.
            # Use highest available version instead of current version.
            logger.warning(
                f"  {task_name}: latest digest not found in any semver tag! "
                f"Digest: {task['latest_digest'][:20]}..."
            )
            task['latest_version'] = highest_version
            task['version_source'] = 'highest_available'
            task['warning'] = f"Latest digest not in semver tags, using highest available {highest_version}"
            # Update digest to match highest_version
            if highest_version in version_to_digest:
                task['latest_digest'] = version_to_digest[highest_version]
            logger.warning(f"  {task_name}: falling back to highest available version {highest_version}")

        task['is_version_bump'] = task['latest_version'] != current_version

        return task

    async def process_all_tasks(self, tasks: List[dict]) -> List[dict]:
        """Process all tasks in parallel."""
        async with aiohttp.ClientSession() as session:
            self.session = session
            results = await asyncio.gather(*[self.process_task(task) for task in tasks])

        logger.info(f"Total registry API requests: {self.request_count}")

        # Sort by task name
        results.sort(key=lambda x: x['task_name'])
        return results


def parse_ec_log(log_path: str) -> List[dict]:
    """Parse enterprise contract log file to extract outdated task information.

    Parses three types of EC messages:
    1. trusted_task.current warnings - "A newer version of task X exists" with latest digest
    2. tasks.unsupported violations - "Task X is unsupported, update to version Y"
    3. trusted_task.trusted violations - "Untrusted version of PipelineTask X... upgrade to sha256:..."
    """
    logger.info(f"Parsing log file: {log_path}")

    with open(log_path, 'r') as f:
        content = f.read()

    # Find the JSON block after STEP-REPORT-JSON delimiter
    # The JSON may be split across multiple lines due to log line limits
    lines = content.splitlines()
    json_start = None
    for i, line in enumerate(lines):
        if line.strip() == 'STEP-REPORT-JSON':
            json_start = i + 1
            break

    if json_start is None:
        logger.warning("Could not find STEP-REPORT-JSON section in log file")
        return []

    # Concatenate lines until we have valid JSON
    json_parts = []
    for i in range(json_start, len(lines)):
        line = lines[i].strip()
        if not line:
            continue
        json_parts.append(line)
        # Try to parse - if successful, we have complete JSON
        try:
            ec_data = json.loads(''.join(json_parts))
            logger.debug(f"Parsed JSON from {len(json_parts)} lines")
            break
        except json.JSONDecodeError:
            continue
    else:
        logger.error("Could not parse complete JSON from log file")
        return []

    tasks_by_bundle = {}  # Track tasks by bundle to merge info
    untrusted_tasks = {}  # Track untrusted tasks: task_name -> required_digest
    unsupported_recommendations = {}  # task_name -> recommended_version

    # Process each component
    for component in ec_data.get('components', []):
        # Process violations for unsupported and untrusted tasks
        for violation in component.get('violations', []):
            metadata = violation.get('metadata', {})
            code = metadata.get('code', '')

            if code == 'tasks.unsupported':
                msg = violation.get('msg', '')
                # Parse: "Task X is used by... use the 'Y' task (version Z)"
                version_match = re.search(r"use the '([^']+)' task \(version ([^)]+)\)", msg)
                if version_match:
                    task_name = version_match.group(1)
                    version = version_match.group(2)
                    unsupported_recommendations[task_name] = version
                    logger.debug(f"Found unsupported task recommendation: {task_name} -> {version}")

            elif code == 'trusted_task.trusted':
                msg = violation.get('msg', '')
                # Parse: "Untrusted version of PipelineTask \"X\" (Task \"X\")... upgrade to: sha256:..."
                untrusted_match = re.search(
                    r'Untrusted version of PipelineTask "([^"]+)".*'
                    r'Please upgrade the task version to: (sha256:[a-f0-9]+)',
                    msg
                )
                if untrusted_match:
                    task_name = untrusted_match.group(1)
                    required_digest = untrusted_match.group(2)
                    untrusted_tasks[task_name] = required_digest
                    logger.info(f"Found untrusted task: {task_name} -> {required_digest[:20]}...")

        # Process warnings for outdated tasks
        for warning in component.get('warnings', []):
            metadata = warning.get('metadata', {})
            if metadata.get('code') == 'trusted_task.current':
                msg = warning.get('msg', '')
                # Parse: "A newer version of task X exists... current bundle is Y... latest bundle ref is Z"
                task_match = re.search(
                    r'A newer version of task "([^"]+)" exists\. '
                    r'Please update before ([^.]+)\. '
                    r'The current bundle is "([^"]+)" and the latest bundle ref is "([^"]+)"',
                    msg
                )
                if task_match:
                    friendly_name = task_match.group(1)
                    expiry_date = task_match.group(2)
                    current_bundle = task_match.group(3)
                    latest_digest = task_match.group(4)

                    # Extract task name and version from bundle URI
                    # Format: oci://quay.io/konflux-ci/tekton-catalog/task-{name}:{version}@{digest}
                    bundle_match = re.search(r'task-([^:]+):([^@]+)@(sha256:[a-f0-9]+)', current_bundle)
                    if bundle_match and current_bundle not in tasks_by_bundle:
                        task_name = bundle_match.group(1)
                        task_data = {
                            'friendly_name': friendly_name,
                            'task_name': task_name,
                            'current_version': bundle_match.group(2),
                            'current_digest': bundle_match.group(3),
                            'latest_digest': latest_digest,
                            'expiry_date': expiry_date,
                            'current_bundle': current_bundle,
                            'recommended_version': unsupported_recommendations.get(task_name),
                        }
                        tasks_by_bundle[current_bundle] = task_data
                        logger.debug(f"Found outdated task: {friendly_name} -> {latest_digest[:20]}...")

                        if task_data['recommended_version']:
                            logger.info(f"Task {friendly_name}: EC recommends version {task_data['recommended_version']}")

    # Add untrusted tasks that weren't already found via trusted_task.current warnings
    for task_name, required_digest in untrusted_tasks.items():
        # Check if this task is already tracked (might have been found via warning too)
        already_tracked = any(
            t['task_name'] == task_name for t in tasks_by_bundle.values()
        )
        if not already_tracked:
            # For untrusted tasks, we don't have the current bundle info from the EC log
            # We mark them specially so process_task knows to handle them differently
            task_data = {
                'friendly_name': task_name,
                'task_name': task_name,
                'current_version': 'unknown',  # Will need to look up from pipeline
                'current_digest': 'unknown',
                'latest_digest': required_digest,
                'expiry_date': 'now',  # Untrusted means it's already blocking
                'current_bundle': f'untrusted:{task_name}',
                'recommended_version': None,
                'is_untrusted': True,  # Flag to indicate this is an untrusted task violation
            }
            tasks_by_bundle[f'untrusted:{task_name}'] = task_data
            logger.info(f"Added untrusted task: {task_name} (needs digest {required_digest[:20]}...)")

    tasks = list(tasks_by_bundle.values())
    logger.info(f"Found {len(tasks)} tasks needing updates in log")
    return tasks


def print_summary(results: List[dict]):
    """Print a human-readable summary of the results."""
    print("\n## 🔄 Konflux Tekton Tasks Version Lookup\n")

    # Separate untrusted tasks (blocking violations) from regular updates
    untrusted_tasks = [t for t in results if t.get('is_untrusted')]
    regular_results = [t for t in results if not t.get('is_untrusted')]

    version_bumps = [t for t in regular_results if t.get('is_version_bump')]
    digest_updates = [t for t in regular_results if not t.get('is_version_bump') and not t.get('error')]
    errors = [t for t in results if t.get('error')]
    warnings = [t for t in results if t.get('warning')]

    print("### Summary")
    print(f"- Total tasks: {len(results)}")
    print(f"- **Untrusted tasks (BLOCKING)**: {len(untrusted_tasks)}")
    print(f"- Version bumps: {len(version_bumps)}")
    print(f"- Digest updates: {len(digest_updates)}")
    print(f"- Warnings: {len(warnings)}")
    print(f"- Errors: {len(errors)}")
    print()

    # Show untrusted tasks first - these are blocking!
    if untrusted_tasks:
        print("### 🚨 Untrusted Tasks (BLOCKING - must fix)")
        for task in untrusted_tasks:
            print(f"- **{task['task_name']}**: needs digest {task['latest_digest'][:20]}... (version {task.get('latest_version', 'unknown')})")
        print()

    if version_bumps:
        print("### Version Bumps (require migration notes check)")
        for task in version_bumps:
            source = task.get('version_source', 'unknown')
            source_info = f" (via {source})" if source != 'digest_match' else ""
            print(f"- ⬆️  **{task['task_name']}**: {task['current_version']} → {task['latest_version']}{source_info}")
        print()

    if digest_updates:
        print("### Digest Updates (same version, new digest)")
        for task in digest_updates:
            print(f"- 🔄 {task['task_name']}: {task['current_version']}@...{task['current_digest'][-12:]} → @...{task['latest_digest'][-12:]}")
        print()

    if warnings:
        print("### ⚠️  Warnings")
        for task in warnings:
            print(f"- **{task['task_name']}**: {task['warning']}")
        print()

    if errors:
        print("### ❌ Errors")
        for task in errors:
            print(f"- **{task['task_name']}**: {task.get('error', 'Unknown error')}")
        print()

    # Print update commands
    print("### Update Mapping")
    print("```")
    for task in results:
        if not task.get('error'):
            old_ref = f"task-{task['task_name']}:{task['current_version']}@{task['current_digest']}"
            new_ref = f"task-{task['task_name']}:{task['latest_version']}@{task['latest_digest']}"
            print(f"{old_ref}")
            print(f"  → {new_ref}")
            print()
    print("```")


async def main():
    parser = argparse.ArgumentParser(
        description='Lookup Konflux Tekton task versions from enterprise contract logs'
    )
    parser.add_argument(
        'log_file',
        help='Path to the enterprise contract verification log file'
    )
    parser.add_argument(
        '--output', '-o',
        choices=['json', 'summary'],
        default='json',
        help='Output format (default: json)'
    )
    parser.add_argument(
        '--verbose', '-v',
        action='store_true',
        help='Enable info-level logging'
    )
    parser.add_argument(
        '--debug', '-d',
        action='store_true',
        help='Enable debug-level logging (includes HTTP requests)'
    )

    args = parser.parse_args()

    # Setup logging
    setup_logging(verbose=args.verbose, debug=args.debug)

    if not HAS_AIOHTTP:
        print("Error: aiohttp is required. Install with: pip install aiohttp", file=sys.stderr)
        sys.exit(1)

    # Parse the log file
    print(f"Parsing log file: {args.log_file}", file=sys.stderr)
    tasks = parse_ec_log(args.log_file)

    if not tasks:
        print("No outdated tasks found in log file", file=sys.stderr)
        sys.exit(0)

    print(f"Found {len(tasks)} outdated tasks, looking up versions...", file=sys.stderr)

    # Get auth and show source
    auth_token, auth_source = get_registry_auth()
    print(f"Auth: {auth_source}", file=sys.stderr)

    # Process all tasks
    lookup = KonfluxTaskLookup(auth_token=auth_token)
    results = await lookup.process_all_tasks(tasks)

    # Output results
    if args.output == 'json':
        print(json.dumps(results, indent=2))
    else:
        print_summary(results)


if __name__ == '__main__':
    asyncio.run(main())
