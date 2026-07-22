---
name: e2e-analyze
description: >
  Analyze e2e test failures from CI runs. Use when a CI job has failed and you need to
  download artifacts, identify the root cause of a specific test failure, and produce a
  structured failure analysis with evidence. Requires a CI run URL, test name, and an
  existing local directory for storing artifacts.
---

# Analyze E2E Test Failures

Download and analyze CI test artifacts to determine the root cause of an e2e test failure.

## Usage

```
/skill:e2e-analyze <ci-run-url> <test-name> <artifact-directory>
```

**Arguments:**
- `ci-run-url` (required): HTTPS URL of the CI run (e.g., a Prow job URL)
- `test-name` (required): Name of the failing test to analyze
- `artifact-directory` (required): Path to an **existing** local directory where artifacts will be stored

## Procedure

### 1. Validate inputs

- Confirm the artifact directory exists. **Do not create it** — fail if missing.
- Extract `{BUILD_NUMBER}` from the CI URL by matching a 10–20 digit sequence. Fail if none is found.
- Only allow HTTPS URLs (reject HTTP).

### 2. Download the build log

```bash
curl -fsSL --max-time 20 --retry 3 --retry-connrefused \
  --proto '=https' --max-filesize 100M \
  "${CI_RUN_URL}/build-log.txt"
```

### 3. Analyze the failure

- Parse `build-log.txt` for failures related to the specified test name.
- Use `gcloud storage` to fetch relevant artifacts into the artifact directory, incorporating `{BUILD_NUMBER}` in URLs.
- Gather primary evidence for the failure.
- Search for additional evidence in logs and events.
- **Do not delete downloaded artifacts** after analysis.

## Output Format

```text
Error: {Error message here}
Summary: {Failure analysis here}
Evidence: {Evidence here}
Additional evidence: {Additional evidence here}
```
