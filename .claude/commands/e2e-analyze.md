---
description: "Analyze test errors"
allowed-tools: ["Read", "Bash:curl", "Bash:gcloud"]
argument-hint: Pass URL of a CI run as $1 and the test name as $2
---

# General instructions

- Validate that "$1" includes a 19-digit number.
- Use the 19-digit number from "$1" as variable {BUILD_NUMBER}.
- Use curl to download the file build-log.txt under "$1". Only allow HTTPS URLs (reject HTTP).
- When fetching, use: `curl -fsSL --max-time 20 --retry 3 --retry-connrefused --proto '=https' --max-filesize 100M "$1"`.
- The build-log.txt file contains e2e failures, store the artifacts related to failure "$2" under /tmp/ci-artifacts/{BUILD_NUMBER} to determine a possible root cause for the failure.
- Use "gcloud storage" command to fetch given artifacts and make sure to use {BUILD_NUMBER} in URLs.
- Provide evidence for the failure.
- Try to find additional evidence. For example, in logs and events.
- Do not delete downloaded artifacts in cleanup phase.

# Output format

- The output should be formatted as:
  ```
  Error: {Error message here}
  Summary: {Failure analysis here}
  Evidence: {Evidence here}
  Additional evidence: {Additional evidence here}
  ```
