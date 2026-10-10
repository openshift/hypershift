# Catalog Health Probes

The certified, community, and Red Hat catalog `registry` containers use separate
exec probes defined in their deployment assets. All three probes run:

```text
grpc_health_probe -addr=:50051 -connect-timeout=1s -rpc-timeout=2s
```

The kubelet timeout is 5 seconds: 1 second for connecting, 2 seconds for the
health RPC, and 2 seconds of headroom for exec startup and CPU scheduling.
The previous 1-second liveness and startup timeouts could terminate the probe
under CPU contention before a healthy server's response was observed. Readiness
keeps its existing 5-second kubelet timeout. All deadlines remain finite, and
each successful probe still requires a healthy gRPC response.

The startup failure threshold increases from 15 to 120 at the unchanged
10-second period, extending the startup allowance from approximately 2.5 minutes
to 20 minutes. Liveness and readiness remain gated on startup success; their
steady-state failure thresholds remain 3. Probe periods, initial delays, and
success thresholds are unchanged.

The deployment sets `progressDeadlineSeconds: 1800` rather than relying on the
600-second default, which is shorter than the 1,200-second startup allowance.
This provides 20 minutes for startup plus 10 minutes of headroom for scheduling,
image pulls, and init containers before a lack of deployment progress is
reported as `ProgressDeadlineExceeded`.

CPU and memory requests and limits, images, TLS configuration, the health
endpoint, and the exec probe kind are unchanged. Applying the updated CPO rolls
these three catalog deployments because their pod templates change; it does not
change API-serving workloads or NodePool configuration.
