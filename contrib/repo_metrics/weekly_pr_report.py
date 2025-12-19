#!/usr/bin/env python3
"""
Weekly PR Report Generator for HyperShift
Optimized version with parallel API calls and batch processing
"""

import asyncio
import json
import os
import re
import sys
from datetime import datetime, timedelta
from typing import Dict, List, Optional, Set
import subprocess

# API pagination and limit constants
GITHUB_CONTRIBUTORS_PER_PAGE = 100
GITHUB_PR_SEARCH_LIMIT = 100
GITHUB_REVIEWS_LIMIT = 50
GITHUB_LABELS_LIMIT = 20
GITHUB_TIMELINE_ITEMS_LIMIT = 100

# Default report period
DEFAULT_DAYS_AGO = 7

# Try to import aiohttp, fall back to requests if not available
try:
    import aiohttp
    HAS_AIOHTTP = True
except ImportError:
    import requests
    HAS_AIOHTTP = False
    print("Warning: aiohttp not available, using synchronous requests (slower)")


class PRReportGenerator:
    def __init__(self, since_date: str):
        self.since_date = since_date
        self.github_token = os.getenv('GITHUB_TOKEN') or self._get_gh_token()
        self.jira_url = os.getenv('JIRA_URL', 'https://issues.redhat.com')

        # Data storage
        self.prs: List[Dict] = []
        self.jira_hierarchy: Dict = {}
        self.hypershift_authors: Set[str] = set()

    def _get_gh_token(self) -> str:
        """Get GitHub token from gh CLI"""
        try:
            result = subprocess.run(
                ['gh', 'auth', 'token'],
                capture_output=True,
                text=True,
                check=True
            )
            return result.stdout.strip()
        except Exception as e:
            print(f"Error getting GitHub token: {e}")
            sys.exit(1)

    async def fetch_repository_contributors(self, repo_owner: str, repo_name: str) -> Set[str]:
        """Fetch repository contributors using GitHub REST API"""
        headers = {
            "Authorization": f"Bearer {self.github_token}",
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28"
        }

        contributors = set()
        page = 1

        if HAS_AIOHTTP:
            async with aiohttp.ClientSession() as session:
                while True:
                    url = f"https://api.github.com/repos/{repo_owner}/{repo_name}/contributors?per_page={GITHUB_CONTRIBUTORS_PER_PAGE}&page={page}"
                    async with session.get(url, headers=headers) as response:
                        if response.status != 200:
                            break
                        data = await response.json()

                    if not data:
                        break

                    for contributor in data:
                        contributors.add(contributor['login'])

                    if len(data) < GITHUB_CONTRIBUTORS_PER_PAGE:
                        break

                    page += 1
        else:
            while True:
                url = f"https://api.github.com/repos/{repo_owner}/{repo_name}/contributors?per_page={GITHUB_CONTRIBUTORS_PER_PAGE}&page={page}"
                response = requests.get(url, headers=headers)
                if response.status_code != 200:
                    break
                data = response.json()

                if not data:
                    break

                for contributor in data:
                    contributors.add(contributor['login'])

                if len(data) < GITHUB_CONTRIBUTORS_PER_PAGE:
                    break

                page += 1

        return contributors

    async def fetch_prs_graphql(self, repo_owner: str, repo_name: str) -> List[Dict]:
        """Fetch PRs using GitHub GraphQL API with search query for date filtering"""
        # Use search query to filter by merge date range
        search_query = f"repo:{repo_owner}/{repo_name} is:pr is:merged merged:>={self.since_date}"

        query = f"""
        query($searchQuery: String!, $cursor: String) {{
          search(query: $searchQuery, type: ISSUE, first: {GITHUB_PR_SEARCH_LIMIT}, after: $cursor) {{
            pageInfo {{
              hasNextPage
              endCursor
            }}
            nodes {{
              ... on PullRequest {{
                number
                title
                url
                author {{ login }}
                createdAt
                mergedAt
                isDraft
                body
                labels(first: {GITHUB_LABELS_LIMIT}) {{ nodes {{ name }} }}
                reviews(first: {GITHUB_REVIEWS_LIMIT}) {{
                  nodes {{
                    author {{ login }}
                    state
                    submittedAt
                  }}
                }}
                timelineItems(first: {GITHUB_TIMELINE_ITEMS_LIMIT}, itemTypes: [READY_FOR_REVIEW_EVENT, CONVERT_TO_DRAFT_EVENT]) {{
                  nodes {{
                    __typename
                    ... on ReadyForReviewEvent {{ createdAt }}
                    ... on ConvertToDraftEvent {{ createdAt }}
                  }}
                }}
              }}
            }}
          }}
        }}
        """

        headers = {
            "Authorization": f"Bearer {self.github_token}",
            "Content-Type": "application/json"
        }

        prs = []
        cursor = None

        if HAS_AIOHTTP:
            async with aiohttp.ClientSession() as session:
                while True:
                    variables = {"searchQuery": search_query, "cursor": cursor}
                    async with session.post(
                        'https://api.github.com/graphql',
                        json={"query": query, "variables": variables},
                        headers=headers
                    ) as response:
                        data = await response.json()

                    if 'data' not in data or not data['data']['search']:
                        break

                    for pr in data['data']['search']['nodes']:
                        if pr:  # Skip null entries
                            prs.append(self._process_pr_data(pr, f"{repo_owner}/{repo_name}"))

                    page_info = data['data']['search']['pageInfo']
                    if not page_info['hasNextPage']:
                        break
                    cursor = page_info['endCursor']
        else:
            while True:
                variables = {"searchQuery": search_query, "cursor": cursor}
                response = requests.post(
                    'https://api.github.com/graphql',
                    json={"query": query, "variables": variables},
                    headers=headers
                )
                data = response.json()

                if 'data' not in data or not data['data']['search']:
                    break

                for pr in data['data']['search']['nodes']:
                    if pr:  # Skip null entries
                        prs.append(self._process_pr_data(pr, f"{repo_owner}/{repo_name}"))

                page_info = data['data']['search']['pageInfo']
                if not page_info['hasNextPage']:
                    break
                cursor = page_info['endCursor']

        return prs

    def _process_pr_data(self, pr: Dict, repo: str) -> Dict:
        """Process PR data from GraphQL response"""
        # Extract reviewers and approvers
        reviewers = set()
        approvers = set()

        if pr.get('reviews', {}).get('nodes'):
            for review in pr['reviews']['nodes']:
                if review['author']:
                    login = review['author']['login']
                    reviewers.add(login)
                    if review['state'] == 'APPROVED':
                        approvers.add(login)

        # Calculate timeline
        created_at = datetime.fromisoformat(pr['createdAt'].replace('Z', '+00:00'))
        merged_at = datetime.fromisoformat(pr['mergedAt'].replace('Z', '+00:00'))

        # Find when it became ready
        ready_at = created_at
        was_draft = pr['isDraft']

        if pr.get('timelineItems', {}).get('nodes'):
            for item in pr['timelineItems']['nodes']:
                if item['__typename'] == 'ReadyForReviewEvent':
                    ready_at = datetime.fromisoformat(item['createdAt'].replace('Z', '+00:00'))
                    was_draft = True
                    break

        # Calculate hours
        draft_to_ready_hours = None
        if was_draft and ready_at > created_at:
            draft_to_ready_hours = (ready_at - created_at).total_seconds() / 3600

        ready_to_merge_hours = (merged_at - ready_at).total_seconds() / 3600

        # Extract Jira tickets with word boundaries for accurate matching
        text = f"{pr['title']}\n{pr['body'] or ''}"
        jira_tickets = list(set(re.findall(
            r'\b(?:OCPBUGS|CNTRLPLANE|OCPSTRAT|RFE|HOSTEDCP)-\d+\b',
            text
        )))

        # Extract labels
        labels = [label['name'] for label in pr.get('labels', {}).get('nodes', [])]

        return {
            'repo': repo,
            'number': pr['number'],
            'title': pr['title'],
            'url': pr['url'],
            'author': pr['author']['login'] if pr['author'] else 'ghost',
            'createdAt': pr['createdAt'],
            'mergedAt': pr['mergedAt'],
            'readyAt': ready_at.isoformat(),
            'wasDraft': was_draft,
            'draftToReadyHours': draft_to_ready_hours,
            'readyToMergeHours': ready_to_merge_hours,
            'reviewers': sorted(list(reviewers)),
            'approvers': sorted(list(approvers)),
            'jiraTickets': list(set(jira_tickets)),
            'labels': labels,
            'body': pr['body']
        }

    async def fetch_all_prs(self):
        """Fetch PRs from all repositories in parallel"""
        print("Fetching HyperShift contributors...")

        # Get HyperShift contributors first
        self.hypershift_authors = await self.fetch_repository_contributors('openshift', 'hypershift')
        print(f"Found {len(self.hypershift_authors)} HyperShift contributors")

        print("Fetching PRs from repositories...")

        if HAS_AIOHTTP:
            tasks = [
                self.fetch_prs_graphql('openshift', 'hypershift'),
                self.fetch_prs_graphql('openshift-eng', 'ai-helpers'),
                # Note: openshift/release would need separate handling due to size
            ]
            results = await asyncio.gather(*tasks)
        else:
            # Fallback to sequential
            results = [
                await self.fetch_prs_graphql('openshift', 'hypershift'),
                await self.fetch_prs_graphql('openshift-eng', 'ai-helpers'),
            ]

        # Combine and filter
        hypershift_prs = results[0]
        ai_helpers_prs = results[1] if len(results) > 1 else []

        # Filter ai-helpers PRs to only HyperShift contributors
        filtered_ai_helpers = [
            pr for pr in ai_helpers_prs
            if pr['author'] in self.hypershift_authors
        ]

        self.prs = hypershift_prs + filtered_ai_helpers
        print(f"Found {len(self.prs)} PRs ({len(hypershift_prs)} hypershift, {len(filtered_ai_helpers)} ai-helpers)")

    async def load_jira_hierarchy_cache(self):
        """Load Jira hierarchy from cache file if available.

        Note: This does not fetch from Jira directly. The cache is populated
        by the weekly-pr-report.md command which uses MCP Jira tools.
        """
        # Extract all unique Jira tickets for logging
        all_tickets = set()
        for pr in self.prs:
            all_tickets.update(pr['jiraTickets'])

        print(f"Found {len(all_tickets)} unique Jira tickets")

        # Load existing hierarchy if available
        try:
            with open('/tmp/jira_hierarchy.json', 'r') as f:
                self.jira_hierarchy = json.load(f)
            print(f"Loaded Jira hierarchy cache with {len(self.jira_hierarchy)} entries")
        except FileNotFoundError:
            print("Warning: Jira hierarchy cache not found at /tmp/jira_hierarchy.json")
            print("Run the /pr-report command to populate Jira data via MCP tools")
            self.jira_hierarchy = {}

    def generate_report(self, output_path: str):
        """Generate markdown report"""
        print("Generating report...")

        with open(output_path, 'w') as f:
            # Header
            f.write(f"# Weekly PR Report: openshift/hypershift\n")
            f.write(f"**Period:** {self.since_date} to {datetime.now().strftime('%Y-%m-%d')}\n")

            # Count by repo
            hypershift_count = len([pr for pr in self.prs if pr['repo'] == 'openshift/hypershift'])
            ai_helpers_count = len([pr for pr in self.prs if pr['repo'] == 'openshift-eng/ai-helpers'])

            f.write(f"**Total PRs:** {len(self.prs)} ({hypershift_count} hypershift, {ai_helpers_count} ai-helpers, 0 release)\n\n")
            f.write("---\n\n")

            # Summary Statistics
            f.write("## Summary Statistics\n\n")
            f.write("### Repository Breakdown\n")
            f.write(f"- **openshift/hypershift:** {hypershift_count} PRs\n")
            f.write(f"- **openshift-eng/ai-helpers:** {ai_helpers_count} PRs\n")
            f.write(f"- **openshift/release:** 0 PRs\n\n")

            # Group by OCPSTRAT
            f.write("### Epic/Feature Groupings\n\n")

            # Build OCPSTRAT groups
            ocpstrat_groups = {}
            ungrouped_prs = []

            for pr in self.prs:
                grouped = False
                for ticket in pr['jiraTickets']:
                    if ticket in self.jira_hierarchy:
                        info = self.jira_hierarchy[ticket]
                        if info.get('ocpstrat'):
                            ocpstrat_key = info['ocpstrat']
                            if ocpstrat_key not in ocpstrat_groups:
                                ocpstrat_groups[ocpstrat_key] = {
                                    'summary': info.get('ocpstratSummary', 'N/A'),
                                    'prs': []
                                }
                            ocpstrat_groups[ocpstrat_key]['prs'].append(pr)
                            grouped = True
                            break

                if not grouped:
                    ungrouped_prs.append(pr)

            # Write OCPSTRAT groups (PRs sorted by title within each group)
            for ocpstrat_key in sorted(ocpstrat_groups.keys()):
                group = ocpstrat_groups[ocpstrat_key]
                f.write(f"#### [{ocpstrat_key}]({self.jira_url}/browse/{ocpstrat_key}): {group['summary']}\n")
                for pr in sorted(group['prs'], key=lambda p: p['title'].lower()):
                    f.write(f"- PR [#{pr['number']}]({pr['url']}) - {self._linkify_jira_tickets(pr['title'])}\n")
                f.write("\n")

            # Write ungrouped PRs (sorted by title)
            if ungrouped_prs:
                f.write("#### PRs without OCPSTRAT linkage\n")
                for pr in sorted(ungrouped_prs, key=lambda p: p['title'].lower()):
                    f.write(f"- PR [#{pr['number']}]({pr['url']}) ({pr['repo'].split('/')[1]}) - {self._linkify_jira_tickets(pr['title'])}\n")
                f.write("\n")

            # Timing metrics
            draft_times = [pr['draftToReadyHours'] for pr in self.prs if pr['draftToReadyHours'] is not None]
            merge_times = [pr['readyToMergeHours'] for pr in self.prs if pr['readyToMergeHours'] is not None]

            f.write("### Timing Metrics\n")
            if draft_times:
                f.write(f"- Draft→Ready: Average {sum(draft_times)/len(draft_times):.1f} hours, Median {sorted(draft_times)[len(draft_times)//2]:.1f} hours\n")
            if merge_times:
                f.write(f"- Ready→Merge: Average {sum(merge_times)/len(merge_times):.1f} hours, Median {sorted(merge_times)[len(merge_times)//2]:.1f} hours\n")

            # Fastest/slowest
            if merge_times:
                fastest_pr = min(self.prs, key=lambda pr: pr['readyToMergeHours'] or float('inf'))
                slowest_pr = max(self.prs, key=lambda pr: pr['readyToMergeHours'] or 0)
                f.write(f"- Fastest merge: PR [#{fastest_pr['number']}]({fastest_pr['url']}) ({fastest_pr['readyToMergeHours']:.1f} hours)\n")
                f.write(f"- Slowest merge: PR [#{slowest_pr['number']}]({slowest_pr['url']}) ({slowest_pr['readyToMergeHours']:.1f} hours)\n\n")

            # Review activity
            all_reviewers = {}
            for pr in self.prs:
                for reviewer in pr['reviewers']:
                    all_reviewers[reviewer] = all_reviewers.get(reviewer, 0) + 1

            f.write("### Review Activity\n")
            f.write(f"- Total unique reviewers: {len(all_reviewers)}\n")
            f.write("- Most active reviewers:\n")
            for reviewer, count in sorted(all_reviewers.items(), key=lambda x: x[1], reverse=True)[:5]:
                bot_marker = " (bot)" if reviewer.lower() in ['coderabbitai', 'dependabot', 'renovate'] else ""
                f.write(f"  - @{reviewer}: {count} PRs{bot_marker}\n")
            f.write("\n")

            # Busiest merge days
            merge_days = {}
            for pr in self.prs:
                day = pr['mergedAt'].split('T')[0]
                merge_days[day] = merge_days.get(day, 0) + 1

            f.write("### Busiest Merge Days\n")
            for day, count in sorted(merge_days.items(), key=lambda x: x[1], reverse=True)[:5]:
                f.write(f"- {day}: {count} PRs\n")
            f.write("\n")

            # Detailed PR list
            f.write("---\n\n## Detailed PR List\n\n")

            # Sort by merge date (newest first)
            sorted_prs = sorted(self.prs, key=lambda pr: pr['mergedAt'], reverse=True)

            for pr in sorted_prs:
                f.write(f"### PR [#{pr['number']}]({pr['url']}): {self._linkify_jira_tickets(pr['title'])}\n")
                f.write(f"**Repository:** {pr['repo']}  \n")
                f.write(f"**Author:** @{pr['author']}  \n")
                f.write(f"**Merged:** {pr['mergedAt']}\n\n")

                # Topic (first line or two of body)
                if pr['body']:
                    body_lines = pr['body'].strip().split('\n')
                    topic = ' '.join(body_lines[:2])[:200]
                    f.write(f"**Topic:** {topic}...\n\n")

                # Jira hierarchy
                if pr['jiraTickets']:
                    f.write("**Jira Hierarchy:**\n")
                    for ticket in pr['jiraTickets']:
                        if ticket in self.jira_hierarchy:
                            info = self.jira_hierarchy[ticket]
                            f.write(f"- Ticket: [{ticket}]({self.jira_url}/browse/{ticket}) - \"{info.get('summary', 'N/A')}\"\n")
                            if info.get('epic'):
                                f.write(f"  - Epic: [{info['epic']}]({self.jira_url}/browse/{info['epic']}) - \"{info.get('epicSummary', 'N/A')}\"\n")
                            if info.get('ocpstrat'):
                                f.write(f"  - OCPSTRAT: [{info['ocpstrat']}]({self.jira_url}/browse/{info['ocpstrat']}) - \"{info.get('ocpstratSummary', 'N/A')}\"\n")
                        else:
                            f.write(f"- Ticket: [{ticket}]({self.jira_url}/browse/{ticket}) (hierarchy not available)\n")
                else:
                    f.write("**Jira:** No Jira linkage\n")
                f.write("\n")

                # Reviewers and approvers
                if pr['reviewers']:
                    f.write(f"**Reviewers:** {', '.join('@' + r for r in pr['reviewers'])}  \n")
                if pr['approvers']:
                    f.write(f"**Approvers:** {', '.join('@' + a for a in pr['approvers'])}  \n")
                f.write("\n")

                # Timeline
                f.write("**Timeline:**\n")
                f.write(f"- Created: {pr['createdAt']}\n")
                if pr['wasDraft'] and pr['draftToReadyHours']:
                    f.write(f"- Ready: {pr['readyAt']} (Draft→Ready: {pr['draftToReadyHours']:.1f} hours)\n")
                else:
                    f.write(f"- Ready: Created ready\n")
                f.write(f"- Merged: {pr['mergedAt']} (Ready→Merge: {pr['readyToMergeHours']:.1f} hours)\n\n")

                # OCPSTRAT Impact (generate based on title and labels)
                impact = self._generate_impact_statement(pr)
                f.write(f"**OCPSTRAT Impact:** {impact}\n\n")

                # Labels
                if pr['labels']:
                    f.write(f"**Labels:** {', '.join(pr['labels'])}\n")

                f.write("\n---\n\n")

        print(f"Report written to {output_path}")

    def _generate_impact_statement(self, pr: Dict) -> str:
        """Generate an OCPSTRAT impact statement based on PR data and Jira info"""
        title = pr['title'].lower()
        labels = [label.lower() for label in pr['labels']]

        # Try to use Jira ticket description for better context
        if pr['jiraTickets']:
            for ticket in pr['jiraTickets']:
                if ticket in self.jira_hierarchy:
                    jira_info = self.jira_hierarchy[ticket]

                    # Use Jira summary and description for richer impact statement
                    summary = jira_info.get('summary', '')
                    description = jira_info.get('description', '')

                    # If we have an OCPSTRAT parent, use its context
                    if jira_info.get('ocpstrat'):
                        ocpstrat_summary = jira_info.get('ocpstratSummary', '')
                        if ocpstrat_summary:
                            # Extract key goal from OCPSTRAT summary
                            return f"Advances {ocpstrat_summary}: {summary}"

                    # Otherwise use the ticket summary
                    if summary:
                        return summary

        # Fallback to label-based heuristics
        if 'critical' in labels or 'severity/critical' in labels:
            return f"Critical fix: {pr['title']}"
        elif 'bugfix' in labels or 'bug' in labels:
            return f"Resolved bug affecting {self._extract_component(pr)}"
        elif 'enhancement' in labels or 'feature' in labels:
            return f"Enhanced {self._extract_component(pr)} functionality"
        elif 'backport' in labels:
            return f"Backported critical fix for {self._extract_version(pr)}"
        else:
            return f"Improved {self._extract_component(pr)}"

    def _extract_component(self, pr: Dict) -> str:
        """Extract component from PR title or labels"""
        title = pr['title'].lower()

        # Common patterns
        if 'aws' in title: return 'AWS platform support'
        if 'azure' in title or 'aro' in title: return 'Azure platform support'
        if 'gcp' in title: return 'GCP platform support'
        if 'nodepool' in title: return 'NodePool management'
        if 'hosted' in title or 'hcp' in title: return 'hosted control plane'
        if 'cli' in title: return 'CLI tooling'
        if 'operator' in title: return 'operator functionality'
        if 'test' in title: return 'testing infrastructure'

        return 'core functionality'

    def _extract_version(self, pr: Dict) -> str:
        """Extract version from PR labels"""
        for label in pr['labels']:
            if 'backport' in label.lower():
                # Extract version like "4.20" from "backport-4.20"
                match = re.search(r'(\d+\.\d+)', label)
                if match:
                    return f"OCP {match.group(1)}"
        return 'earlier release'

    def _linkify_jira_tickets(self, text: str) -> str:
        """Convert Jira ticket IDs in text to markdown links"""
        def replace_ticket(match):
            ticket = match.group(0)
            return f"[{ticket}]({self.jira_url}/browse/{ticket})"

        return re.sub(
            r'(?:OCPBUGS|CNTRLPLANE|OCPSTRAT|RFE|HOSTEDCP)-\d+',
            replace_ticket,
            text
        )

    def save_raw_data(self, output_path: str):
        """Save raw PR data to JSON"""
        with open(output_path, 'w') as f:
            json.dump(self.prs, f, indent=2)
        print(f"Raw data saved to {output_path}")


async def main():
    import time
    start_time = time.time()

    if len(sys.argv) > 1:
        since_date = sys.argv[1]
    else:
        since_date = (datetime.now() - timedelta(days=DEFAULT_DAYS_AGO)).strftime('%Y-%m-%d')

    print(f"Generating PR report since: {since_date}")
    print(f"Using {'async (aiohttp)' if HAS_AIOHTTP else 'sync (requests)'} mode\n")

    generator = PRReportGenerator(since_date)

    # Fetch all data in parallel
    await generator.fetch_all_prs()
    await generator.load_jira_hierarchy_cache()

    # Generate outputs
    generator.generate_report('/tmp/weekly_pr_report_fast.md')
    generator.save_raw_data('/tmp/hypershift_pr_details_fast.json')

    elapsed = time.time() - start_time
    print(f"\nDone in {elapsed:.2f} seconds!")


if __name__ == '__main__':
    if HAS_AIOHTTP:
        asyncio.run(main())
    else:
        # Python 3.6 compatibility
        loop = asyncio.get_event_loop()
        loop.run_until_complete(main())
