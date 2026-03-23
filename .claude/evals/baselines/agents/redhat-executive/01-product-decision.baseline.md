# GPU Support in HyperShift NodePool API: Strategic Assessment

## My Position

**Do not build native GPU time-sharing or MIG support into the NodePool API.** This is a partner ecosystem play, not a platform play. Investing engineering cycles here would violate our core operating principles, weaken our partner leverage, and create a support burden that doesn't scale.

Let me walk you through why.

---

## The Strategic Case Against Building Native GPU/MIG in NodePool

### 1. This Violates the "Operate On, Not As" Principle

Red Hat operates *on* infrastructure — we don't *become* the infrastructure. GPU partitioning, time-sharing, and MIG configuration are **device-level concerns** that sit below our platform abstraction layer. The moment we bake MIG profiles into the NodePool API, we're taking ownership of:

- NVIDIA driver compatibility matrices (CUDA versions × kernel versions × GPU generations)
- AMD ROCm equivalents
- Intel Gaudi/Flex Series device plugin semantics
- Future silicon from custom cloud providers (AWS Trainium/Inferentia, Google TPUs)

That's a **combinatorial explosion** of hardware-specific support that fundamentally doesn't match our business model. We'd be writing silicon-specific code that changes every GPU generation — that's NVIDIA's job, not ours.

### 2. The Ecosystem Multiplier Works in Our Favor — Today

The NVIDIA GPU Operator already handles:
- MIG partition management
- GPU time-sharing via time-slicing and MPS
- Driver lifecycle (including pre-compiled driver containers for RHCOS)
- GPU feature discovery and node labeling
- DCGM monitoring integration

NVIDIA has **certified the GPU Operator on OpenShift.** This is exactly the ISV ecosystem flywheel we want: partners build on us, customers buy us *and* them, and our subscription value comes from being the **certified, supported platform** underneath. If we rebuild this functionality, we:

- Undercut a critical partner relationship (NVIDIA is the #1 AI infrastructure partner)
- Take on maintenance that NVIDIA funds with their own R&D budget
- Lose the "NVIDIA-certified on OpenShift" marketing, which is worth more than any feature we could build

### 3. Day 2 Supportability Would Be a Nightmare

Ask yourself: Can our SRE teams support MIG configuration across 10,000 ROSA HCP clusters running different GPU SKUs (A100, H100, H200, B200, MI300X) without linear headcount growth? **Absolutely not.**

GPU troubleshooting requires deep hardware expertise. When a customer's MIG partition fails or a time-shared GPU causes memory contention, is our L2 support team debugging NVIDIA driver issues? That's a support cost crater. Today, we can tell customers: "Open a case with NVIDIA for GPU Operator issues, open a case with us for OpenShift platform issues." Clean boundary. Clear accountability.

### 4. It Doesn't Strengthen Subscription Value

Our subscription value proposition is: **lifecycle management, security patching, certification, and 24/7 support for the platform.** GPU device management doesn't strengthen any of these pillars:

- **Lifecycle management**: GPU drivers follow NVIDIA's release cadence, not ours. We'd be perpetually chasing upstream.
- **Security patching**: GPU firmware/driver CVEs are NVIDIA's responsibility. Taking this on expands our vulnerability surface for zero customer benefit.
- **Certification**: We certify the *platform*; NVIDIA certifies the *operator*. That's the right division.

---

## What We SHOULD Do Instead

### Option A: First-Class GPU Scheduling Primitives (Recommended)

Invest engineering effort where we have legitimate platform differentiation:

1. **GPU-aware scheduling hints in NodePool**: Allow customers to specify GPU SKU requirements, topology constraints (e.g., NVLink affinity), and capacity reservations at the NodePool level. This is a *platform scheduling concern*, not a device management concern. It fits cleanly into our API.

2. **OpenShift AI + HyperShift integration**: Ensure that OpenShift AI model serving workloads can seamlessly target GPU-enabled NodePools in hosted clusters. The value is in the **workflow** — from model training to inference deployment on HCP — not in GPU partitioning.

3. **Node Feature Discovery (NFD) integration**: Ensure NFD labels for GPU capabilities are properly propagated in HCP topologies so that the NVIDIA GPU Operator and scheduler can make informed decisions.

4. **Resource quota and cost attribution**: Help customers understand GPU utilization and cost at the namespace/team level. This is a genuine platform gap that no device plugin solves.

### Option B: Strategic Partnership Deepening

5. **Co-engineer with NVIDIA on HCP-specific GPU Operator enhancements**: If there are gaps in how the GPU Operator works in a hosted control plane topology (e.g., operator lifecycle via OLM in the data plane, GPU node auto-repair), work *with* NVIDIA to close them upstream. This preserves our "upstream first" principle.

6. **Extend the ISV certification program**: Make it dead simple for NVIDIA, AMD, and Intel to certify their GPU operators on ROSA HCP and ARO HCP. Reduce their certification friction and we win by being the easiest platform to validate against.

---

## Competitive Lens

| Competitor | Their Approach | Our Counter |
|---|---|---|
| **EKS** | Native GPU instance types + Karpenter auto-provisioning | We can match GPU node scaling via Karpenter operator in HyperShift. Don't need MIG in our API. |
| **GKE** | GPU time-sharing as a first-class node pool feature | Google owns the full stack (TPUs, custom silicon). We can't and shouldn't compete on vertical integration — compete on **portability and multi-cloud**. |
| **AKS** | GPU node pools with NVIDIA device plugin | Same partner ecosystem approach we'd take. No differentiation in building it ourselves. |

The critical insight: **GKE is the only one that built native GPU sharing, because they have custom silicon (TPUs) they need to differentiate.** We don't have custom silicon. We have a platform story. Play to our strength.

---

## The AI Inference Angle

I know the counter-argument: "AI inference is the #1 workload growth driver. We need GPU features to win." I agree AI inference is critical — but the winning play is **OpenShift AI + RHEL AI + InstructLab**, not GPU plumbing in NodePool.

Customers choosing Red Hat for AI inference care about:
1. **Model portability** — Run the same model on-prem, on AWS, on Azure (our multi-cloud story)
2. **Governance and compliance** — Model provenance, data lineage, audit trails (OpenShift AI)
3. **Hardware flexibility** — Not locked to NVIDIA; can use AMD MI300X or Intel Gaudi (our hardware-agnostic story)

None of these require us to manage MIG partitions. All of them require us to have a great *platform* that GPU operators run on top of.

---

## Risks of NOT Building It

Let me be honest about the downside:

- **Sales friction**: Some enterprise RFPs may check a box for "native GPU management." Mitigation: position GPU Operator as "validated and supported on OpenShift" in our materials. It's effectively native.
- **Developer experience gap**: A developer using GKE can request GPU time-slicing in one YAML field. On OpenShift, they need to install the GPU Operator first. Mitigation: Include GPU Operator in default OperatorHub catalogs, provide one-click enablement in the console, or pre-install it in managed service offerings.
- **Perception of "falling behind"**: If GKE markets GPU sharing heavily, we look slow. Mitigation: Marketing is a messaging problem, not an engineering problem.

---

## Bottom Line

**Build the platform. Partner for the devices.**

Engineering effort should go toward:
- GPU-aware scheduling and topology hints in NodePool (platform concern ✅)
- OpenShift AI workflow integration (differentiated value ✅)
- Reducing GPU Operator certification friction on HCP (ecosystem multiplier ✅)
- Cost attribution and observability for GPU workloads (subscription value ✅)

Do **not** invest in:
- MIG partition management in NodePool API (device concern ❌)
- GPU time-sharing implementation (partner's job ❌)
- Driver lifecycle management (support cost crater ❌)

This preserves our engineering bandwidth for the things that actually drive ARR growth — platform differentiation, AI workflow integration, and multi-cloud portability — while letting NVIDIA, AMD, and Intel spend their R&D dollars on the device layer that makes our platform more valuable.
