#!/usr/bin/env python3
"""Exercise the Dependabot workflow's title handler with GitHub API responses."""

import json
import os
import subprocess
import unittest
from pathlib import Path
from unittest.mock import patch

import yaml


WORKFLOW = Path(__file__).resolve().parents[2] / ".github/workflows/dependabot-pr-title-reusable.yaml"
SCRIPT = yaml.safe_load(WORKFLOW.read_text())["jobs"]["prefix-title"]["steps"][0]["run"]
ENDPOINT = "repos/openshift/hypershift/pulls/123"


class TestDependabotPRTitle(unittest.TestCase):
    def run_handler(self, title, login="dependabot[bot]", user_id=49699333):
        pull = {
            "title": title,
            "user": {"login": login, "id": user_id},
            "head": {"sha": "unchanged-head"},
        }
        with (
            patch.dict(os.environ, {"GH_REPO": "openshift/hypershift", "PR_NUMBER": "123"}),
            patch.object(subprocess, "check_output", return_value=json.dumps(pull)) as get,
            patch.object(subprocess, "run") as update,
        ):
            try:
                exec(compile(SCRIPT, str(WORKFLOW), "exec"), {})
            except SystemExit as exit_result:
                self.assertEqual(exit_result.code, 0)
            get.assert_called_once_with(["gh", "api", ENDPOINT])
        return update

    def test_prefixes_title_without_changing_other_pr_fields(self):
        for title in (
            "build(deps): bump the k8s-dependencies group",
            "ci(deps): bump actions/checkout from 6 to 7",
            "ci(deps): bump actions-runner",
            'build(deps): bump "quoted" $(touch /tmp/unexpected) `command` $HOME',
        ):
            with self.subTest(title=title):
                update = self.run_handler(title)
                update.assert_called_once_with(
                    ["gh", "api", "--method", "PATCH", ENDPOINT, "--input", "-"],
                    input=json.dumps({"title": f"NO-JIRA: {title}"}),
                    text=True,
                    check=True,
                )

    def test_repeated_events_do_not_update_an_already_prefixed_title(self):
        for title in (
            "NO-JIRA: build(deps): bump dependencies",
            "NO-JIRA:build(deps): bump dependencies",
            "NO-JIRA:",
        ):
            with self.subTest(title=title):
                self.run_handler(title).assert_not_called()

    def test_restores_prefix_after_dependabot_resets_the_title(self):
        title = "build(deps): bump dependencies"
        self.run_handler(title).assert_called_once()
        self.run_handler(f"NO-JIRA: {title}").assert_not_called()
        self.run_handler(title).assert_called_once()

    def test_other_authors_cannot_change_titles(self):
        for login, user_id in (
            ("human", 123),
            ("renovate[bot]", 29139614),
            ("dependabot[bot]", 123),
            ("human", 49699333),
        ):
            with self.subTest(login=login, user_id=user_id):
                self.run_handler("build(deps): bump dependencies", login, user_id).assert_not_called()


if __name__ == "__main__":
    unittest.main()
