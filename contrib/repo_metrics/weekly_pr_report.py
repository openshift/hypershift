#!/usr/bin/env python3
"""
Weekly PR Report Generator for HyperShift
Optimized version with parallel API calls and batch processing
"""

import argparse
import asyncio
import json
import os
import re
import sys
from datetime import datetime, timedelta
from typing import Dict, List, Optional, Set, Tuple
import subprocess

# API pagination and limit constants
GITHUB_CONTRIBUTORS_PER_PAGE = 100
GITHUB_PR_SEARCH_LIMIT = 100
GITHUB_REVIEWS_LIMIT = 50
GITHUB_LABELS_LIMIT = 20
GITHUB_TIMELINE_ITEMS_LIMIT = 100
GITHUB_FILES_LIMIT = 50

# HyperShift-related paths in openshift/release repository
HYPERSHIFT_RELEASE_PATHS = [
    'ci-operator/config/openshift/hypershift/',
    'ci-operator/jobs/openshift/hypershift/',
    'ci-operator/step-registry/hypershift/',
    'clusters/app.ci/openshift/hypershift/',
]

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

# URL encoding for Jira queries
from urllib.parse import urlencode

# Jira rate limiting constants (conservative for Red Hat Jira)
JIRA_REQUEST_DELAY_SECONDS = 0.2  # 200ms between requests
JIRA_MAX_CONCURRENT_REQUESTS = 3  # Max 3 parallel requests
JIRA_BATCH_SIZE = 40  # Max tickets per JQL query


