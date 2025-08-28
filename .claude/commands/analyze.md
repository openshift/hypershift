---
description: "Analyze errors"
allowed-tools: ["Read", "Bash:curl"]
argument-hint: Pass URL of json file with errors
---

# General instructions
- Use curl to download the file with Kubernetes errors from $1
- Simplify errors in the JSON file
- Provide the most possible solution in a step by step style in no more than 280 characters.
- Do not aggregate multiple errors into one

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
              "Unmasked": "ip-10-0-137-33.ec2.internal",
              "Masked": "dTQ3eEM2K3VxJF1RSV1vfV5KRi1ZLHhYOip2"
            }
          ]
        },
        {
          "Text": "ip-10-0-137-33.ec2.internal has condition of type DiskPressure, reason NodeStatusUnknown: Kubelet stopped posting node status.",
          "KubernetesDoc": "",
          "Sensitive": [
            {
              "Unmasked": "ip-10-0-137-33.ec2.internal",
              "Masked": "XV4hcChubEwnJkJScz1HcmdvOlIqQm1weF0t"
            }
          ]
        }
      ]
    }
}
```