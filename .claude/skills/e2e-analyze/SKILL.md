---
name: e2e-analyze
description: "Analyze test errors"
---

# General instructions

- Validate that the artifact directory argument is an existing directory; fail if it is not. Do NOT create the directory.
- Extract {BUILD_NUMBER} from the CI run URL argument by matching a 10–20 digit sequence; fail if none is found.
- Use the extracted number as {BUILD_NUMBER}.
- Use curl to download the file build-log.txt under the CI run URL. Only allow HTTPS URLs (reject HTTP).
- When fetching, use: `curl -fsSL --max-time 20 --retry 3 --retry-connrefused --proto '=https' --max-filesize 100M "<ci-run-url>/build-log.txt"`.
- The build-log.txt file contains e2e failures, store the artifacts related to the specified test name under the artifact directory to determine a possible root cause for the failure.
- Use the "gcloud storage" command to fetch given artifacts under the artifact directory and make sure to use {BUILD_NUMBER} in URLs.
- Provide evidence for the failure.
- Try to find additional evidence. For example, in logs and events.
- Do not delete downloaded artifacts during the cleanup phase.

# Output format

- The output should be formatted as:
  ```text
  Error: {Error message here}
  Summary: {Failure analysis here}
  Evidence: {Evidence here}
  Additional evidence: {Additional evidence here}
  ```