class JiraClient:
    """Client for fetching Jira issues via REST API with batch support and rate limiting."""

    # Fields to fetch from Jira for hierarchy building
    FIELDS = 'summary,description,parent,customfield_12311140,customfield_12313140,issuelinks,labels,priority,status'

    def __init__(self):
        self.base_url = os.getenv('JIRA_URL', 'https://issues.redhat.com')
        self.token = os.getenv('JIRA_TOKEN')
        self.enabled = bool(self.token)
        self.session: Optional["aiohttp.ClientSession"] = None
        self.semaphore = asyncio.Semaphore(JIRA_MAX_CONCURRENT_REQUESTS)
        self.request_count = 0

    def _get_headers(self) -> Dict:
        """Get authentication headers for Jira API."""
        headers = {
            'Accept': 'application/json',
            'Content-Type': 'application/json',
        }
        if self.token:
            headers['Authorization'] = f'Bearer {self.token}'
        return headers

    async def _fetch_json(self, url: str, _retry_count: int = 0) -> Optional[Dict]:
        """Fetch JSON from URL with rate limiting and error handling."""
        MAX_RETRIES = 3
        async with self.semaphore:
            await asyncio.sleep(JIRA_REQUEST_DELAY_SECONDS)
            self.request_count += 1

            if HAS_AIOHTTP and self.session:
                try:
                    async with self.session.get(url, headers=self._get_headers()) as response:
                        if response.status == 200:
                            return await response.json()
                        elif response.status == 401:
                            print("Warning: Jira authentication failed (401). Check JIRA_TOKEN.")
                            return None
                        elif response.status == 404:
                            return None
                        elif response.status == 429:
                            if _retry_count >= MAX_RETRIES:
                                print(f"  Rate limited {MAX_RETRIES} times, giving up on {url}")
                                return None
                            print("  Rate limited! Waiting 30 seconds...")
                            await asyncio.sleep(30)
                            return await self._fetch_json(url, _retry_count + 1)
                        else:
                            text = await response.text()
                            print(f"Warning: Jira API returned {response.status}: {text[:200]}")
                            return None
                except Exception as e:
                    print(f"Error fetching {url}: {e}")
                    return None
            else:
                try:
                    response = requests.get(url, headers=self._get_headers(), timeout=30)
                    if response.status_code == 200:
                        return response.json()
                    elif response.status_code == 401:
                        print("Warning: Jira authentication failed (401). Check JIRA_TOKEN.")
                        return None
                    elif response.status_code == 404:
                        return None
                    elif response.status_code == 429:
                        if _retry_count >= MAX_RETRIES:
                            print(f"  Rate limited {MAX_RETRIES} times, giving up on {url}")
                            return None
                        print("  Rate limited! Waiting 30 seconds...")
                        await asyncio.sleep(30)
                        return await self._fetch_json(url, _retry_count + 1)
                    else:
                        return None
                except Exception as e:
                    print(f"Error fetching {url}: {e}")
                    return None

    async def fetch_issues_batch(self, ticket_keys: List[str]) -> Dict[str, Dict]:
        """Fetch multiple issues using JQL: key in (TICKET1, TICKET2, ...)"""
        if not ticket_keys:
            return {}

        # Build JQL query
        keys_str = ','.join(ticket_keys)
        jql = f'key in ({keys_str})'

        params = {
            'jql': jql,
            'fields': self.FIELDS,
            'maxResults': len(ticket_keys) + 10,
        }
        url = f'{self.base_url}/rest/api/2/search?{urlencode(params)}'

        results = {}
        data = await self._fetch_json(url)

        if data:
            for issue in data.get('issues', []):
                results[issue['key']] = self._parse_issue(issue)

        return results

    def _parse_issue(self, issue: Dict) -> Dict:
        """Parse Jira issue response into simplified dict."""
        fields = issue.get('fields', {})

        # Extract epic link (customfield_12311140)
        epic_link_field = fields.get('customfield_12311140')
        epic_link = None
        if isinstance(epic_link_field, dict):
            epic_link = epic_link_field.get('value')
        elif isinstance(epic_link_field, str):
            epic_link = epic_link_field

        # Extract OCPSTRAT parent (customfield_12313140)
        ocpstrat_field = fields.get('customfield_12313140')
        ocpstrat = None
        if isinstance(ocpstrat_field, dict):
            ocpstrat = ocpstrat_field.get('value')
        elif isinstance(ocpstrat_field, str):
            ocpstrat = ocpstrat_field

        # Get description (truncate for storage)
        description = fields.get('description') or ''
        if len(description) > 2000:
            description = description[:2000] + '...'

        # Extract labels
        labels = []
        for label in fields.get('labels', []):
            if isinstance(label, dict):
                labels.append(label.get('name', ''))
            else:
                labels.append(label)

        # Extract priority and status
        priority = fields.get('priority', {})
        priority_name = priority.get('name') if isinstance(priority, dict) else None

        status = fields.get('status', {})
        status_name = status.get('name') if isinstance(status, dict) else None

        return {
            'key': issue['key'],
            'summary': fields.get('summary', ''),
            'description': description,
            'epicLink': epic_link,
            'ocpstratParent': ocpstrat,
            'labels': labels,
            'priority': priority_name,
            'status': status_name,
        }

    async def fetch_all_tickets(self, tickets: Set[str]) -> Dict[str, Dict]:
        """Fetch all tickets in batches with rate limiting."""
        ticket_list = list(tickets)
        all_results = {}

        for i in range(0, len(ticket_list), JIRA_BATCH_SIZE):
            batch = ticket_list[i:i + JIRA_BATCH_SIZE]
            batch_num = i // JIRA_BATCH_SIZE + 1
            total_batches = (len(ticket_list) + JIRA_BATCH_SIZE - 1) // JIRA_BATCH_SIZE
            print(f"  Fetching Jira batch {batch_num}/{total_batches} ({len(batch)} tickets)...")
            results = await self.fetch_issues_batch(batch)
            all_results.update(results)

        return all_results

    async def build_hierarchy(self, tickets: Set[str]) -> Dict[str, Dict]:
        """Build complete hierarchy including epics and OCPSTRAT parents."""
        if not tickets:
            return {}

        print(f"Fetching Jira data via REST API ({len(tickets)} tickets)...")

        if HAS_AIOHTTP:
            async with aiohttp.ClientSession() as session:
                self.session = session
                return await self._build_hierarchy_internal(tickets)
        else:
            return await self._build_hierarchy_internal(tickets)

    async def _build_hierarchy_internal(self, tickets: Set[str]) -> Dict[str, Dict]:
        """Internal hierarchy builder."""
        # Step 1: Fetch all direct tickets
        ticket_data = await self.fetch_all_tickets(tickets)

        # Step 2: Extract epic links and OCPSTRAT refs that need to be fetched
        epics_to_fetch = set()
        ocpstrats_to_fetch = set()

        for ticket_key, data in ticket_data.items():
            epic_link = data.get('epicLink')
            if epic_link and epic_link not in ticket_data:
                epics_to_fetch.add(epic_link)

            ocpstrat = data.get('ocpstratParent')
            if ocpstrat and ocpstrat not in ticket_data:
                ocpstrats_to_fetch.add(ocpstrat)

        # Step 3: Fetch epics
        if epics_to_fetch:
            print(f"  Fetching {len(epics_to_fetch)} epics...")
            epic_data = await self.fetch_all_tickets(epics_to_fetch)
            ticket_data.update(epic_data)

            # Check if epics have OCPSTRAT parents we need
            for epic_key, data in epic_data.items():
                ocpstrat = data.get('ocpstratParent')
                if ocpstrat and ocpstrat not in ticket_data and ocpstrat not in ocpstrats_to_fetch:
                    ocpstrats_to_fetch.add(ocpstrat)

        # Step 4: Fetch OCPSTRAT issues
        if ocpstrats_to_fetch:
            print(f"  Fetching {len(ocpstrats_to_fetch)} OCPSTRAT issues...")
            ocpstrat_data = await self.fetch_all_tickets(ocpstrats_to_fetch)
            ticket_data.update(ocpstrat_data)

        print(f"  Total Jira API requests: {self.request_count}")

        # Step 5: Build final hierarchy dict
        return self._build_hierarchy_dict(ticket_data)

    def _build_hierarchy_dict(self, ticket_data: Dict[str, Dict]) -> Dict[str, Dict]:
        """Build the final hierarchy dict with resolved references."""
        hierarchy = {}

        for key, data in ticket_data.items():
            epic_key = data.get('epicLink')
            epic_data = ticket_data.get(epic_key, {}) if epic_key else {}

            # Get OCPSTRAT from ticket or its epic
            ocpstrat_key = data.get('ocpstratParent') or epic_data.get('ocpstratParent')
            ocpstrat_data = ticket_data.get(ocpstrat_key, {}) if ocpstrat_key else {}

            hierarchy[key] = {
                'summary': data.get('summary', ''),
                'description': data.get('description', ''),
                'labels': data.get('labels', []),
                'priority': data.get('priority'),
                'status': data.get('status'),
                'epic': epic_key,
                'epicSummary': epic_data.get('summary'),
                'epicDescription': epic_data.get('description'),
                'ocpstrat': ocpstrat_key,
                'ocpstratSummary': ocpstrat_data.get('summary'),
            }

        return hierarchy


