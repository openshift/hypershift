# .tekton/lib - Pipeline Helper Library

Python modules shared across Tekton pipeline tasks in this repository.

## Why this exists

The `ho-release-gate` pipeline contains non-trivial logic: triggering and polling
Prow jobs via the Gangway API, building Slack Block Kit payloads, evaluating gate
verdicts. Keeping this logic inline in YAML task scripts makes the pipeline hard
to read, review, and maintain.

This library extracts reusable functions into Python modules so that each task's
inline script is a short orchestration stub (~10-25 lines) rather than a wall of
bash (~100-300 lines per task).

## Runtime environment

Pipeline tasks run in the **appstudio-utils** container image
(`quay.io/konflux-ci/appstudio-utils:latest`), managed by the Konflux team. This
image includes:

- Python 3 with the full **standard library**
- CLI tools: `oc`, `git`, `jq`, `curl`, etc.

The image does **not** include third-party Python packages such as `requests`,
`kubernetes`, `pyyaml`, or any pip-installed library.

## stdlib-only constraint

All modules in this directory use **only the Python standard library** (`urllib.request`, `urllib.parse`,
`json`, `subprocess`, `time`, `os`, `datetime`). This is a deliberate design choice:

1. **No `pip install` at runtime.** Konflux discourages runtime package installation
   to maintain hermetic, reproducible builds. Installing packages at runtime introduces
   network dependencies, version drift, and supply-chain risk.
2. **No custom container image.** Building and maintaining a custom image for a few
   helper functions adds CI/CD overhead (image builds, vulnerability scanning, registry
   management) disproportionate to the benefit.
3. **stdlib is sufficient.** `urllib.request` covers all our HTTP needs (Gangway API,
   Slack webhooks). `json` replaces `jq`. `subprocess` handles `oc` CLI calls.

If future requirements demand third-party libraries, the recommended path is to build
a custom image and update the task `image:` references.

## Module architecture

```
http_utils.py              (no deps, stdlib only)
    Low-level HTTP wrapper around urllib.request with retry logic.

prow_utils.py              -> http_utils
    Gangway API interactions: trigger jobs, resolve URLs, poll status.

slack_utils.py             -> http_utils
    Slack webhook: send messages, build Block Kit payloads.

kubearchive_utils.py       -> http_utils
    KubeArchive REST API: fetch historical PipelineRuns for stale
    promotion detection.

ho_release_gate.py         -> prow_utils, slack_utils, kubearchive_utils
    Pipeline-specific orchestration for the HO release gate:
    image extraction, job triggering/polling, gate evaluation,
    notifications (gate result, error, stale promotion alert).

tests/                     (unittest + unittest.mock, stdlib only)
    Unit tests for all modules. Run with:
    cd .tekton/lib && python3 -m unittest discover tests/ -v

mock/                      (test utilities)
    Drop-in mock functions for integration testing the pipeline
    without hitting real Gangway/KubeArchive APIs. Not used in
    production; temporarily swapped in during pipeline test runs.
```

## How the library reaches the tasks

The library is delivered to pipeline tasks via a **Tekton Workspace** backed by an
ephemeral PVC:

1. A `clone-lib` task runs first. It performs a sparse git checkout of only
   `.tekton/lib/` from `openshift/hypershift:main` and copies the files into the
   shared workspace.
2. Each subsequent task mounts the same workspace and adds it to `sys.path`:
   ```python
   import sys
   sys.path.insert(0, "$(workspaces.shared.path)")
   from prow_utils import trigger_prow_job
   ```
3. `finally` tasks also have access to the workspace (Tekton guarantees workspace
   availability in finally tasks).

The PVC is ephemeral (created per PipelineRun, deleted when the run completes) and
requires only ~10Mi of storage.
