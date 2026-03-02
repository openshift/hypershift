## Architectural Assessment: Worker Nodes → Management Cluster Webhook

**This design violates several core HyperShift architectural principles. I would strongly recommend against it.** Let me walk through the specific concerns:

---

### 1. Violation: Communication Directionality

> *"Communication between management cluster and a hosted cluster should be unidirectional"*

The foundational communication model in HyperShift is **management → hosted cluster**, not the reverse. Worker nodes in the data plane should never initiate connections back to the management cluster. This is a hard architectural boundary, not a guideline.

A webhook from worker nodes to the hypershift-operator creates a **reverse data flow** — the data plane is now pushing into the management plane. This breaks the unidirectional contract.

### 2. Violation: Worker Nodes Running Non-User Workloads

> *"Compute worker Nodes should not run anything beyond user workloads"*

For worker nodes to call a management-side REST endpoint, you'd need an agent or sidecar running on those nodes — something that collects health metrics and POSTs them to the webhook. This is management-plane infrastructure running on user compute, which violates the worker node isolation principle.

### 3. Violation: Data Plane Changes Triggering Management-Side Actions

> *"Changes to anything running on the data plane should not trigger a lifecycle action on components running management side"*

A webhook endpoint on the hypershift-operator that receives node health data and potentially reacts to it (alerting, scaling decisions, remediation) is exactly this — data plane state changes driving management-side behavior through a direct coupling.

### 4. Security & Network Exposure Concerns

> *"Each control plane namespace should be as much isolated as possible via networking and linux container primitives"*

Exposing a REST endpoint on the hypershift-operator that worker nodes can reach requires:
- **Network path from data plane to management plane** — this is a significant security surface expansion
- **Authentication/authorization** — every worker node needs credentials to call the management cluster, meaning management-plane credentials on user compute
- **Per-cluster endpoint routing** — the webhook needs to map incoming calls to the correct HostedCluster/NodePool, adding complexity and attack surface

This also potentially conflicts with:
> *"HyperShift components should not own or manage user infrastructure platform credentials"*

...since the authentication tokens for this webhook become a shared concern.

### 5. Scaling Concerns

If you have thousands of hosted clusters with hundreds of nodes each, every node is now making periodic REST calls to the hypershift-operator. This creates a thundering herd problem and makes the hypershift-operator a bottleneck for data-plane observability across all clusters.

---

## Recommended Alternatives

### Option A: Control Plane Proxy Pattern (Preferred)

The health data stays within the hosted cluster's control plane namespace on the management side, respecting the existing communication model:

```
┌─────────────────────┐
│   Worker Nodes      │
│  (data plane)       │
│                     │
│  kubelet reports    │
│  node status to     │
│  hosted API server  │
└────────┬────────────┘
         │ (standard kubelet → API server)
         ▼
┌─────────────────────────────┐
│  Hosted Control Plane       │
│  (management cluster,       │
│   CP namespace)             │
│                             │
│  ┌─────────────────────┐    │
│  │ Hosted API Server   │    │
│  │ (has Node status)   │    │
│  └────────┬────────────┘    │
│           │                 │
│  ┌────────▼────────────┐    │
│  │ CPO / Health        │    │
│  │ Controller          │    │
│  │ (watches Nodes,     │    │
│  │  writes status to   │    │
│  │  HostedCluster CR)  │    │
│  └─────────────────────┘    │
└─────────────────────────────┘
         │
         ▼ (status subresource update)
┌─────────────────────┐
│ hypershift-operator  │
│ reads HC status      │
│ (standard reconcile) │
└──────────────────────┘
```

**How it works:**
- Worker nodes report to the **hosted API server** via standard kubelet mechanisms (already happens)
- A controller within the **control-plane-operator** (running inside the CP namespace) watches Node objects on the hosted cluster and aggregates health metrics
- It writes a summary to the `HostedCluster.status` or a dedicated `ConfigMap`/`Condition`
- The hypershift-operator reads this through its normal reconciliation loop

**This respects all principles:**
- ✅ Unidirectional communication (management side reads from its own namespace)
- ✅ Worker nodes run only user workloads
- ✅ No data plane → management plane network path
- ✅ Isolation per control plane namespace
- ✅ No new credentials on worker nodes

### Option B: Metrics Pull via Hosted Cluster API

If you need richer health data than Node conditions provide:

- Deploy a health aggregator as a **hosted cluster control plane component** (runs in the CP namespace, not on worker nodes)
- It queries the hosted cluster's metrics endpoint or Node API
- Exposes aggregated health status via the HostedCluster status conditions
- The hypershift-operator consumes this through standard reconciliation

---

## Summary

| Concern | Webhook Design | Recommended Alternative |
|---|---|---|
| Communication direction | ❌ Reverse (data→mgmt) | ✅ Unidirectional |
| Worker node purity | ❌ Requires agent on nodes | ✅ No data plane agents |
| Data plane → mgmt coupling | ❌ Direct trigger | ✅ Status-based, reconcile loop |
| Network security | ❌ New attack surface | ✅ No new network paths |
| Credential management | ❌ Mgmt creds on workers | ✅ No new credentials |
| Scalability | ❌ O(nodes) webhook calls | ✅ O(clusters) reconciles |

**Bottom line:** Use the hosted API server and CPO as the bridge. The data is already flowing through the right channels — kubelet reports Node status to the hosted API server, which lives in the management cluster's CP namespace. A controller in CPO can aggregate and surface this data without breaking any architectural boundaries.