class PRReportGenerator:
    def __init__(self, since_date: str, end_date: Optional[str] = None):
        self.since_date = since_date
        self.end_date = end_date or datetime.now().strftime('%Y-%m-%d')
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
        # Use search query to filter by merge date range (range syntax: START..END)
        search_query = f"repo:{repo_owner}/{repo_name} is:pr is:merged merged:{self.since_date}..{self.end_date}"

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

    async def fetch_release_prs_graphql(self) -> List[Dict]:
        """Fetch PRs from openshift/release that touch HyperShift-related paths.

        Uses GitHub GraphQL API to fetch merged PRs, then filters by file paths
        to identify HyperShift-related changes.
        """
        repo_owner = 'openshift'
        repo_name = 'release'
        search_query = f"repo:{repo_owner}/{repo_name} is:pr is:merged merged:{self.since_date}..{self.end_date}"

        # Include files in the query for path-based filtering
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
                files(first: {GITHUB_FILES_LIMIT}) {{
                  nodes {{
                    path
                  }}
                }}
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
        total_scanned = 0
        retry_count = 0
        max_retries = 3

        def is_hypershift_related(pr_data: Dict) -> bool:
            """Check if PR touches any HyperShift-related paths."""
            files = pr_data.get('files', {}).get('nodes', [])
            for file_node in files:
                path = file_node.get('path', '')
                # Check if path contains 'hypershift' anywhere
                if 'hypershift' in path.lower():
                    return True
            return False

        if HAS_AIOHTTP:
            async with aiohttp.ClientSession() as session:
                while True:
                    variables = {"searchQuery": search_query, "cursor": cursor}
                    async with session.post(
                        'https://api.github.com/graphql',
                        json={"query": query, "variables": variables},
                        headers=headers
                    ) as response:
                        if response.status != 200:
                            retry_count += 1
                            if retry_count > max_retries:
                                print(f"  GitHub API returned {response.status}, max retries exceeded")
                                break
                            print(f"  GitHub API returned {response.status}, retrying ({retry_count}/{max_retries})...")
                            await asyncio.sleep(2)
                            continue
                        try:
                            data = await response.json()
                            retry_count = 0  # Reset on success
                        except Exception as e:
                            retry_count += 1
                            if retry_count > max_retries:
                                print(f"  Error parsing response: {e}, max retries exceeded")
                                break
                            print(f"  Error parsing response: {e}, retrying ({retry_count}/{max_retries})...")
                            await asyncio.sleep(2)
                            continue

                    if 'errors' in data:
                        print(f"GraphQL errors: {data['errors']}")
                        break

                    if 'data' not in data or not data['data']['search']:
                        break

                    for pr in data['data']['search']['nodes']:
                        if pr:
                            total_scanned += 1
                            if is_hypershift_related(pr):
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

                if 'errors' in data:
                    print(f"GraphQL errors: {data['errors']}")
                    break

                if 'data' not in data or not data['data']['search']:
                    break

                for pr in data['data']['search']['nodes']:
                    if pr:
                        total_scanned += 1
                        if is_hypershift_related(pr):
                            prs.append(self._process_pr_data(pr, f"{repo_owner}/{repo_name}"))

                page_info = data['data']['search']['pageInfo']
                if not page_info['hasNextPage']:
                    break
                cursor = page_info['endCursor']

        print(f"  Scanned {total_scanned} release PRs, found {len(prs)} HyperShift-related")
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

        # Fetch openshift/release PRs filtered by HyperShift-related paths
        print("Fetching openshift/release PRs (filtering by HyperShift paths)...")
        release_prs = await self.fetch_release_prs_graphql()

        self.prs = hypershift_prs + filtered_ai_helpers + release_prs
        print(f"Found {len(self.prs)} PRs ({len(hypershift_prs)} hypershift, {len(filtered_ai_helpers)} ai-helpers, {len(release_prs)} release)")

    async def load_jira_hierarchy(self):
        """Load Jira hierarchy - either via direct API or from cache.

        If JIRA_TOKEN is set, fetches data directly via Jira REST API.
        Otherwise, falls back to loading from cache file (populated by MCP tools).
        """
        # Extract all unique Jira tickets
        all_tickets = set()
        for pr in self.prs:
            all_tickets.update(pr['jiraTickets'])

        print(f"Found {len(all_tickets)} unique Jira tickets")

        if not all_tickets:
            self.jira_hierarchy = {}
            return

        # Check if we should use direct Jira API
        jira_client = JiraClient()

        if jira_client.enabled:
            # Fetch directly via Jira REST API
            self.jira_hierarchy = await jira_client.build_hierarchy(all_tickets)

            # Save to cache for future runs
            if self.jira_hierarchy:
                with open('/tmp/jira_hierarchy.json', 'w') as f:
                    json.dump(self.jira_hierarchy, f, indent=2)
                print(f"Saved Jira hierarchy to cache ({len(self.jira_hierarchy)} entries)")
        else:
            # Fall back to loading from cache
            print("JIRA_TOKEN not set, loading from cache (use MCP tools to populate)")
            try:
                with open('/tmp/jira_hierarchy.json', 'r') as f:
                    self.jira_hierarchy = json.load(f)
                print(f"Loaded Jira hierarchy cache with {len(self.jira_hierarchy)} entries")
            except FileNotFoundError:
                print("Warning: Jira hierarchy cache not found at /tmp/jira_hierarchy.json")
                print("Set JIRA_TOKEN or run /pr-report command to populate Jira data via MCP tools")
                self.jira_hierarchy = {}

    def generate_report(self, output_path: str):
        """Generate markdown report"""
        print("Generating report...")

        with open(output_path, 'w') as f:
            # Header
            f.write(f"# Weekly PR Report: openshift/hypershift\n")
            f.write(f"**Period:** {self.since_date} to {self.end_date}\n")

            # Count by repo
            hypershift_count = len([pr for pr in self.prs if pr['repo'] == 'openshift/hypershift'])
            ai_helpers_count = len([pr for pr in self.prs if pr['repo'] == 'openshift-eng/ai-helpers'])
            release_count = len([pr for pr in self.prs if pr['repo'] == 'openshift/release'])

            f.write(f"**Total PRs:** {len(self.prs)} ({hypershift_count} hypershift, {ai_helpers_count} ai-helpers, {release_count} release)\n\n")
            f.write("---\n\n")

            # Summary Statistics
            f.write("## Summary Statistics\n\n")
            f.write("### Repository Breakdown\n")
            f.write(f"- **openshift/hypershift:** {hypershift_count} PRs\n")
            f.write(f"- **openshift-eng/ai-helpers:** {ai_helpers_count} PRs\n")
            f.write(f"- **openshift/release:** {release_count} PRs\n\n")

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

    def save_summary_data(self, output_path: str):
        """Save compact PR summary for LLM analysis (no large body text).

        Outputs a compact format optimized for LLM consumption:
        - Stats and period info
        - PRs grouped by OCPSTRAT initiative
        - Ungrouped PRs listed separately
        - No redundant duplication of PR data
        """
        # Build compact PR records
        def compact_pr(pr: Dict) -> Dict:
            """Create a compact PR record with essential fields only."""
            # Get first OCPSTRAT from Jira tickets
            ocpstrat = None
            jira_summary = None
            for ticket in pr.get('jiraTickets', []):
                if ticket in self.jira_hierarchy:
                    h = self.jira_hierarchy[ticket]
                    jira_summary = h.get('summary', '')
                    if h.get('ocpstrat'):
                        ocpstrat = h['ocpstrat']
                        break

            return {
                'repo': pr['repo'].split('/')[-1],  # Just repo name, not owner
                'number': pr['number'],
                'title': pr['title'],
                'author': pr['author'],
                'topic': self._get_pr_topic(pr),
                'priority': self._get_pr_jira_priority(pr),
                'jira': pr.get('jiraTickets', []),
                'jiraSummary': jira_summary,
                'ocpstrat': ocpstrat,
                'mergeHours': round(pr.get('readyToMergeHours') or 0, 1),
            }

        compact_prs = [compact_pr(pr) for pr in self.prs]

        # Group by OCPSTRAT
        ocpstrat_groups = {}
        ungrouped = []

        for pr in compact_prs:
            ocpstrat = pr.get('ocpstrat')
            if ocpstrat:
                if ocpstrat not in ocpstrat_groups:
                    # Find OCPSTRAT summary from jira_hierarchy
                    ocpstrat_summary = self.jira_hierarchy.get(ocpstrat, {}).get('summary', '')
                    ocpstrat_groups[ocpstrat] = {
                        'key': ocpstrat,
                        'summary': ocpstrat_summary,
                        'prs': []
                    }
                # Remove ocpstrat from PR since it's redundant in grouped context
                pr_copy = {k: v for k, v in pr.items() if k != 'ocpstrat'}
                ocpstrat_groups[ocpstrat]['prs'].append(pr_copy)
            else:
                ungrouped.append(pr)

        # Calculate timing stats
        merge_times = [pr['mergeHours'] for pr in compact_prs if pr['mergeHours'] > 0]
        avg_merge = round(sum(merge_times) / len(merge_times), 1) if merge_times else 0

        # Find most active reviewer
        reviewer_counts = {}
        for pr in self.prs:
            for r in pr.get('reviewers', []):
                reviewer_counts[r] = reviewer_counts.get(r, 0) + 1
        top_reviewer = max(reviewer_counts.items(), key=lambda x: x[1]) if reviewer_counts else ('N/A', 0)

        # Score all PRs for deep analysis selection
        scored_prs = self.score_prs_for_deep_analysis(limit=len(self.prs))
        scored_list = [{
            'rank': i + 1,
            'score': pr['score'],
            'repo': pr['repo'].split('/')[-1],
            'number': pr['number'],
            'title': pr['title'][:80],
            'priority': pr.get('priority', '-'),
            'topic': pr.get('topic', '-'),
            'pr_id': f"{pr['repo']}#{pr['number']}",
        } for i, pr in enumerate(scored_prs)]

        output = {
            'period': f"{self.since_date} to {self.end_date}",
            'stats': {
                'total': len(compact_prs),
                'hypershift': len([p for p in compact_prs if p['repo'] == 'hypershift']),
                'ai_helpers': len([p for p in compact_prs if p['repo'] == 'ai-helpers']),
                'release': len([p for p in compact_prs if p['repo'] == 'release']),
                'authors': len(set(p['author'] for p in compact_prs)),
                'avgMergeHours': avg_merge,
                'topReviewer': f"@{top_reviewer[0]} ({top_reviewer[1]} PRs)",
            },
            'initiatives': list(ocpstrat_groups.values()),
            'other': ungrouped,
            'scored': scored_list,  # Pre-scored for deep analysis selection
        }

        with open(output_path, 'w') as f:
            json.dump(output, f, indent=2)
        print(f"Summary data saved to {output_path}")

    def parse_pr_identifiers(self, pr_ids: List[str]) -> List[Tuple[str, int]]:
        """Parse PR identifiers in owner/repo#number format.

        Args:
            pr_ids: List of strings like "openshift/hypershift#7709"

        Returns:
            List of (repo, pr_number) tuples
        """
        parsed = []
        for pr_id in pr_ids:
            match = re.match(r'^([^/]+/[^#]+)#(\d+)$', pr_id)
            if match:
                repo = match.group(1)
                pr_num = int(match.group(2))
                parsed.append((repo, pr_num))
            else:
                print(f"Warning: Invalid PR format '{pr_id}', expected owner/repo#number")
        return parsed

    def _parse_conventional_commit(self, title: str) -> Tuple[Optional[str], Optional[str]]:
        """Parse conventional commit type and scope from PR title.

        Conventional commit format: type(scope): description
        Or with Jira prefix: TICKET-123: type(scope): description

        Returns:
            Tuple of (type, scope) or (None, None) if not found
        """
        # Strip Jira ticket prefix if present (e.g., "CNTRLPLANE-123: ")
        title_stripped = re.sub(r'^(?:\[.*?\]\s*)?(?:[A-Z]+-\d+:\s*)+', '', title)

        # Match conventional commit: type(scope): or type:
        match = re.match(r'^(\w+)(?:\(([^)]+)\))?:\s*', title_stripped)
        if match:
            commit_type = match.group(1).lower()
            scope = match.group(2).lower() if match.group(2) else None
            return commit_type, scope

        return None, None

    def _get_pr_topic(self, pr: Dict) -> str:
        """Derive topic category from PR title using conventional commit format."""
        title = pr.get('title', '')

        # Try to parse conventional commit format first
        commit_type, scope = self._parse_conventional_commit(title)

        if commit_type:
            # Return type with scope if available (e.g., "feat:aws", "fix:nodepool")
            if scope:
                return f"{commit_type}:{scope}"
            return commit_type

        # Fallback: check for OCPBUGS (bug fix)
        if any(t.startswith('OCPBUGS') for t in pr.get('jiraTickets', [])):
            return 'fix'

        # Fallback: CI for release repo
        if pr['repo'] == 'openshift/release':
            return 'ci'

        return '-'

    def _get_pr_jira_priority(self, pr: Dict) -> str:
        """Get highest Jira priority from PR's tickets."""
        priority_order = ['Critical', 'Blocker', 'Major', 'Normal', 'Minor', 'Undefined']
        highest_priority = '-'
        highest_idx = len(priority_order)

        for ticket in pr.get('jiraTickets', []):
            if ticket in self.jira_hierarchy:
                priority = self.jira_hierarchy[ticket].get('priority', 'Undefined')
                try:
                    idx = priority_order.index(priority)
                    if idx < highest_idx:
                        highest_idx = idx
                        highest_priority = priority
                except ValueError:
                    pass

        return highest_priority

    def score_prs_for_deep_analysis(self, limit: int = 20) -> List[Dict]:
        """Score PRs by importance for deep analysis selection.

        Scoring criteria (higher = more important):
        - Jira priority: Critical=100, Blocker=100, Major=50, Normal=20, Minor=10
        - SDK/API/migration work: +30 points
        - Feature work (feat in title): +15 points
        - Bug fixes (OCPBUGS): +10 points
        - Has Jira ticket: +5 points
        - Non-bot author in openshift/release: +10 points

        Args:
            limit: Maximum number of PRs to return

        Returns:
            List of PR dicts with 'score', 'score_reasons', 'topic', and 'priority' fields,
            sorted by score descending
        """
        # Priority scores (higher = more important)
        priority_scores = {
            'Critical': 100,
            'Blocker': 100,
            'Major': 50,
            'Normal': 20,
            'Minor': 10,
            'Undefined': 5,
        }

        # Bot patterns to detect automated PRs
        bot_patterns = ['bot', 'robot', 'renovate', 'dependabot']

        scored_prs = []

        for pr in self.prs:
            score = 0
            reasons = []

            # Get topic and priority for display
            topic = self._get_pr_topic(pr)
            priority = self._get_pr_jira_priority(pr)

            # Score based on Jira ticket priority
            for ticket in pr.get('jiraTickets', []):
                if ticket in self.jira_hierarchy:
                    ticket_priority = self.jira_hierarchy[ticket].get('priority', 'Undefined')
                    pscore = priority_scores.get(ticket_priority, 5)
                    score += pscore
                    if pscore >= 50:
                        reasons.append(ticket_priority)

            # Bonus for SDK/API/migration work (significant changes)
            title_lower = pr.get('title', '').lower()
            if any(term in title_lower for term in ['sdk', 'migrate', 'api', 'breaking']):
                score += 30
                reasons.append('SDK/API')
            elif 'feat' in title_lower or 'feature' in title_lower:
                score += 15
                reasons.append('feature')

            # Bug fixes get moderate priority
            if any(t.startswith('OCPBUGS') for t in pr.get('jiraTickets', [])):
                score += 10
                reasons.append('bugfix')

            # Having any Jira ticket is better than none
            if pr.get('jiraTickets'):
                score += 5

            # For release repo, prefer non-bot PRs (manual CI changes)
            if pr['repo'] == 'openshift/release':
                author_lower = pr.get('author', '').lower()
                is_bot = any(bot in author_lower for bot in bot_patterns)
                if not is_bot:
                    score += 10
                    reasons.append('manual-CI')

            # Store score in PR
            pr_copy = pr.copy()
            pr_copy['score'] = score
            pr_copy['score_reasons'] = reasons
            pr_copy['topic'] = topic
            pr_copy['priority'] = priority
            scored_prs.append(pr_copy)

        # Sort by score descending
        scored_prs.sort(key=lambda x: -x['score'])

        return scored_prs[:limit]

    def print_scored_prs(self, scored_prs: List[Dict]):
        """Print scored PRs in a readable format with priority and topic."""
        print("\nPR Selection by Importance Score:")
        print("-" * 120)
        print(f"{'#':<3} {'Score':<6} {'Priority':<10} {'Repo':<12} {'PR':<7} {'Topic':<14} {'Title':<60}")
        print("-" * 120)

        for i, pr in enumerate(scored_prs, 1):
            repo_short = pr['repo'].replace('openshift/', '').replace('openshift-eng/', '')
            title_trunc = pr['title'][:58] + '..' if len(pr['title']) > 60 else pr['title']
            topic = pr.get('topic', '-')[:14]
            print(f"{i:<3} {pr['score']:<6} {pr.get('priority', '-'):<10} {repo_short:<12} #{pr['number']:<6} {topic:<14} {title_trunc}")

        print("-" * 120)
        print(f"Selected {len(scored_prs)} PRs for deep analysis\n")

    async def fetch_pr_diffs(self, pr_list: List[Tuple[str, int]]) -> Dict[str, Dict]:
        """Fetch diffs for selected PRs via GitHub REST API.

        Args:
            pr_list: List of (repo, pr_number) tuples

        Returns:
            Dict mapping "owner_repo_number" key to diff data
        """
        headers = {
            "Authorization": f"Bearer {self.github_token}",
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28"
        }

        async def fetch_single_diff(session, repo: str, number: int) -> Dict:
            """Fetch diff for a single PR."""
            url = f"https://api.github.com/repos/{repo}/pulls/{number}/files"

            try:
                if HAS_AIOHTTP:
                    async with session.get(url, headers=headers) as response:
                        if response.status != 200:
                            return {'repo': repo, 'number': number, 'error': f"HTTP {response.status}"}
                        files = await response.json()
                else:
                    response = requests.get(url, headers=headers, timeout=30)
                    if response.status_code != 200:
                        return {'repo': repo, 'number': number, 'error': f"HTTP {response.status_code}"}
                    files = response.json()

                # Process file patches, truncate large ones
                patches = []
                for f in files:
                    patch = f.get('patch', '')
                    if len(patch) > 5000:
                        patch = patch[:5000] + "\n... [truncated]"
                    patches.append({
                        'filename': f['filename'],
                        'status': f['status'],
                        'additions': f['additions'],
                        'deletions': f['deletions'],
                        'patch': patch
                    })

                return {
                    'repo': repo,
                    'number': number,
                    'files': patches,
                    'total_additions': sum(f['additions'] for f in files),
                    'total_deletions': sum(f['deletions'] for f in files),
                    'total_files': len(files)
                }
            except Exception as e:
                return {'repo': repo, 'number': number, 'error': str(e)}

        results = {}

        if HAS_AIOHTTP:
            async with aiohttp.ClientSession() as session:
                tasks = [fetch_single_diff(session, repo, num) for repo, num in pr_list]
                responses = await asyncio.gather(*tasks)
                for r in responses:
                    key = f"{r['repo'].replace('/', '_')}_{r['number']}"
                    results[key] = r
        else:
            for repo, num in pr_list:
                r = await fetch_single_diff(None, repo, num)
                key = f"{repo.replace('/', '_')}_{num}"
                results[key] = r

        errors = [k for k, v in results.items() if 'error' in v]
        if errors:
            print(f"  Warning: Failed to fetch {len(errors)} PRs")

        return results

    def write_deep_pr_files(self, diffs: Dict[str, Dict], output_dir: str = '.work/pr_deep'):
        """Write per-PR JSON files combining metadata + Jira + diff.

        Args:
            diffs: Dict from fetch_pr_diffs()
            output_dir: Directory to write files
        """
        os.makedirs(output_dir, exist_ok=True)

        written = 0
        for key, diff_data in diffs.items():
            if 'error' in diff_data:
                continue

            repo = diff_data['repo']
            number = diff_data['number']

            # Find matching PR metadata
            pr = next((p for p in self.prs if p['repo'] == repo and p['number'] == number), None)

            if not pr:
                print(f"  Warning: No metadata found for {repo}#{number}")
                continue

            # Build combined JSON
            combined = {
                'repo': pr['repo'],
                'number': pr['number'],
                'title': pr['title'],
                'url': pr['url'],
                'author': pr['author'],
                'body': pr['body'],
                'jiraTickets': pr['jiraTickets'],
                'labels': pr['labels'],
                'mergedAt': pr['mergedAt'],
                'jiraHierarchy': {t: self.jira_hierarchy[t] for t in pr['jiraTickets'] if t in self.jira_hierarchy},
                'diff': {
                    'files': diff_data.get('files', []),
                    'total_additions': diff_data.get('total_additions', 0),
                    'total_deletions': diff_data.get('total_deletions', 0),
                    'total_files': diff_data.get('total_files', 0)
                }
            }

            filepath = os.path.join(output_dir, f"{key}.json")
            with open(filepath, 'w') as f:
                json.dump(combined, f, indent=2)
            written += 1

        print(f"Wrote {written} per-PR JSON files to {output_dir}/")


