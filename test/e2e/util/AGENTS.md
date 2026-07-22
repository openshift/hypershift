# E2E Utility Package (`test/e2e/util`)

## Async Assertions

All asynchronous assertions that poll Kubernetes objects **must** use `EventuallyObject` or `EventuallyObjects` from `eventually.go`. Do **not** use raw `wait.PollUntilContextTimeout` with `t.Logf` inside the poll body.

### Why

Raw polling loops log on every iteration. A 20-minute timeout with a 10-second interval produces up to 120 identical log lines per object, drowning useful output. The `EventuallyObject` framework only logs when state **changes**, keeps output compact, and provides a structured failure summary at the end.

### Usage

```go
EventuallyObject(t, ctx, "DaemonSet kube-system/my-ds to be ready",
    func(ctx context.Context) (*appsv1.DaemonSet, error) {
        ds := &appsv1.DaemonSet{}
        err := client.Get(ctx, crclient.ObjectKey{Name: name, Namespace: ns}, ds)
        return ds, err
    },
    []Predicate[*appsv1.DaemonSet]{
        myPredicate(),
    },
    WithTimeout(20*time.Minute),
)
```

A `Predicate[T]` has signature `func(T) (done bool, reason string, err error)`. The `reason` is logged only when it differs from the previous poll cycle.

### When to use Gomega `Eventually`

Gomega's `Eventually` (from Ginkgo/Gomega) is acceptable for short-lived, non-object checks (e.g., verifying a function returns true within 30 seconds). For anything polling a Kubernetes object with a timeout longer than ~1 minute, prefer `EventuallyObject`/`EventuallyObjects`.
