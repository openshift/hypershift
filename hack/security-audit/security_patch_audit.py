#!/usr/bin/env python3
"""
Security Patch Audit Tool for HyperShift

Validates that a security patching PR actually fixes what the linked Jira ticket claims.
Compares Containerfile base image changes against the Jira ticket's GO-xxxx and RHSA-xxxx
advisories, using the Go vulnerability database and Red Hat Pyxis container catalog.

Usage:
    # Full auto-detect (on a PR branch with JIRA_EMAIL/JIRA_TOKEN set):
    python3 security_patch_audit.py

    # Explicit Jira key:
    python3 security_patch_audit.py --jira OCPBUGS-78544

    # Explicit base ref:
    python3 security_patch_audit.py --jira OCPBUGS-78544 --base-ref origin/main

    # GitHub Actions mode (outputs summary as step output):
    python3 security_patch_audit.py --gh-action
"""

import argparse
import json
import logging
import os
import re
import subprocess
import sys
import time
from dataclasses import dataclass, field
from typing import Optional

try:
    import requests
except ImportError:
    print("ERROR: 'requests' package is required. Install with: pip install requests", file=sys.stderr)
    sys.exit(1)

logging.basicConfig(level=logging.INFO, format="%(levelname)s: %(message)s")
log = logging.getLogger(__name__)

# HTTP request defaults
HTTP_TIMEOUT = 30  # seconds
HTTP_MAX_RETRIES = 3
HTTP_RATE_LIMIT_WAIT = 30  # seconds to wait on 429


def _fetch_json(
    url: str,
    auth: Optional[tuple[str, str]] = None,
    _retry: int = 0,
) -> Optional[dict]:
    """Fetch JSON from a URL with timeout, retry on 429, and exception handling.

    Returns the parsed JSON dict, or None on failure.
    """
    try:
        resp = requests.get(url, auth=auth, timeout=HTTP_TIMEOUT)
    except requests.RequestException as e:
        log.warning("HTTP request failed for %s: %s", url, e)
        return None

    if resp.status_code == 200:
        try:
            return resp.json()
        except (json.JSONDecodeError, ValueError):
            log.warning("Could not parse JSON response from %s", url)
            return None

    if resp.status_code == 429:
        if _retry >= HTTP_MAX_RETRIES:
            log.warning("Rate limited %d times, giving up on %s", HTTP_MAX_RETRIES, url)
            return None
        log.info("Rate limited, waiting %ds before retry (%d/%d)...",
                 HTTP_RATE_LIMIT_WAIT, _retry + 1, HTTP_MAX_RETRIES)
        time.sleep(HTTP_RATE_LIMIT_WAIT)
        return _fetch_json(url, auth=auth, _retry=_retry + 1)

    log.warning("HTTP %d from %s: %s", resp.status_code, url, resp.text[:200])
    return None


# --- Data classes ---

@dataclass
class GoAdvisory:
    go_id: str  # e.g. GO-2026-4340
    summary: str = ""
    affected_packages: list[str] = field(default_factory=list)
    cves: list[str] = field(default_factory=list)
    _ranges: list[list[dict]] = field(default_factory=list)  # SEMVER range events from vuln DB


# --- Jira parsing ---

def parse_advisory_ids(blob: object) -> tuple[list[str], list[str]]:
    """Extract GO-xxxx and RHSA-xxxx IDs from any object by serializing to string and regexing.

    Works with ADF dicts, plain text strings, or any JSON-serializable structure.
    Returns (go_ids, rhsa_ids), deduplicated and order-preserved.
    """
    text = json.dumps(blob) if not isinstance(blob, str) else blob
    go_ids = list(dict.fromkeys(re.findall(r"GO-\d{4}-\d+", text)))
    rhsa_ids = list(dict.fromkeys(re.findall(r"RHSA-\d{4}:\d+", text)))
    return go_ids, rhsa_ids


def detect_jira_keys_from_git(base_ref: str) -> list[str]:
    """Detect all Jira issue keys from the current git branch and commit log.

    Scans:
    1. Current branch name
    2. All commit messages between base_ref and HEAD

    Returns deduplicated list of keys like ['OCPBUGS-78544', 'CNTRLPLANE-1234'].
    """
    jira_pattern = re.compile(r"([A-Z][A-Z0-9]+-\d+)")
    keys: list[str] = []

    # Branch name
    result = subprocess.run(
        ["git", "branch", "--show-current"],
        capture_output=True, text=True,
    )
    if result.returncode == 0:
        keys.extend(jira_pattern.findall(result.stdout.strip()))

    # All commit messages between base_ref and HEAD
    result = subprocess.run(
        ["git", "log", f"{base_ref}..HEAD", "--format=%B"],
        capture_output=True, text=True,
    )
    if result.returncode == 0:
        keys.extend(jira_pattern.findall(result.stdout))

    return list(dict.fromkeys(keys))


