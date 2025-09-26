---
description: "Analyze test errors"
allowed-tools: ["Read", "Bash(curl)", "Bash(gcloud)", "Bash(echo:*)", "Bash(ls:*)", "Bash(find:*)", "Bash(grep:*)", "Bash(test:*)"]
argument-hint: Pass URL of a CI run as $1, the test name as $2 and target directory for artifacts as $3
---

# General instructions

- Validate that "$3" is an existing directory; fail if it is not. Do NOT create the directory.
- Extract {BUILD_NUMBER} from "$1" by matching a 10–20 digit sequence; fail if none is found.
- Use the extracted number as {BUILD_NUMBER}.
- Use curl to download the file build-log.txt under "$1". Only allow HTTPS URLs (reject HTTP).
- When fetching, use: `curl -fsSL --max-time 20 --retry 3 --retry-connrefused --proto '=https' --max-filesize 100M "${1}/build-log.txt"`.
- The build-log.txt file contains e2e failures, store the artifacts related to failure "$2" under directory specified by "$3" to determine a possible root cause for the failure.
- Use the "gcloud storage" command to fetch given artifacts under "$3" and make sure to use {BUILD_NUMBER} in URLs.
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
