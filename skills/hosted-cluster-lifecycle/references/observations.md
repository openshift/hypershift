# Version-scoped AWS observations

These observations cover two distinct AWS HostedCluster creations and their
subsequent deletions on 28 October 2026. Each had two `m5.large` workers in
`us-east-1a`, public endpoints, SingleReplica policies and an existing healthy
HO on a warm development management cluster. HC 1 and HC 2 label the two
separate HostedClusters.

These are development examples only. They do not indicate expected startup or
teardown times for managed services such as ROSA HCP or ARO HCP, production
deployments, Azure, or other environments. They are not performance benchmarks,
deadlines, SLAs, or SLOs. Measure the target environment separately before
setting timing expectations or declaring a cluster stuck based on elapsed time.

- Release: `quay.io/openshift-release-dev/ocp-release:5.0.0-rc.7-multi`
- Resolved release digest: `sha256:502d2836c126b1a5c2f3f93cf6c43bbb9e0527530a0335ad8d3cbdb01bea8416`
- HO image: `quay.io/acm-d/rhtap-hypershift-operator@sha256:d7eb652dbbfac8ae9866b413c7125c86961a5cfb46396eb31774e0b3e171a3a5`
- HO source: `d1790a739bfc51d115968f11a93e66e87dc5ec7c`
- Release CPO source: `b9d6ae1c63453c012e889525eb924b3a07bac26b`

| Signal | HC 1 | HC 2 |
|--------|---------------|---------------|
| First HCP available | 4m32s | 3m57s |
| Control-plane rollout complete | 5m32s | 4m12s |
| NodePool ready | 9m40s | 9m28s |
| Full startup checklist observed | ~17m02s | ~17m02s |
| Destroy command returned | 7m37s | 7m20s |
| Teardown independently verified | ~7m40s | ~7m34s |

Neither startup required manual credential repair or bootstrap changes.
Both teardowns used the normal path without force or manual finalizer removal.
Do not convert these timings into deadlines, setup defaults or an Azure baseline.

Three observations change the next diagnostic action:

1. **Default worker SG gate:** at this CPO revision, the early AWS identity
   check records Unknown when KAS is unavailable but returns no error. The
   health-check controller then selects its five-minute successful-check
   interval. Identity validated around +5m34s and the worker SG followed.
   Verify the deployed revision and condition before inferring this timer;
   this was not a credential repair. Source: `controllers/healthcheck/aws.go`
   and `healthcheck_controller.go` under `control-plane-operator`.
2. **Monitoring bootstrap:** missing RBAC dependencies initially prevented
   Prometheus creation. A five-minute availability wait preceded the next
   reconciliation; Prometheus and monitoring then converged without manual
   changes. Check operand CRs and CMO tasks, not just running pods.
3. **Deletion:** HCP disappeared before the namespace and remaining API-server
   pods. HC 2 captured KAS's 135-second and OpenShift API servers' 120-second
   grace periods. Both traces also showed temporary internet-gateway dependency
   errors while NAT deletion progressed. These waits completed normally.

HC 2 first captured HCP availability at +3m57s; a later successful condition
carried +4m12s, also the rollout completion time. Both captured statuses were
True; an intervening False was not captured. Preserve earliest successful
transitions separately from latest conditions and final simultaneous readiness.