def fetch_jira_issue(jira_key: str) -> dict:
    """Fetch a Jira issue using the REST API v3."""
    email = os.environ.get("JIRA_EMAIL")
    token = os.environ.get("JIRA_TOKEN")
    if not email or not token:
        log.error("JIRA_EMAIL and JIRA_TOKEN environment variables are required")
        sys.exit(1)

    url = f"https://redhat.atlassian.net/rest/api/3/issue/{jira_key}"
    data = _fetch_json(url, auth=(email, token))
    if data is None:
        log.error("Failed to fetch Jira issue %s", jira_key)
        sys.exit(1)

    if "errorMessages" in data:
        log.error("Jira API error for %s: %s", jira_key, data["errorMessages"])
        sys.exit(1)
    return data


# --- Go vulnerability database ---

def fetch_go_advisory(go_id: str) -> GoAdvisory:
    """Fetch advisory details from the Go vulnerability database."""
    url = f"https://vuln.go.dev/ID/{go_id}.json"
    advisory = GoAdvisory(go_id=go_id)

    data = _fetch_json(url)
    if data is None:
        log.warning("Could not fetch Go advisory %s", go_id)
        return advisory

    advisory.summary = data.get("summary", "")
    advisory.cves = data.get("aliases", [])

    # The vuln DB uses SEMVER ranges with multiple introduced/fixed pairs.
    # We store all ranges so we can check if a specific version is affected.
    advisory._ranges = []
    for affected in data.get("affected", []):
        pkg = affected.get("package", {}).get("name", "")
        if pkg:
            advisory.affected_packages.append(pkg)
        for r in affected.get("ranges", []):
            events = r.get("events", [])
            advisory._ranges.append(events)

    return advisory


# --- Red Hat Container Catalog (Pyxis) API ---

PYXIS_BASE = "https://catalog.redhat.com/api/containers/v1"


@dataclass
class PyxisVuln:
    """A vulnerability entry from the Pyxis container catalog."""
    rhsa_id: str        # e.g. RHSA-2026:2786
    cve_id: str
    affected_packages: list[dict] = field(default_factory=list)  # [{name, version, arch}]


def pyxis_find_image_id(registry: str, repository: str, tag: str) -> Optional[str]:
    """Find a Pyxis image ID by registry/repository:tag."""
    url = (
        f"{PYXIS_BASE}/repositories/registry/{registry}"
        f"/repository/{repository}/images"
        f"?filter=repositories.tags.name=={tag}&page_size=1"
    )
    data = _fetch_json(url)
    if data is None:
        log.warning("Pyxis image lookup failed for %s/%s:%s", registry, repository, tag)
        return None
    if data.get("data"):
        return data["data"][0]["_id"]
    return None


def pyxis_get_vulnerabilities(image_id: str) -> list[PyxisVuln]:
    """Get all vulnerabilities for a Pyxis image, handling pagination."""
    vulns = []
    page = 0
    page_size = 100

    while True:
        url = f"{PYXIS_BASE}/images/id/{image_id}/vulnerabilities?page_size={page_size}&page={page}"
        data = _fetch_json(url)
        if data is None:
            log.warning("Pyxis vulnerabilities lookup failed for image %s", image_id)
            break

        for v in data.get("data", []):
            advisory_id = v.get("advisory_id", "")
            advisory_type = v.get("advisory_type", "")
            rhsa_id = f"{advisory_type}-{advisory_id}" if advisory_type else advisory_id
            vulns.append(PyxisVuln(
                rhsa_id=rhsa_id,
                cve_id=v.get("cve_id", ""),
                affected_packages=v.get("affected_packages", []),
            ))

        total = data.get("total", 0)
        if (page + 1) * page_size >= total:
            break
        page += 1

    return vulns


