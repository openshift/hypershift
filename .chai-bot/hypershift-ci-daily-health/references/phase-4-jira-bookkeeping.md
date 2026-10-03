# Phase 4 — Jira bookkeeping (after approval)

## Workflow

1. **Execute the approved blocker/flake changes.** Build the hierarchy `epic (dev cycle) →
   CNTRLPLANE story → CNTRLPLANE sub-task (per job/test) → linked OCPBUGS (per defect)`:
   - find or open the **current dev-cycle epic**;
   - find or open the per-release release-blocker / per-branch merge-queue / per-dev-cycle flake
     **CNTRLPLANE story** under it;
   - open a **CNTRLPLANE sub-task per job** (per test for flakes), created **unassigned**;
   - link **one OCPBUGS per distinct defect** from the affected job's sub-task (dedup a shared
     defect across jobs — one OCPBUGS linked from each).
2. **Apply the approved periodics-health lifecycle** (from Phase 3) under the per-release
   periodics-health story: **open** restore sub-tasks (newly breached; link the OCPBUGS cause
   where known), **nudge**, **escalate** to fix-or-retire, and **close** recovered ones.
3. **Post one reply** reporting the writes: the blocker/flake **nested tree** (story → sub-task →
   linked defect(s)) plus a **Periodics Health** group with the lifecycle outcomes, per the
   template below.
4. **Post an updated Team Action Items list.** End the reply with a fresh `Team Action Items
   (updated)` section reflecting the post-approval state — completed items led by `:check:`,
   still-open human items as plain bullets. You **MUST NOT** edit the Phase 1 message in place;
   the current worklist is re-posted here.

## Message format

Styling follows the main prompt's Slack rules (mrkdwn styling + Markdown links, an emoji on
every section header, nested `*` sub-bullets). Every issue reference is a Markdown link with the
ID **inside** the label: `[[CNTRLPLANE-…] <short title>](url)` / `[[OCPBUGS-…] <signature>](url)`
— never a bare `[ID]` bracket. Release/branch are **bold sub-labels** (no emoji). Legend:

| Header | Shortcode |
|---|---|
| Jira Updated | `:jira-6472:` |
| Release blockers (per release) | `:openshift:` |
| Merge-queue blockers (per branch) | `:pr-open:` |
| Flaky tests (per dev cycle) | `:snowflake:` |
| Periodics Health (SLO lifecycle) | `:thermometer:` |
| Team Action Items (updated) | `:done-circle-check:` |

### Template

```
:jira-6472: *Jira Updated*   _under epic [[CNTRLPLANE-…] dev cycle <release>](https://issues.redhat.com/browse/CNTRLPLANE-…)_

*:openshift: Release blockers*
* *OCP <release>* — [[CNTRLPLANE-…] release-blocker story](https://issues.redhat.com/browse/CNTRLPLANE-…)
  * [[CNTRLPLANE-…] <job>](https://issues.redhat.com/browse/CNTRLPLANE-…)
    * [[OCPBUGS-…] <signature>](https://issues.redhat.com/browse/OCPBUGS-…)

*:pr-open: Merge-queue blockers*
* *<branch>* — [[CNTRLPLANE-…] merge-queue story](https://issues.redhat.com/browse/CNTRLPLANE-…)
  * [[CNTRLPLANE-…] <job>](https://issues.redhat.com/browse/CNTRLPLANE-…)
    * [[OCPBUGS-…] <signature>](https://issues.redhat.com/browse/OCPBUGS-…)
    * [[OCPBUGS-…] <signature>](https://issues.redhat.com/browse/OCPBUGS-…)

*:snowflake: Flaky tests*
* [[CNTRLPLANE-…] flake story](https://issues.redhat.com/browse/CNTRLPLANE-…)
  * [[CNTRLPLANE-…] <test>](https://issues.redhat.com/browse/CNTRLPLANE-…)
    * [[OCPBUGS-…] <signature>](https://issues.redhat.com/browse/OCPBUGS-…)

*:thermometer: Periodics Health*
* *OCP <release>*
  * :new: opened [[CNTRLPLANE-…] restore <periodic>](https://issues.redhat.com/browse/CNTRLPLANE-…) (cause [[OCPBUGS-…]](https://issues.redhat.com/browse/OCPBUGS-…))
  * :green-up-arrow: closed [[CNTRLPLANE-…] restore <periodic>](https://issues.redhat.com/browse/CNTRLPLANE-…) — recovered

*:done-circle-check: Team Action Items (updated)*
* :check: <what was opened — brief> (links above).
* <still-open human item, e.g. declare/handle the incident>.
```

