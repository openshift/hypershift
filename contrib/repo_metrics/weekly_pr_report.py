#!/usr/bin/env python3
"""
Weekly PR Report Generator for HyperShift
Optimized version with parallel API calls and batch processing
"""

import argparse
import asyncio
import base64
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

# Jira rate limiting constants (conservative for Jira Cloud)
JIRA_REQUEST_DELAY_SECONDS = 0.2  # 200ms between requests
JIRA_MAX_CONCURRENT_REQUESTS = 3  # Max 3 parallel requests
JIRA_BATCH_SIZE = 100  # Max tickets per bulkfetch call


class JiraClient:
    """Client for fetching Jira issues via REST API with batch support and rate limiting.

    Targets Jira Cloud at redhat.atlassian.net. Uses Basic auth (email + API token).
    Field mapping (Cloud IDs):
      parent           = native hierarchy (Story->Epic->Feature, replaces old custom fields)
      customfield_10978 = SFDC Cases Counter
      customfield_10979 = SFDC Cases Links
      customfield_10980 = SFDC Cases Open
    """

    FIELDS = 'summary,description,parent,issuetype,customfield_10978,customfield_10979,customfield_10980,issuelinks,labels,priority,status'

    def __init__(self):
        self.base_url = os.getenv('JIRA_URL', 'https://redhat.atlassian.net')
        self.token = os.getenv('JIRA_TOKEN')
        self.email = os.getenv('JIRA_EMAIL')
        self.enabled = bool(self.token and self.email)
        self.session: Optional["aiohttp.ClientSession"] = None
        self.semaphore = asyncio.Semaphore(JIRA_MAX_CONCURRENT_REQUESTS)
        self.request_count = 0

    def _get_headers(self) -> Dict:
        """Get authentication headers for Jira Cloud (Basic auth with email + API token)."""
        headers = {
            'Accept': 'application/json',
            'Content-Type': 'application/json',
        }
        if self.token and self.email:
            credentials = base64.b64encode(f'{self.email}:{self.token}'.encode()).decode()
            headers['Authorization'] = f'Basic {credentials}'
        return headers

    async def _fetch_json(self, url: str, *, json_body: Optional[Dict] = None,
                          _retry_count: int = 0) -> Optional[Dict]:
        """Fetch JSON from Jira with rate limiting and error handling.

        Uses POST if json_body is provided, GET otherwise.
        """
        MAX_RETRIES = 3
        async with self.semaphore:
            await asyncio.sleep(JIRA_REQUEST_DELAY_SECONDS)
            self.request_count += 1

            if HAS_AIOHTTP and self.session:
                try:
                    method = self.session.post if json_body is not None else self.session.get
                    kwargs = {'headers': self._get_headers()}
                    if json_body is not None:
                        kwargs['json'] = json_body
                    async with method(url, **kwargs) as response:
                        if response.status == 200:
                            return await response.json()
                        elif response.status == 401:
                            print("Warning: Jira authentication failed (401). Check JIRA_EMAIL and JIRA_TOKEN.")
                            return None
                        elif response.status == 404:
                            return None
                        elif response.status == 429:
                            if _retry_count >= MAX_RETRIES:
                                print(f"  Rate limited {MAX_RETRIES} times, giving up on {url}")
                                return None
                            print("  Rate limited! Waiting 30 seconds...")
                            await asyncio.sleep(30)
                            return await self._fetch_json(url, json_body=json_body, _retry_count=_retry_count + 1)
                        else:
                            text = await response.text()
                            print(f"Warning: Jira API returned {response.status}: {text[:200]}")
                            return None
                except Exception as e:
                    print(f"Error fetching {url}: {e}")
                    return None
            else:
                try:
                    if json_body is not None:
                        response = requests.post(url, headers=self._get_headers(), json=json_body, timeout=30)
                    else:
                        response = requests.get(url, headers=self._get_headers(), timeout=30)
                    if response.status_code == 200:
                        return response.json()
                    elif response.status_code == 401:
                        print("Warning: Jira authentication failed (401). Check JIRA_EMAIL and JIRA_TOKEN.")
                        return None
                    elif response.status_code == 404:
                        return None
                    elif response.status_code == 429:
                        if _retry_count >= MAX_RETRIES:
                            print(f"  Rate limited {MAX_RETRIES} times, giving up on {url}")
                            return None
                        print("  Rate limited! Waiting 30 seconds...")
                        await asyncio.sleep(30)
                        return await self._fetch_json(url, json_body=json_body, _retry_count=_retry_count + 1)
                    else:
                        return None
                except Exception as e:
                    print(f"Error fetching {url}: {e}")
                    return None

    async def fetch_issues_batch(self, ticket_keys: List[str]) -> Dict[str, Dict]:
        """Fetch multiple issues using POST /rest/api/3/issue/bulkfetch.

        Advantages over JQL search:
        - Up to 100 keys per call (vs 40 with JQL)
        - No JQL query construction/parsing overhead
        - Per-issue error handling via issueErrors
        """
        if not ticket_keys:
            return {}

        url = f'{self.base_url}/rest/api/3/issue/bulkfetch'
        payload = {
            'issueIdsOrKeys': ticket_keys,
            'fields': self.FIELDS.split(','),
            'expand': ['renderedFields'],
        }

        results = {}
        data = await self._fetch_json(url, json_body=payload)

        if data:
            for issue in data.get('issues', []):
                results[issue['key']] = self._parse_issue(issue)
            errors = data.get('issueErrors', {})
            if errors:
                print(f"  Warning: {len(errors)} issues had errors: {list(errors.keys())[:5]}")

        return results

    def _parse_issue(self, issue: Dict) -> Dict:
        """Parse Jira Cloud issue response into simplified dict.

        Uses the native `parent` field for hierarchy traversal. The parent field
        includes inline data (key, summary, status, priority, issuetype with
        hierarchyLevel) so we can walk Story->Epic->Feature in fewer API calls.
        """
        fields = issue.get('fields', {})

        # Extract parent key (Jira Cloud native hierarchy)
        parent_field = fields.get('parent') or {}
        parent_key = parent_field.get('key')

        # Determine issue type
        issue_type = fields.get('issuetype') or {}
        hierarchy_level = issue_type.get('hierarchyLevel')
        type_name = issue_type.get('name')

        # Get description (truncate for storage)
        description = fields.get('description') or ''
        if isinstance(description, dict):
            # Jira Cloud v3 returns ADF (Atlassian Document Format); extract text
            description = self._extract_text_from_adf(description)
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

        # Extract SFDC case data from renderedFields (Cloud Forge app fields are
        # encrypted in raw fields but decrypted in renderedFields)
        rendered = issue.get('renderedFields', {})
        sfdc_counter_str = rendered.get('customfield_10978') or '0'
        sfdc_open_str = rendered.get('customfield_10980') or '0'
        sfdc_links_str = rendered.get('customfield_10979') or ''

        try:
            sfdc_cases_total = int(sfdc_counter_str)
        except (ValueError, TypeError):
            sfdc_cases_total = 0
        try:
            sfdc_cases_open = int(sfdc_open_str)
        except (ValueError, TypeError):
            sfdc_cases_open = 0

        # Parse case IDs — rendered links are space-separated case numbers
        sfdc_case_ids = re.findall(r'\b(\d{6,8})\b', sfdc_links_str) if sfdc_links_str else []
        sfdc_cases_links_raw = sfdc_links_str

        return {
            'key': issue['key'],
            'summary': fields.get('summary', ''),
            'description': description,
            'parentKey': parent_key,
            'hierarchyLevel': hierarchy_level,
            'typeName': type_name,
            'labels': labels,
            'priority': priority_name,
            'status': status_name,
            'sfdcCasesTotal': sfdc_cases_total,
            'sfdcCasesOpen': sfdc_cases_open,
            'sfdcCaseIds': sfdc_case_ids,
            'sfdcCasesLinks': sfdc_cases_links_raw,
        }

    @staticmethod
    def _extract_text_from_adf(adf: Dict) -> str:
        """Extract plain text from Atlassian Document Format (ADF)."""
        texts = []
        def _walk(node):
            if isinstance(node, dict):
                if node.get('type') == 'text':
                    texts.append(node.get('text', ''))
                for child in node.get('content', []):
                    _walk(child)
            elif isinstance(node, list):
                for item in node:
                    _walk(item)
        _walk(adf)
        return '\n'.join(texts)

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
        """Internal hierarchy builder using Jira Cloud native parent field.

        Fetches tickets, then iteratively resolves unfetched parents by
        enqueueing newly discovered parent keys from each batch.
        """
        ticket_data: Dict[str, Dict] = {}
        queue = tickets

        while True:
            fetched = await self.fetch_all_tickets(queue)
            ticket_data.update(fetched)

            queue = {
                data['parentKey']
                for data in fetched.values()
                if data.get('parentKey') and data['parentKey'] not in ticket_data
            }
            if not queue:
                break
            print(f"  Fetching {len(queue)} parent issues...")

        print(f"  Total Jira API requests: {self.request_count}")
        return self._build_hierarchy_dict(ticket_data)

    def _build_hierarchy_dict(self, ticket_data: Dict[str, Dict]) -> Dict[str, Dict]:
        """Build the final hierarchy dict by walking each ticket's parent chain.

        Walks up from each ticket classifying ancestors by issue type:
          Epic    = issuetype 'Epic' or hierarchyLevel 1
          Feature = issuetype 'Feature' or hierarchyLevel 2
        """
        hierarchy = {}

        for key, data in ticket_data.items():
            epic_key = epic_summary = epic_description = None
            feature_key = feature_summary = None

            seen = {key}
            current = data
            while current.get('parentKey') and current['parentKey'] not in seen:
                pk = current['parentKey']
                seen.add(pk)
                parent = ticket_data.get(pk)
                if not parent:
                    break

                ptype = parent.get('typeName', '')
                plevel = parent.get('hierarchyLevel')

                if (plevel == 1 or ptype == 'Epic') and not epic_key:
                    epic_key = pk
                    epic_summary = parent.get('summary')
                    epic_description = parent.get('description')
                elif (plevel == 2 or ptype == 'Feature') and not feature_key:
                    feature_key = pk
                    feature_summary = parent.get('summary')

                current = parent

            hierarchy[key] = {
                'summary': data.get('summary', ''),
                'description': data.get('description', ''),
                'labels': data.get('labels', []),
                'priority': data.get('priority'),
                'status': data.get('status'),
                'epic': epic_key,
                'epicSummary': epic_summary,
                'epicDescription': epic_description,
                'ocpstrat': feature_key,
                'ocpstratSummary': feature_summary,
                'sfdcCasesTotal': data.get('sfdcCasesTotal', 0),
                'sfdcCasesOpen': data.get('sfdcCasesOpen', 0),
                'sfdcCaseIds': data.get('sfdcCaseIds', []),
                'sfdcCasesLinks': data.get('sfdcCasesLinks', ''),
            }

        return hierarchy