def _parse_base_image_refs(content: str) -> dict[str, tuple[str, str]]:
    """Parse FROM lines in a Containerfile to extract registry.access.redhat.com image refs.

    Returns dict like {"ubi-minimal": ("ubi9/ubi-minimal", "9.7-1773204619")}.
    The key is the last path component, the value is (full_repo_path, tag).
    """
    refs = {}
    for line in content.split("\n"):
        match = re.search(r"registry\.access\.redhat\.com/([\w/-]+):(\S+)", line)
        if match:
            repo_path = match.group(1)
            tag = match.group(2)
            key = repo_path.split("/")[-1]
            refs[key] = (repo_path, tag)
    return refs


@dataclass
class BaseImageChange:
    """A base image that changed between the old and new Containerfiles."""
    key: str            # e.g. "ubi-minimal"
    repo_path: str      # e.g. "ubi9/ubi-minimal"
    old_tag: str        # e.g. "9.6-1760515502"
    new_tag: str        # e.g. "9.7-1773204619"
    registry: str = "registry.access.redhat.com"


def get_base_image_changes(
    containerfiles: list[str],
    base_ref: str = "main",
) -> list[BaseImageChange]:
    """Extract base image changes by comparing Containerfiles against a git base ref.

    Reads each Containerfile from the working tree (new) and from the git base ref (old).
    Only returns entries where the tag actually changed.
    """
    seen: dict[str, BaseImageChange] = {}

    for containerfile in containerfiles:
        try:
            with open(containerfile) as f:
                new_content = f.read()
        except OSError:
            log.warning("Cannot read %s", containerfile)
            continue

        new_refs = _parse_base_image_refs(new_content)

        result = subprocess.run(
            ["git", "show", f"{base_ref}:{containerfile}"],
            capture_output=True, text=True,
        )
        if result.returncode != 0:
            log.warning("Cannot read %s from %s (new file?)", containerfile, base_ref)
            old_refs: dict[str, tuple[str, str]] = {}
        else:
            old_refs = _parse_base_image_refs(result.stdout)

        for key, (repo_path, new_tag) in new_refs.items():
            old_tag = old_refs.get(key, ("", ""))[1]
            if old_tag != new_tag and key not in seen:
                seen[key] = BaseImageChange(
                    key=key, repo_path=repo_path,
                    old_tag=old_tag, new_tag=new_tag,
                )

    return list(seen.values())


