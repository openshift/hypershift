---
title: Contribute to HyperShift
---

# Contributing to HyperShift
Thanks for your interest in contributing to HyperShift. Here are some guidelines that help make the process more straightforward for everyone.

## Prior to Submitting a Pull Request
1. Prior to committing your code
   1. Install `precommit`. The HyperShift repo is set up to run `codespell` on your commits through `precommit`. Instructions for installing `precommit` can be found [here](https://pre-commit.com/#install).
   2. Run `make pre-commit`. This updates all Golang and API dependencies, builds the source code, builds the e2e tests, verifies source code format, and runs all unit tests. This will help catch issues before committing so that the verify and unit test CI jobs will not fail on your PR.
2. Before submitting your pull request on GitHub, look at your changes and try to view them from the eyes of a reviewer.
    1. Try to find the aspects that might not immediately make sense for someone else and explain them in the pull request description.
3. Keep commits/changes scoped to one thing and as minimal as possible.
    1. Always keep refactorings (how we do something) separate from logic changes (what we do).
    2. If you find additional things along the way that you feel should be improved, do that in a separate pull request.
    3. This helps ensure that you will get a timely review of your change, as a series of small pull requests is a lot easier to review than one big pull request that changes 10 independent things for independent reasons.
4. Use the [imperative mood](https://en.wikipedia.org/wiki/Imperative_mood) in the subject line for every commit, e.g. `Mark infraID as required` instead of `This patch marks infraID as required`.
    1. This follows Git’s own built-in conventions; see [github.com/openshift/hypershift/pull/485](https://github.com/openshift/hypershift/pull/485) as an example.
5. Make sure the "Why" and "How" are included in the message of each commit.

## Creating a Pull Request
1. For small changes, you can just do the change and submit a pull request with it.
2. For bigger changes (more than 200 lines of code diff), do not just do the change but, ask for feedback on the idea and direction of the change first (Either in a GitHub issue or the #project-hypershift channel in the External Red Hat Slack).
    1. This avoids situations where big changes are submitted that are then declined or never reviewed, which is frustrating for everyone.
3. Regardless of the size of the change, always explain how the change will improve the project.
4. Every PR title must be prefixed with the Jira ticket that is addressing e.g. [https://github.com/openshift/hypershift/pull/2233](https://github.com/openshift/hypershift/pull/2233).
5. This repository is the base code for the Hypershift Operator and the Control Plane Operator (belongs to the OCP payload) so they might have different release cadence.