class PRReportGenerator:
    def __init__(self, since_date: str, end_date: Optional[str] = None, output_dir: str = '/tmp'):
        self.since_date = since_date
        self.end_date = end_date or datetime.now().strftime('%Y-%m-%d')
        self.output_dir = output_dir
        self.github_token = os.getenv('GITHUB_TOKEN') or self._get_gh_token()
        self.jira_url = os.getenv('JIRA_URL', 'https://redhat.atlassian.net')

        # Data storage
        self.prs: List[Dict] = []
        self.jira_hierarchy: Dict = {}
        self.hypershift_authors: Set[str] = set()

    # Bot authors to tag in release repo PRs
    RELEASE_BOT_AUTHORS = {'renovate[bot]', 'dependabot[bot]'}

    async def _github_graphql_request(self, session, query: str, variables: Dict,
                                       headers: Dict, max_retries: int = 3) -> Optional[Dict]:
        """Make a GitHub GraphQL request with rate limit handling.

        Handles 403/429 rate limit responses by respecting Retry-After headers.
        Returns parsed JSON data or None on failure.
        """
        for attempt in range(max_retries + 1):
            try:
                if HAS_AIOHTTP and session is not None:
                    async with session.post(
                        'https://api.github.com/graphql',
                        json={"query": query, "variables": variables},
                        headers=headers
                    ) as response:
                        if response.status in (403, 429):
                            retry_after = int(response.headers.get('Retry-After', 60))
                            print(f"  Rate limited (HTTP {response.status}), waiting {retry_after}s...")
                            await asyncio.sleep(retry_after)
                            continue
                        if response.status != 200:
                            print(f"  GitHub API returned {response.status}")
                            if attempt < max_retries:
                                await asyncio.sleep(2)
                                continue
                            return None
                        return await response.json()
                else:
                    response = requests.post(
                        'https://api.github.com/graphql',
                        json={"query": query, "variables": variables},
                        headers=headers,
                        timeout=30
                    )
                    if response.status_code in (403, 429):
                        retry_after = int(response.headers.get('Retry-After', 60))
                        print(f"  Rate limited (HTTP {response.status_code}), waiting {retry_after}s...")
                        await asyncio.sleep(retry_after)
                        continue
                    if response.status_code != 200:
                        print(f"  GitHub API returned {response.status_code}")
                        if attempt < max_retries:
                            await asyncio.sleep(2)
                            continue
                        return None
                    return response.json()
            except Exception as e:
                print(f"  Request error: {e}")
                if attempt < max_retries:
                    await asyncio.sleep(2)
                    continue
                return None
        return None

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

        session = aiohttp.ClientSession() if HAS_AIOHTTP else None
        try:
            while True:
                variables = {"searchQuery": search_query, "cursor": cursor}
                data = await self._github_graphql_request(session, query, variables, headers)
                if not data or 'data' not in data or not data['data']['search']:
                    break

                for pr in data['data']['search']['nodes']:
                    if pr:  # Skip null entries
                        prs.append(self._process_pr_data(pr, f"{repo_owner}/{repo_name}"))

                page_info = data['data']['search']['pageInfo']
                if not page_info['hasNextPage']:
                    break
                cursor = page_info['endCursor']
        finally:
            if session:
                await session.close()

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
                  pageInfo {{
                    hasNextPage
                  }}
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

        def files_incomplete(pr_data: Dict) -> bool:
            """Check if files were truncated by the GraphQL limit."""
            return pr_data.get('files', {}).get('pageInfo', {}).get('hasNextPage', False)

        async def check_remaining_files_async(session, pr_number: int) -> bool:
            """Use REST API to check all files when GraphQL result was truncated."""
            page = 1
            per_page = 100
            while True:
                url = f"https://api.github.com/repos/{repo_owner}/{repo_name}/pulls/{pr_number}/files"
                params = {"per_page": per_page, "page": page}
                try:
                    if HAS_AIOHTTP:
                        async with session.get(url, headers=headers, params=params) as resp:
                            if resp.status != 200:
                                return False
                            batch = await resp.json()
                    else:
                        resp = requests.get(url, headers=headers, params=params, timeout=30)
                        if resp.status_code != 200:
                            return False
                        batch = resp.json()
                except Exception:
                    return False
                for f in batch:
                    if 'hypershift' in f.get('filename', '').lower():
                        return True
                if len(batch) < per_page:
                    break
                page += 1
            return False

        session = aiohttp.ClientSession() if HAS_AIOHTTP else None
        bot_count = 0
        try:
            while True:
                variables = {"searchQuery": search_query, "cursor": cursor}
                data = await self._github_graphql_request(session, query, variables, headers)
                if not data:
                    break

                if 'errors' in data:
                    print(f"GraphQL errors: {data['errors']}")
                    break

                if 'data' not in data or not data['data']['search']:
                    break

                for pr in data['data']['search']['nodes']:
                    if pr:
                        total_scanned += 1
                        if is_hypershift_related(pr):
                            pr_data = self._process_pr_data(pr, f"{repo_owner}/{repo_name}")
                            # Tag bot-authored PRs
                            author = pr.get('author', {}).get('login', '') if pr.get('author') else ''
                            if author in self.RELEASE_BOT_AUTHORS:
                                pr_data['is_bot'] = True
                                bot_count += 1
                            prs.append(pr_data)
                        elif files_incomplete(pr):
                            if await check_remaining_files_async(session, pr['number']):
                                pr_data = self._process_pr_data(pr, f"{repo_owner}/{repo_name}")
                                author = pr.get('author', {}).get('login', '') if pr.get('author') else ''
                                if author in self.RELEASE_BOT_AUTHORS:
                                    pr_data['is_bot'] = True
                                    bot_count += 1
                                prs.append(pr_data)

                page_info = data['data']['search']['pageInfo']
                if not page_info['hasNextPage']:
                    break
                cursor = page_info['endCursor']
        finally:
            if session:
                await session.close()

        bot_note = f", {bot_count} bot-authored" if bot_count else ""
        print(f"  Scanned {total_scanned} release PRs, found {len(prs)} HyperShift-related{bot_note}")
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
                self.fetch_prs_graphql('openshift', 'enhancements'),
            ]
            results = await asyncio.gather(*tasks)
        else:
            # Fallback to sequential
            results = [
                await self.fetch_prs_graphql('openshift', 'hypershift'),
                await self.fetch_prs_graphql('openshift-eng', 'ai-helpers'),
                await self.fetch_prs_graphql('openshift', 'enhancements'),
            ]

        # Combine and filter
        hypershift_prs = results[0]
        ai_helpers_prs = results[1] if len(results) > 1 else []
        enhancements_prs = results[2] if len(results) > 2 else []

        # Filter ai-helpers PRs to only HyperShift contributors
        filtered_ai_helpers = [
            pr for pr in ai_helpers_prs
            if pr['author'] in self.hypershift_authors
        ]

        # Filter enhancements PRs to only HyperShift contributors
        filtered_enhancements = [
            pr for pr in enhancements_prs
            if pr['author'] in self.hypershift_authors
        ]

        # Fetch openshift/release PRs filtered by HyperShift-related paths
        print("Fetching openshift/release PRs (filtering by HyperShift paths)...")
        release_prs = await self.fetch_release_prs_graphql()

        self.prs = hypershift_prs + filtered_ai_helpers + filtered_enhancements + release_prs
        print(f"Found {len(self.prs)} PRs ({len(hypershift_prs)} hypershift, {len(filtered_ai_helpers)} ai-helpers, {len(filtered_enhancements)} enhancements, {len(release_prs)} release)")

    async def load_jira_hierarchy(self):
        """Load Jira hierarchy - either via direct API or from cache.

        If JIRA_EMAIL + JIRA_TOKEN are set, fetches data directly via Jira Cloud REST API.
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
            jira_cache_path = os.path.join(self.output_dir, 'jira_hierarchy.json')
            if self.jira_hierarchy:
                with open(jira_cache_path, 'w') as f:
                    json.dump(self.jira_hierarchy, f, indent=2)
                print(f"Saved Jira hierarchy to cache ({len(self.jira_hierarchy)} entries)")
        else:
            # Fall back to loading from cache
            jira_cache_path = os.path.join(self.output_dir, 'jira_hierarchy.json')
            print("JIRA_EMAIL/JIRA_TOKEN not set, loading from cache (use MCP tools to populate)")
            try:
                # Check if cache is stale (older than report end date)
                cache_mtime = datetime.fromtimestamp(os.path.getmtime(jira_cache_path))
                report_end = datetime.strptime(self.end_date, '%Y-%m-%d')
                if cache_mtime < report_end:
                    print(f"Warning: Jira cache is stale (last updated {cache_mtime.strftime('%Y-%m-%d')}, "
                          f"report ends {self.end_date}). Invalidating cache.")
                    os.remove(jira_cache_path)
                    raise FileNotFoundError
                with open(jira_cache_path, 'r') as f:
                    self.jira_hierarchy = json.load(f)
                print(f"Loaded Jira hierarchy cache with {len(self.jira_hierarchy)} entries")
            except FileNotFoundError:
                print(f"Warning: Jira hierarchy cache not found at {jira_cache_path}")
                print("Set JIRA_EMAIL + JIRA_TOKEN or run /pr-report command to populate Jira data via MCP tools")
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
            enhancements_count = len([pr for pr in self.prs if pr['repo'] == 'openshift/enhancements'])
            release_count = len([pr for pr in self.prs if pr['repo'] == 'openshift/release'])

            f.write(f"**Total PRs:** {len(self.prs)} ({hypershift_count} hypershift, {ai_helpers_count} ai-helpers, {enhancements_count} enhancements, {release_count} release)\n\n")
            f.write("---\n\n")

            # Summary Statistics
            f.write("## Summary Statistics\n\n")
            f.write("### Repository Breakdown\n")
            f.write(f"- **openshift/hypershift:** {hypershift_count} PRs\n")
            f.write(f"- **openshift-eng/ai-helpers:** {ai_helpers_count} PRs\n")
            f.write(f"- **openshift/enhancements:** {enhancements_count} PRs\n")
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

            # Categorize PRs into sections
            bug_prs = []       # OCPBUGS tickets (any repo)
            ai_helpers_prs = []  # openshift-eng/ai-helpers
            enhancement_prs = []  # openshift/enhancements
            feature_prs = []   # Everything else (features, improvements, CI)

            for pr in self.prs:
                if pr['repo'] == 'openshift-eng/ai-helpers':
                    ai_helpers_prs.append(pr)
                elif pr['repo'] == 'openshift/enhancements':
                    enhancement_prs.append(pr)
                elif any(t.startswith('OCPBUGS') for t in pr.get('jiraTickets', [])):
                    bug_prs.append(pr)
                else:
                    feature_prs.append(pr)

            # Write each section
            self._write_section(f, "Bug Fixes (OCPBUGS)", bug_prs, show_sfdc=True)
            self._write_section(f, "Enhancements", enhancement_prs)
            self._write_section(f, "AI Helpers", ai_helpers_prs)
            self._write_section(f, "Features & Improvements", feature_prs)

        print(f"Report written to {output_path}")

    def _get_pr_sfdc_info(self, pr: Dict) -> Tuple[int, int, List[str]]:
        """Get SFDC case info for a PR from its Jira tickets.

        Returns:
            Tuple of (cases_total, cases_open, case_ids)
        """
        cases_total = 0
        cases_open = 0
        case_ids = []
        for ticket in pr.get('jiraTickets', []):
            if ticket in self.jira_hierarchy:
                info = self.jira_hierarchy[ticket]
                t = info.get('sfdcCasesTotal', 0)
                o = info.get('sfdcCasesOpen', 0)
                ids = info.get('sfdcCaseIds', [])
                if t > cases_total:
                    cases_total = t
                    cases_open = o
                    case_ids = ids
        return cases_total, cases_open, case_ids

    def _write_section(self, f, title: str, prs: List[Dict], show_sfdc: bool = False):
        """Write a report section with summary table and detailed PR list."""
        f.write(f"---\n\n## {title}\n\n")
        f.write(f"**{len(prs)} PRs**\n\n")

        if not prs:
            f.write("_No PRs in this category._\n\n")
            return

        # Summary table
        if show_sfdc:
            f.write("| PR | Author | Title | Priority | SFDC Cases | Cases Open | Case IDs |\n")
            f.write("|-----|--------|-------|----------|------------|------------|----------|\n")
        else:
            f.write("| PR | Author | Title | Priority | Repo |\n")
            f.write("|-----|--------|-------|----------|------|\n")

        sorted_prs = sorted(prs, key=lambda pr: pr['mergedAt'], reverse=True)

        for pr in sorted_prs:
            priority = self._get_pr_jira_priority(pr)
            title_text = self._linkify_jira_tickets(pr['title'][:80])
            repo_short = pr['repo'].split('/')[-1]
            pr_link = f"[#{pr['number']}]({pr['url']})"

            if show_sfdc:
                cases_total, cases_open, case_ids = self._get_pr_sfdc_info(pr)
                cases_str = str(cases_total) if cases_total else '-'
                open_str = str(cases_open) if cases_open else '-'
                ids_str = ', '.join(case_ids) if case_ids else '-'
                f.write(f"| {pr_link} | @{pr['author']} | {title_text} | {priority} | {cases_str} | {open_str} | {ids_str} |\n")
            else:
                f.write(f"| {pr_link} | @{pr['author']} | {title_text} | {priority} | {repo_short} |\n")

        f.write("\n")

        # Detailed PR list
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
                        # Show SFDC info if available
                        sfdc_total = info.get('sfdcCasesTotal', 0)
                        if sfdc_total:
                            sfdc_open = info.get('sfdcCasesOpen', 0)
                            sfdc_ids = info.get('sfdcCaseIds', [])
                            f.write(f"  - SFDC: {sfdc_total} cases ({sfdc_open} open)")
                            if sfdc_ids:
                                f.write(f" — Case IDs: {', '.join(sfdc_ids)}")
                            f.write("\n")
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

            # OCPSTRAT Impact
            impact = self._generate_impact_statement(pr)
            f.write(f"**OCPSTRAT Impact:** {impact}\n\n")

            # Labels
            if pr['labels']:
                f.write(f"**Labels:** {', '.join(pr['labels'])}\n")

            f.write("\n---\n\n")

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

            # Get SFDC info
            sfdc_total, sfdc_open, sfdc_ids = self._get_pr_sfdc_info(pr)

            result = {
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

            # Only include SFDC fields when there are cases
            if sfdc_total:
                result['sfdcCasesTotal'] = sfdc_total
                result['sfdcCasesOpen'] = sfdc_open
                result['sfdcCaseIds'] = sfdc_ids

            return result

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
                'enhancements': len([p for p in compact_prs if p['repo'] == 'enhancements']),
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

        # Enhancement proposals
        if pr['repo'] == 'openshift/enhancements':
            return 'enhancement'

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
        - Enhancement proposals (openshift/enhancements): +200 points (always selected)
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

            # Enhancement proposals are always high-priority for deep analysis
            if pr['repo'] == 'openshift/enhancements':
                score += 200
                reasons.append('enhancement')

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
            """Fetch diff for a single PR with pagination."""
            url = f"https://api.github.com/repos/{repo}/pulls/{number}/files"
            per_page = 100

            try:
                files = []
                page = 1
                while True:
                    params = {"per_page": per_page, "page": page}
                    if HAS_AIOHTTP:
                        async with session.get(url, headers=headers, params=params) as response:
                            if response.status != 200:
                                return {'repo': repo, 'number': number, 'error': f"HTTP {response.status}"}
                            batch = await response.json()
                    else:
                        response = requests.get(url, headers=headers, params=params, timeout=30)
                        if response.status_code != 200:
                            return {'repo': repo, 'number': number, 'error': f"HTTP {response.status_code}"}
                        batch = response.json()

                    files.extend(batch)
                    if len(batch) < per_page:
                        break
                    page += 1

                # Process file patches, skip vendor dirs, truncate large ones
                patches = []
                for f in files:
                    filename = f['filename']
                    dirs = set(filename.split('/')[:-1])
                    if 'vendor' in dirs:
                        continue
                    patch = f.get('patch', '')
                    if len(patch) > 5000:
                        patch = patch[:5000] + "\n... [truncated]"
                    patches.append({
                        'filename': filename,
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
    parser.add_argument(
        '--output-dir',
        default='/tmp',
        metavar='DIR',
        help='Directory for output files (default: /tmp)'
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
    output_dir = args.output_dir
    os.makedirs(output_dir, exist_ok=True)

    print(f"Generating PR report for: {since_date} to {end_date}")
    print(f"Using {'async (aiohttp)' if HAS_AIOHTTP else 'sync (requests)'} mode")
    if deep_prs:
        print(f"Deep mode: will fetch diffs for {len(deep_prs)} PRs")
    if score_mode:
        print(f"Score mode: will output top {score_limit} PRs by importance")
    print()

    generator = PRReportGenerator(since_date, end_date, output_dir=output_dir)

    # Fetch all data
    await generator.fetch_all_prs()
    await generator.load_jira_hierarchy()

    # Generate outputs
    generator.generate_report(os.path.join(output_dir, 'weekly_pr_report_fast.md'))
    generator.save_raw_data(os.path.join(output_dir, 'hypershift_pr_details_fast.json'))
    generator.save_summary_data(os.path.join(output_dir, 'hypershift_pr_summary.json'))

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

        scored_path = os.path.join(output_dir, 'pr_scored.json')
        with open(scored_path, 'w') as f:
            json.dump(scored_output, f, indent=2)
        print(f"Scored PRs saved to {scored_path}")

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

            # Print deep analysis summary
            successful = {k: v for k, v in diffs.items() if 'error' not in v}
            failed = {k: v for k, v in diffs.items() if 'error' in v}
            total_additions = sum(d.get('total_additions', 0) for d in successful.values())
            total_deletions = sum(d.get('total_deletions', 0) for d in successful.values())
            total_files = sum(d.get('total_files', 0) for d in successful.values())
            total_patches = sum(len(d.get('files', [])) for d in successful.values())
            vendor_skipped = total_files - total_patches

            print(f"\n  Deep analysis summary:")
            print(f"    PRs fetched:    {len(successful)}/{len(diffs)}")
            print(f"    Total files:    {total_files} ({vendor_skipped} vendor files skipped)")
            print(f"    Lines changed:  +{total_additions} -{total_deletions}")
            if failed:
                print(f"    Failed:         {', '.join(v.get('error', '?') for v in failed.values())}")

    elapsed = time.time() - start_time
    print(f"\nDone in {elapsed:.2f} seconds!")


if __name__ == '__main__':
    if HAS_AIOHTTP:
        asyncio.run(main())
    else:
        # Python 3.6 compatibility
        loop = asyncio.get_event_loop()
        loop.run_until_complete(main())