def detect_upstream_remote(repo_slug: str = "openshift/hypershift") -> str:
    """Find the git remote that tracks the upstream repo (e.g. openshift/hypershift).

    Checks all remotes' URLs for the repo slug. Falls back to 'origin'.
    """
    result = subprocess.run(
        ["git", "remote", "-v"],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        return "origin"

    for line in result.stdout.strip().split("\n"):
        parts = line.split()
        if len(parts) >= 2 and repo_slug in parts[1] and "(fetch)" in line:
            return parts[0]

    return "origin"


def detect_base_ref(upstream_remote: str) -> str:
    """Auto-detect the base ref by finding the closest upstream branch ancestor.

    Walks HEAD's history to find which <remote>/* branch is the closest ancestor
    (fewest commits ahead). Falls back to '<remote>/main'.
    """
    fallback = f"{upstream_remote}/main"

    result = subprocess.run(
        ["git", "branch", "-r", "--format=%(refname:short)"],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        log.warning("Could not list remote branches, defaulting to %s", fallback)
        return fallback

    # Only consider branches from the upstream remote
    prefix = f"{upstream_remote}/"
    remote_branches = [
        b.strip() for b in result.stdout.strip().split("\n")
        if b.strip().startswith(prefix) and "HEAD" not in b
    ]
    if not remote_branches:
        return fallback

    best_ref = fallback
    best_distance = float("inf")

    for branch in remote_branches:
        result = subprocess.run(
            ["git", "rev-list", "--count", f"{branch}..HEAD"],
            capture_output=True, text=True,
        )
        if result.returncode != 0:
            continue
        try:
            distance = int(result.stdout.strip())
        except ValueError:
            continue

        if 0 < distance < best_distance:
            best_distance = distance
            best_ref = branch
        elif distance == 0 and best_distance == float("inf"):
            best_ref = branch

    log.info("Auto-detected base ref: %s (%s commits from HEAD)",
             best_ref, best_distance if best_distance != float("inf") else 0)
    return best_ref


def find_containerfiles(repo_root: str = ".") -> list[str]:
    """Find all Containerfiles in the repo root."""
    return sorted(
        os.path.join(repo_root, name) if repo_root != "." else name
        for name in os.listdir(repo_root)
        if name.startswith("Containerfile")
    )


def check_rhsa_fixes_via_pyxis(
    rhsa_ids: list[str],
    base_image_changes: list[BaseImageChange],
) -> list[dict]:
    """Check RHSA fixes by comparing Pyxis vulnerability data between old and new base images.

    For each base image that changed (e.g. ubi-minimal 9.6 -> 9.7), looks up vulnerabilities
    in the old image and verifies they're gone in the new image.
    """
    old_vulns: dict[str, list[PyxisVuln]] = {}  # RHSA -> vulns
    new_vuln_rhsas: set[str] = set()

    for change in base_image_changes:
        if not change.old_tag or not change.new_tag:
            continue

        log.info("Looking up Pyxis vulns for %s/%s (old: %s, new: %s)",
                 change.registry, change.repo_path, change.old_tag, change.new_tag)

        # Get old image vulns
        old_id = pyxis_find_image_id(change.registry, change.repo_path, change.old_tag)
        if old_id:
            for v in pyxis_get_vulnerabilities(old_id):
                old_vulns.setdefault(v.rhsa_id, []).append(v)
        else:
            log.warning("Could not find old image %s:%s in Pyxis", change.repo_path, change.old_tag)

        # Get new image vulns
        new_id = pyxis_find_image_id(change.registry, change.repo_path, change.new_tag)
        if new_id:
            for v in pyxis_get_vulnerabilities(new_id):
                new_vuln_rhsas.add(v.rhsa_id)
        else:
            log.warning("Could not find new image %s:%s in Pyxis", change.repo_path, change.new_tag)

    # Build results for each RHSA from the ticket
    results = []
    for rhsa_id in rhsa_ids:
        vulns = old_vulns.get(rhsa_id, [])

        if vulns:
            # We found this RHSA in the old image — check if it's gone in the new one
            cves = list(dict.fromkeys(v.cve_id for v in vulns if v.cve_id))
            pkgs = []
            for v in vulns:
                for p in v.affected_packages:
                    pkg_str = f"{p.get('name', '?')}-{p.get('version', '?')}"
                    if pkg_str not in pkgs:
                        pkgs.append(pkg_str)

            is_fixed = rhsa_id not in new_vuln_rhsas
            results.append({
                "id": rhsa_id,
                "package": pkgs[0] if pkgs else "",
                "cves": cves,
                "affected_versions": pkgs,
                "resolved": is_fixed,
                "in_old_image": True,
                "in_new_image": not is_fixed,
            })
        else:
            # RHSA not found in old image vulns — might not apply to these base images
            results.append({
                "id": rhsa_id,
                "package": "",
                "cves": [],
                "affected_versions": [],
                "resolved": None,
                "in_old_image": False,
                "in_new_image": rhsa_id in new_vuln_rhsas,
            })

    return results


def parse_go_toolset_tag(tag: str) -> str:
    """Extract Go version from a go-toolset tag like '1.25.7-1773318690'."""
    match = re.match(r"(\d+\.\d+\.\d+)", tag)
    return match.group(1) if match else tag


# --- Comparison and reporting ---

def check_go_fixes(advisories: list[GoAdvisory], go_version: str) -> list[dict]:
    """Check whether the Go version in the image fixes the reported advisories."""
    results = []
    for adv in advisories:
        is_fixed, fixed_in = _check_version_against_ranges(go_version, adv._ranges)

        results.append({
            "id": adv.go_id,
            "summary": adv.summary,
            "cves": adv.cves,
            "affected_packages": adv.affected_packages,
            "fixed_in": fixed_in,
            "image_version": go_version,
            "resolved": is_fixed,
        })
    return results


def _check_version_against_ranges(version: str, ranges: list[list[dict]]) -> tuple[bool, str]:
    """Check if a version is affected according to Go vuln DB SEMVER ranges.

    Ranges are lists of events like:
        [{"introduced": "0"}, {"fixed": "1.24.13"}, {"introduced": "1.25.0-0"}, {"fixed": "1.25.7"}]
    A version is affected if it falls within any introduced..fixed pair.

    Returns (is_fixed, relevant_fixed_version).
    """
    if not version or not ranges:
        return False, ""

    ver = _version_tuple(version)
    if ver is None:
        return False, ""

    best_fix = ""
    for events in ranges:
        # Walk events pairwise: introduced/fixed pairs
        in_range = False
        for event in events:
            if "introduced" in event:
                intro = event["introduced"]
                if intro == "0":
                    in_range = True
                else:
                    intro_t = _version_tuple(intro)
                    in_range = intro_t is not None and ver >= intro_t
            elif "fixed" in event:
                fix = event["fixed"]
                fix_t = _version_tuple(fix)
                if in_range:
                    if fix_t is not None and ver >= fix_t:
                        # Version is past this fix — not affected in this range
                        best_fix = fix
                        in_range = False
                    else:
                        # Version is in the affected range and below the fix
                        return False, fix

        if in_range:
            # Version introduced but no fix yet in this range
            return False, "no fix available"

    return True, best_fix


def _version_tuple(v: str) -> Optional[tuple[int, ...]]:
    """Parse a version string like '1.25.7', 'go1.25.7', or '1.25.0-rc.1' into a comparable tuple."""
    v = re.sub(r"^go", "", v)
    parts = re.split(r"[.\-]", v)
    nums = []
    for p in parts:
        if p.isdigit():
            nums.append(int(p))
        elif p == "rc":
            nums.append(-1)  # rc sorts before release
    return tuple(nums) if nums else None


# --- Output ---

def print_report(
    go_results: list[dict],
    rpm_results: list[dict],
    go_version_old: str,
    go_version_new: str,
    gh_action: bool = False,
):
    """Print the audit report."""
    lines = []
    lines.append("=" * 72)
    lines.append("SECURITY PATCH AUDIT REPORT")
    lines.append("=" * 72)

    if go_version_old or go_version_new:
        lines.append("")
        lines.append(f"Go toolset: {go_version_old} -> {go_version_new}")
        lines.append(f"Go version: {parse_go_toolset_tag(go_version_old)} -> {parse_go_toolset_tag(go_version_new)}")

    if go_results:
        lines.append("")
        lines.append("-" * 72)
        lines.append("GO STDLIB ADVISORIES")
        lines.append("-" * 72)
        all_go_ok = True
        for r in go_results:
            status = "FIXED" if r["resolved"] else "NOT FIXED"
            if not r["resolved"]:
                all_go_ok = False
            icon = "[+]" if r["resolved"] else "[!]"
            lines.append(f"  {icon} {r['id']}: {status}")
            if r["summary"]:
                lines.append(f"      {r['summary']}")
            if r["affected_packages"]:
                lines.append(f"      Packages: {', '.join(r['affected_packages'])}")
            if r["cves"]:
                lines.append(f"      CVEs: {', '.join(r['cves'])}")
            fixed = r['fixed_in'] or 'unknown'
            image = r['image_version'] or 'unknown'
            lines.append(f"      Fixed in: {fixed}  |  Image has: {image}")

        lines.append("")
        lines.append(f"  Go stdlib: {'ALL RESOLVED' if all_go_ok else 'SOME ISSUES REMAIN'}")

    if rpm_results:
        lines.append("")
        lines.append("-" * 72)
        lines.append("RED HAT SECURITY ADVISORIES (RPM via Pyxis)")
        lines.append("-" * 72)
        all_rpm_ok = True
        for r in rpm_results:
            if r["resolved"] is None:
                status = "N/A (not in base image)"
                icon = "[-]"
            elif r["resolved"]:
                status = "FIXED (gone from new image)"
                icon = "[+]"
            else:
                status = "NOT FIXED (still in new image)"
                icon = "[!]"
                all_rpm_ok = False
            lines.append(f"  {icon} {r['id']}: {status}")
            if r.get("package"):
                lines.append(f"      Package: {r['package']}")
            if r.get("cves"):
                lines.append(f"      CVEs: {', '.join(r['cves'][:5])}")
            if r.get("affected_versions"):
                lines.append(f"      Affected: {', '.join(r['affected_versions'][:3])}")

        lines.append("")
        lines.append(f"  RPM fixes: {'ALL RESOLVED' if all_rpm_ok else 'SOME ISSUES REMAIN'}")

    lines.append("")
    lines.append("=" * 72)

    report = "\n".join(lines)
    print(report)

    if gh_action:
        _write_gh_action_output(go_results, rpm_results, report)


def _write_gh_action_output(go_results: list[dict], rpm_results: list[dict], report: str):
    """Write GitHub Actions step outputs and job summary."""
    all_fixed = all(r["resolved"] for r in go_results) and all(
        r["resolved"] is not False for r in rpm_results
    )

    gh_output = os.environ.get("GITHUB_OUTPUT", "")
    if gh_output:
        with open(gh_output, "a") as f:
            f.write(f"all_fixed={'true' if all_fixed else 'false'}\n")

    gh_summary = os.environ.get("GITHUB_STEP_SUMMARY", "")
    if gh_summary:
        with open(gh_summary, "a") as f:
            f.write("```\n")
            f.write(report)
            f.write("\n```\n")

    if not all_fixed:
        log.error("Not all advisories are resolved!")
        sys.exit(1)


# --- Main ---

def main():
    parser = argparse.ArgumentParser(
        description="Audit a security patching PR against its Jira ticket"
    )
    parser.add_argument("--jira", help="Jira ticket key (e.g. OCPBUGS-78544)")
    parser.add_argument("--repo-slug", default="openshift/hypershift",
                        help="Upstream repo slug for remote detection (default: openshift/hypershift)")
    parser.add_argument("--base-ref", help="Git ref for old base image versions (default: auto-detect)")
    parser.add_argument("--containerfiles", nargs="*", help="Containerfiles to check (default: auto-detect)")
    parser.add_argument("--gh-action", action="store_true", help="GitHub Actions output mode")
    parser.add_argument("-v", "--verbose", action="store_true", help="Verbose output")
    args = parser.parse_args()

    if args.verbose:
        logging.getLogger().setLevel(logging.DEBUG)

    # Step 1: Detect base ref (needed for Jira key detection and Containerfile comparison)
    if args.base_ref:
        base_ref = args.base_ref
    else:
        upstream = detect_upstream_remote(args.repo_slug)
        base_ref = detect_base_ref(upstream)

    # Step 2: Get advisory IDs from Jira ticket(s)
    go_ids = []
    rhsa_ids = []

    jira_keys = [args.jira] if args.jira else detect_jira_keys_from_git(base_ref)
    if jira_keys:
        log.info("Jira keys to check: %s", ", ".join(jira_keys))
    else:
        log.error("No Jira keys found (provide --jira or use a branch/commit referencing one)")
        sys.exit(1)

    for jira_key in jira_keys:
        log.info("Fetching Jira issue %s...", jira_key)
        issue = fetch_jira_issue(jira_key)
        description = issue.get("fields", {}).get("description", "")
        go, rhsa = parse_advisory_ids(description)
        go_ids.extend(go)
        rhsa_ids.extend(rhsa)

    # Deduplicate while preserving order
    go_ids = list(dict.fromkeys(go_ids))
    rhsa_ids = list(dict.fromkeys(rhsa_ids))
    log.info("Total: %d Go advisories, %d RHSAs across %d ticket(s)", len(go_ids), len(rhsa_ids), len(jira_keys))

    if not go_ids and not rhsa_ids:
        log.error("No GO-xxxx or RHSA-xxxx identifiers found in Jira ticket(s)")
        sys.exit(1)

    # Step 3: Fetch Go advisory details
    log.info("Fetching Go advisory details...")
    go_advisories = [fetch_go_advisory(gid) for gid in go_ids]

    # Step 4: Detect base image changes from Containerfiles vs base ref
    containerfiles = args.containerfiles or find_containerfiles()
    if not containerfiles:
        log.error("No Containerfiles found")
        sys.exit(1)
    log.info("Checking Containerfiles: %s", ", ".join(containerfiles))

    base_image_changes = get_base_image_changes(containerfiles, base_ref)
    if not base_image_changes:
        log.warning("No base image changes detected (working tree matches %s)", base_ref)

    # Extract Go version from the go-toolset change
    go_version_old = ""
    go_version_new = ""
    for change in base_image_changes:
        if change.key == "go-toolset":
            go_version_old = change.old_tag
            go_version_new = change.new_tag
            break
    go_version_clean = parse_go_toolset_tag(go_version_new) if go_version_new else ""

    # Step 5: Check RHSA fixes via Pyxis (old vs new base image vulnerabilities)
    rpm_results = []
    if rhsa_ids and base_image_changes:
        log.info("Checking RHSA fixes via Red Hat container catalog (Pyxis)...")
        rpm_results = check_rhsa_fixes_via_pyxis(rhsa_ids, base_image_changes)

    # Step 6: Compare Go fixes and report
    go_results = check_go_fixes(go_advisories, go_version_clean)

    print_report(
        go_results, rpm_results,
        go_version_old, go_version_new,
        args.gh_action,
    )


if __name__ == "__main__":
    main()
