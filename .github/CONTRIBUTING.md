---
title: Contribute to HyperShift
---

# Contributing to HyperShift
Thank you for your interest in contributing to HyperShift! HyperShift enables running multiple OpenShift control planes as lightweight, cost-effective hosted clusters. Your contributions help improve this critical infrastructure technology.

The following guidelines will help ensure a smooth contribution process for both contributors and maintainers.

## Prior to Submitting a Pull Request
1. **Keep changes focused**: Scope commits to one thing and keep them minimal. Separate refactoring from logic changes, and save additional improvements for separate PRs.

2. **Test your changes**: Run `make pre-commit` to update dependencies, build code, verify formatting, and run tests. This prevents CI failures on your PR.

3. **Review before submitting**: Look at your changes from a reviewer's perspective and explain anything that might not be immediately clear in your PR description.

4. **Use proper commit format**: 
    1. Write commit subjects in [imperative mood](https://en.wikipedia.org/wiki/Imperative_mood) (e.g., "Fix bug" not "Fixed bug")
    2. Follow [conventional commit format](https://www.conventionalcommits.org/) and include "Why" and "How" in commit messages

!!! tip "Install precommit hooks"
    Install `precommit` to automatically catch issues before committing. This helps catch spelling mistakes, formatting issues, and test failures early in your development process.
    
    * [Installation instructions](https://pre-commit.com/#install)
    * [HyperShift-specific tips](./precommit-hook-help.md)

## Creating a Pull Request
1. **For small changes** (under 200 lines): Create your change and submit a pull request directly.

2. **For larger changes** (200+ lines): Get feedback on your approach first by opening a GitHub issue or posting in the #project-hypershift Slack channel. This prevents situations where large changes get declined after significant work.

3. **Write a clear PR title**: Prefix with your Jira ticket number (e.g., "OCPBUGS-12345: Fix memory leak in controller"). See [example PR](https://github.com/openshift/hypershift/pull/2233).

4. **Open the PR in draft mode**: Use `/auto-cc` to assign reviewers to your PR in draft mode. Keep the PR in draft mode until:

    - all the required labels are on the PR
    - all required tests are passing

5. **Explain the value**: Always describe how your change improves the project in the PR description.

!!! note "Release Information"
    This repository contains code for both the HyperShift Operator and Control Plane Operator (part of OCP payload), which may have different release cadences.