def parse_args():
    """Parse command line arguments."""
    parser = argparse.ArgumentParser(
        description='Generate weekly PR report for HyperShift repositories',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  %(prog)s 2026-02-05
      Standard report since date (until today)

  %(prog)s 2026-02-05 --end 2026-02-12
      Report for specific date range

  %(prog)s 2026-02-05 --end 2026-02-12 --score
      Output scored PR list for deep analysis selection

  %(prog)s 2026-02-05 --score --score-limit 30
      Output top 30 scored PRs

  %(prog)s 2026-02-05 --deep openshift/hypershift#7709 openshift/release#74707
      Fetch diffs for specific PRs (for deep analysis)

PR format: owner/repo#number (e.g., openshift/hypershift#7657)

Scoring criteria (higher = more important):
  - Jira priority: Critical/Blocker=100, Major=50, Normal=20, Minor=10
  - SDK/API/migration work: +30 points
  - Feature work: +15 points
  - Bug fixes (OCPBUGS): +10 points
  - Manual CI changes (non-bot in release repo): +10 points
        """
    )
    parser.add_argument(
        'since_date',
        nargs='?',
        default=(datetime.now() - timedelta(days=DEFAULT_DAYS_AGO)).strftime('%Y-%m-%d'),
        help='Start date in YYYY-MM-DD format (default: 7 days ago)'
    )
    parser.add_argument(
        '--end',
        dest='end_date',
        default=datetime.now().strftime('%Y-%m-%d'),
        help='End date in YYYY-MM-DD format (default: today)'
    )
    parser.add_argument(
        '--deep',
        nargs='+',
        metavar='PR',
        help='Fetch diffs for specified PRs (owner/repo#number format)'
    )
    parser.add_argument(
        '--score',
        action='store_true',
        help='Output scored PR list for deep analysis selection'
    )
    parser.add_argument(
        '--score-limit',
        type=int,
        default=20,
        metavar='N',
        help='Number of PRs to include in scored output (default: 20)'
    )
    return parser.parse_args()


async def main():
    import time
    start_time = time.time()

    args = parse_args()
    since_date = args.since_date
    end_date = args.end_date
    deep_prs = args.deep or []
    score_mode = args.score
    score_limit = args.score_limit

    print(f"Generating PR report for: {since_date} to {end_date}")
    print(f"Using {'async (aiohttp)' if HAS_AIOHTTP else 'sync (requests)'} mode")
    if deep_prs:
        print(f"Deep mode: will fetch diffs for {len(deep_prs)} PRs")
    if score_mode:
        print(f"Score mode: will output top {score_limit} PRs by importance")
    print()

    generator = PRReportGenerator(since_date, end_date)

    # Fetch all data
    await generator.fetch_all_prs()
    await generator.load_jira_hierarchy()

    # Generate outputs
    generator.generate_report('/tmp/weekly_pr_report_fast.md')
    generator.save_raw_data('/tmp/hypershift_pr_details_fast.json')
    generator.save_summary_data('/tmp/hypershift_pr_summary.json')

    # Score mode: output scored PR list
    if score_mode:
        scored_prs = generator.score_prs_for_deep_analysis(limit=score_limit)
        generator.print_scored_prs(scored_prs)

        # Also save to JSON for programmatic use
        scored_output = [{
            'repo': pr['repo'],
            'number': pr['number'],
            'title': pr['title'],
            'score': pr['score'],
            'score_reasons': pr['score_reasons'],
            'pr_id': f"{pr['repo']}#{pr['number']}"
        } for pr in scored_prs]

        with open('/tmp/pr_scored.json', 'w') as f:
            json.dump(scored_output, f, indent=2)
        print(f"Scored PRs saved to /tmp/pr_scored.json")

        # Print PR list ready for --deep flag
        print("\nPR list for --deep flag:")
        print(' '.join([pr['pr_id'] for pr in scored_output]))

    # Deep mode: fetch diffs for specified PRs
    if deep_prs:
        pr_list = generator.parse_pr_identifiers(deep_prs)
        if pr_list:
            print(f"\nFetching diffs for {len(pr_list)} PRs...")
            diffs = await generator.fetch_pr_diffs(pr_list)
            generator.write_deep_pr_files(diffs)

    elapsed = time.time() - start_time
    print(f"\nDone in {elapsed:.2f} seconds!")


if __name__ == '__main__':
    if HAS_AIOHTTP:
        asyncio.run(main())
    else:
        # Python 3.6 compatibility
        loop = asyncio.get_event_loop()
        loop.run_until_complete(main())
