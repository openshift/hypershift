# AWS Endpoint Service Controller

## Condition Message Stability

Errors returned from `reconcileAWSEndpointServiceStatus` are set directly as condition messages on `AWSEndpointService` resources (see `AWSEndpointServiceAvailable` condition). If an error message contains variable output (e.g., AWS request IDs, timestamps), the condition will flip on every reconciliation loop, causing unnecessary status updates and API churn.

Rules:
- **Never wrap raw AWS SDK errors with `%w` in return paths that feed condition messages.** Raw errors from the AWS SDK can contain request IDs and other per-call metadata that change on every invocation.
- **Use stable, deterministic error messages** for all returned errors. Include only fixed strings, error codes (`apiErr.ErrorCode()`), and deterministic identifiers (e.g., resource ARNs from input).
- **Log the full error for debugging, return a stable summary.** Use `log.Info(...)` with the full error before returning a sanitized message. This preserves debuggability without causing condition flapping.

Example:
```go
// Good: log full error, return stable API error code
log.Info("adoption failed", "err", adoptErr)
return "", "", errors.New(apiErr.ErrorCode())

// Bad: wraps potentially variable error into condition message
return "", "", fmt.Errorf("endpoint service adoption failed: %w", adoptErr)
```