### Filled example

```
:jira-6472: *Jira Updated*   _under epic [[CNTRLPLANE-9000] dev cycle 5.1](https://issues.redhat.com/browse/CNTRLPLANE-9000)_

*:openshift: Release blockers*
* *OCP 4.20* — [[CNTRLPLANE-11001] release-blocker story](https://issues.redhat.com/browse/CNTRLPLANE-11001)
  * [[CNTRLPLANE-11002] e2e-aks](https://issues.redhat.com/browse/CNTRLPLANE-11002)
    * [[OCPBUGS-88801] AKS API EOF on ValidateHostedCluster](https://issues.redhat.com/browse/OCPBUGS-88801)
* *OCP 4.22* — [[CNTRLPLANE-11003] release-blocker story](https://issues.redhat.com/browse/CNTRLPLANE-11003)
  * [[CNTRLPLANE-11004] e2e-v2-aws](https://issues.redhat.com/browse/CNTRLPLANE-11004)
    * [[OCPBUGS-88802] component-readiness gate failing](https://issues.redhat.com/browse/OCPBUGS-88802)

*:pr-open: Merge-queue blockers*
* *main* — [[CNTRLPLANE-11010] merge-queue story](https://issues.redhat.com/browse/CNTRLPLANE-11010)
  * [[CNTRLPLANE-11011] e2e-aws](https://issues.redhat.com/browse/CNTRLPLANE-11011)
    * [[OCPBUGS-88810] machine-config-controller exit 255](https://issues.redhat.com/browse/OCPBUGS-88810)
  * [[CNTRLPLANE-11012] e2e-aws-upgrade-hypershift-operator](https://issues.redhat.com/browse/CNTRLPLANE-11012)
    * [[OCPBUGS-88810] machine-config-controller exit 255](https://issues.redhat.com/browse/OCPBUGS-88810)
  * [[CNTRLPLANE-11013] e2e-v2-azure-self-managed](https://issues.redhat.com/browse/CNTRLPLANE-11013)
    * [[OCPBUGS-88812] control plane never reconciles Available](https://issues.redhat.com/browse/OCPBUGS-88812)
    * [[OCPBUGS-88816] Azure DNS record provisioning flake](https://issues.redhat.com/browse/OCPBUGS-88816)
* *release-4.20* — [[CNTRLPLANE-11020] merge-queue story](https://issues.redhat.com/browse/CNTRLPLANE-11020)
  * [[CNTRLPLANE-11021] e2e-kubevirt-aws-ovn-reduced](https://issues.redhat.com/browse/CNTRLPLANE-11021)
    * [[OCPBUGS-88820] console operator unavailable in 4.20 CI payload](https://issues.redhat.com/browse/OCPBUGS-88820)

*:snowflake: Flaky tests*
* [[CNTRLPLANE-11030] flake story](https://issues.redhat.com/browse/CNTRLPLANE-11030)
  * [[CNTRLPLANE-11031] Build image hypershift-cli from the repository](https://issues.redhat.com/browse/CNTRLPLANE-11031)
    * [[OCPBUGS-88830] CI registry 500 on manifest push](https://issues.redhat.com/browse/OCPBUGS-88830)

*:thermometer: Periodics Health*
* *OCP 4.21*
  * :new: opened [[CNTRLPLANE-12101] restore e2e-azure](https://issues.redhat.com/browse/CNTRLPLANE-12101)
  * :hourglass: escalated [[CNTRLPLANE-12010] restore e2e-metal](https://issues.redhat.com/browse/CNTRLPLANE-12010) — fix-or-retire
* *OCP 5.1*
  * :green-up-arrow: closed [[CNTRLPLANE-11990] restore e2e-aws-conformance](https://issues.redhat.com/browse/CNTRLPLANE-11990) — recovered

*:done-circle-check: Team Action Items (updated)*
* :check: Opened OCPBUGS + CNTRLPLANE for the 4.20 and 4.22 release blockers and the main / release-4.20 merge-queue breaks (links above).
* Declare and handle the incident for 4.20, 4.22, and release-4.20 (human).
* Decide fix-or-retire for e2e-metal (4.21) (human).
* Assign the new CNTRLPLANE sub-tasks (RITS).
```
