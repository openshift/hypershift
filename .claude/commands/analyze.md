---
description: "Analyze errors"
allowed-tools: ["Read", "Bash:curl"]
argument-hint: Pass URL of json file with errors
---

# General instructions
- Use curl to download the file with Kubernetes errors from $1. Only allow HTTPS URLs (reject http).
- When fetching, use: curl -fsSL --max-time 20 --retry 3 --retry-connrefused --proto '=https' --max-filesize 10M "$1".
- Validate that the payload is JSON; if not, abort with a short error.
- Abort if Content-Type is not application/json.
- Simplify errors in the JSON file.
- Provide the most possible solution in a step by step style in no more than 280 characters per error.
- Never print values from Sensitive.Unmasked; use Sensitive.Masked or redact entirely.
- Do not aggregate multiple errors into one.
- Prefer non-destructive steps first; include one verify step after each fix (e.g., kubectl get/describe, rollout status).
- Do not execute kubectl/cloud commands; only suggest them with clear verification steps.

# Output format

- Create separate section for each Kind
- Each error should be formatted as:
  Kind/Namespace/Name: {Copy kind and name here}
  Error: {Explain error here}
  Solution: {Step by step solution here as bullets}

# Example input JSON

```json
{
  "provider": "",
  "errors": null,
  "status": "ProblemDetected",
  "problems": 276,
  "results": [
    {
      "kind": "Node",
      "name": "ip-10-0-137-33.ec2.internal",
      "error": [
        {
          "Text": "ip-10-0-137-33.ec2.internal has condition of type MemoryPressure, reason NodeStatusUnknown: Kubelet stopped posting node status.",
          "KubernetesDoc": "",
          "Sensitive": [
            {
              "Unmasked": "[REDACTED]",
              "Masked": "dTQ3eEM2K3VxJF1RSV1vfV5KRi1ZLHhYOip2"
            }
          ]
        },
        {
          "Text": "ip-10-0-137-33.ec2.internal has condition of type DiskPressure, reason NodeStatusUnknown: Kubelet stopped posting node status.",
          "KubernetesDoc": "",
          "Sensitive": [
            {
              "Unmasked": "[REDACTED]",
              "Masked": "XV4hcChubEwnJkJScz1HcmdvOlIqQm1weF0t"
            }
          ]
        }
      ]
    }
  ]
}
```