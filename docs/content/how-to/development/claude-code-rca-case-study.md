# Using Claude Code for Complex Root Cause Analysis: A Case Study

**Investigation**: TestHAEtcdChaos Test Failure
**Date**: 2025-10-08
**Claude Code Version**: 2.0.5
**Model**: claude-sonnet-4-5@20250929
**Outcome**: False positive detection + 808 lines of documentation

---

## Overview

This document captures how Claude Code was used to investigate a complex test failure that turned out to be a false positive (test passing when it should fail). The investigation spanned multiple sessions, involved comparing artifacts from failed and successful runs, tracing code flow through multiple controllers, and resulted in comprehensive documentation updates.

**Key Success Factor**: Systematic prompting that guided the AI through hypothesis formation, evidence gathering, and iterative refinement.

### Investigation Setup

The investigation began with a failed Prow CI job:
- **Job**: `pull-ci-openshift-hypershift-main-e2e-aks/1975632729672257536`
- **PR**: openshift/hypershift#6883
- **Failure**: TestHAEtcdChaos timeout

**Artifact Collection**:
```bash
# Download artifacts from Prow using gcloud command (found on job artifact page)
gcloud storage cp -r \
  gs://test-platform-results/pr-logs/pull/openshift_hypershift/6883/pull-ci-openshift-hypershift-main-e2e-aks/1975632729672257536/artifacts/e2e-aks/hypershift-azure-run-e2e/ \
  .

# Place artifacts directory at repository root
# Directory structure:
# hypershift/
# ├── hypershift-azure-run-e2e/          # Failed run artifacts
# │   ├── build-log.txt
# │   └── artifacts/
# │       └── TestHAEtcdChaos/
# ├── hypershift-azure-run-e2e-success/  # Successful run (for comparison)
# ├── AGENTS.md
# └── ... rest of repo

# Start Claude Code from repository root with artifacts available
```

**Why This Setup Matters**:
- Having artifacts locally allows `@directory` references in prompts
- Comparison between failed and successful runs enabled differential analysis
- Repository context allows code tracing from test execution through controllers

---

## Initial Problem Statement

### User's First Prompt
```
analyze the test failure under @hypershift-azure-run-e2e/build-log.txt
using the collected artifacts under @hypershift-azure-run-e2e/ directory.
try to build a theory for the rootcause. think deeply.
```

### Why This Worked
- **Specific artifact references**: Used `@` to reference directories containing thousands of files
- **Open-ended analysis**: "build a theory" allowed exploration vs. jumping to conclusions
- **Emphasis on depth**: "think deeply" encouraged thorough investigation vs. surface-level answers
- **Context provided**: All artifacts from test run were available for reference

### Claude Code's Approach
1. Read build-log.txt (first 2000 lines) to find failure point
2. Identified timeout waiting for kubeconfig after 10 minutes
3. Systematically examined artifacts:
   - HostedCluster YAML (platform type, status conditions)
   - OAuth route resource (found empty `spec.host`)
   - Events and logs
4. Formed initial hypothesis: OAuth route hostname issue

**Tools Used**: Read, Bash (ls/find for directory exploration)

---

## Iterative Investigation Technique

### Pattern: Compare Success vs Failure

#### User's Prompt
```
explore the possibility that the problem was in external-dns delay or problem,
because you can find another test run, where TestHAEtcdChaos was running also
with Platform: None and it succeeded, under @hypershift-azure-run-e2e-success/
```

#### Why This Was Critical
- **Comparison-driven debugging**: Having both failed and successful runs is gold
- **Hypothesis testing**: User suggested external-DNS as potential cause
- **Claude Code verified by searching logs**: Found zero external-DNS references for the test cluster
- **Conclusion**: External-DNS doesn't process Platform=None routes at all

**Key Insight**: Claude Code searched external-DNS logs in BOTH runs to confirm absence, not just the failed run.

**Tools Used**: Grep (searching logs for cluster names), Read (examining route resources from both runs)

---

## Deep Code Tracing

### User's Prompt
```
how is the decision Pattern A or Pattern B made? where in the code is that decided?
```

### Claude Code's Systematic Trace
1. Started at test setup: `test/e2e/chaos_test.go`
2. Followed to platform creation: `cmd/cluster/none/create.go`
3. Traced route reconciliation: `support/util/route.go`
4. Found shared ingress processing: `sharedingress-config-generator/config.go`
5. Identified the gap: No `DEFAULT_INGRESS_DOMAIN` for Platform=None

**Technique**: Bottom-up code navigation with specific file:line references
- Example: "cmd/cluster/none/create.go:68-71"
- Provided exact code snippets with context

**Tools Used**: Grep (finding function definitions), Read (examining specific files), parallel reads of related files

---

## The Breakthrough: False Positive Detection

### User's Context
```
the test command in this test run looks like this [shows --e2e.external-dns-domain flag]
how come it still failed?
```

### Claude Code's Analysis Process
1. **Examined test command flags**: Found `--e2e.external-dns-domain` was set globally
2. **Checked actual test code**: `chaos_test.go` explicitly uses `Platform=None`
3. **Traced platform-specific logic**: None platform doesn't use external-dns-domain flag
4. **Compared with successful run**: Different test (TestAutoscaling) used Azure platform
5. **Checked validation history**: Found `ValidateConfigurationStatus` added in 4.21

**Breakthrough Realization**: The test was ALWAYS broken, but validation was skipped for Platform=None in earlier versions!

**Tools Used**: Grep (searching for version checks), Read (comparing validation functions across test/e2e/util/ files)

---

## Hypothesis Formation and Validation

### Evolution of Understanding

**Hypothesis 1**: External-DNS delay
- **Test**: Searched logs for cluster references
- **Result**: ❌ External-DNS doesn't process Platform=None routes

**Hypothesis 2**: Route creation timing issue
- **Test**: Compared route resources in failed vs successful runs
- **Result**: ❌ Routes created but with empty `spec.host`

**Hypothesis 3**: Shared ingress misconfiguration
- **Test**: Traced shared ingress config-generator code
- **Result**: ✅ Shared ingress SKIPS routes without hostnames (correct behavior)

**Hypothesis 4**: Missing DEFAULT_INGRESS_DOMAIN
- **Test**: Checked control-plane-operator deployment spec
- **Result**: ✅ Environment variable not set for Platform=None

**Final Hypothesis**: False positive - test was passing due to incomplete validation
- **Test**: Compared validation code before/after 4.21
- **Result**: ✅ CONFIRMED - Earlier versions skipped validation for Platform=None

**Key Technique**: Each hypothesis was tested with concrete evidence from artifacts or code

---

## Documentation Strategy

### User's Prompt
```
based on this RCA, are there any specific agent instructions for improving
a future Claude Code that you think are useful which can be added somewhere in the repo?
```

### Two-Phase Documentation Approach

**Phase 1**: Capture Investigation Knowledge
```
User: "document this in two aspects:
1. Detailed RCA for this session
2. Update docs and concepts about how HyperShift works"
```

**Phase 2**: Avoid Duplication
```
User: "can you double check that what you've added doesn't already exist somewhere in the repo?"
```

**Claude Code's Response**:
1. Searched existing docs with Grep
2. Found complementary (not duplicative) content
3. Added cross-references between docs
4. Updated existing docs rather than creating new ones where possible

**Result**: 5 files updated, 808 lines added, all cross-referenced

---

## Effective Prompting Patterns

### Pattern 1: Specific Artifact References
```
analyze the test failure under @hypershift-azure-run-e2e/build-log.txt
using the collected artifacts under @hypershift-azure-run-e2e/ directory
```
**Why it works**: Claude Code can directly access files via `@` mentions

### Pattern 2: Comparative Analysis
```
explore the possibility... because you can find another test run...
under @hypershift-azure-run-e2e-success/
```
**Why it works**: Comparison reveals differences that single-run analysis misses

### Pattern 3: Code Location Queries
```
how is the decision Pattern A or Pattern B made? where in the code is that decided?
```
**Why it works**: Forces systematic code tracing with specific file/line references

### Pattern 4: Verification Requests
```
double check if TestHAEtcdChaos had Platform set to None.
You can see the generated HostedCluster in the artifacts.
```
**Why it works**: Encourages evidence-based conclusions, not assumptions

### Pattern 5: Meta-Documentation Requests
```
based on this RCA, are there any specific agent instructions for improving
a future Claude Code that you think are useful?
```
**Why it works**: Captures knowledge for future investigations

### Pattern 6: Duplication Checks
```
can you double check that what you've added doesn't already exist somewhere in the repo?
```
**Why it works**: Prevents documentation bloat, encourages cross-referencing

---

## Tools and Techniques Used

### File Reading Strategy
- **Parallel reads**: Multiple `Read` tool calls in single message for comparison
- **Targeted reads**: Specific files based on code tracing, not exploratory reading
- **Artifact comparison**: Same file type from failed vs successful runs

### Code Search Strategy
- **Grep for patterns**: Function names, error messages, labels
- **Glob for file discovery**: Finding all files of specific type
- **Following references**: From test → creation → reconciliation → processing

### Analysis Techniques
- **Status condition analysis**: Always started with HostedCluster/HostedControlPlane status
- **Log correlation**: Searched logs for cluster-specific references
- **Version-gated validation**: Checked for `AtLeast(t, VersionXXX)` patterns
- **Platform-specific behavior**: Always checked platform type first

### Documentation Techniques
- **Mermaid diagrams**: Visual flow charts for complex processes
- **Code snippets with line numbers**: Exact references to source
- **Warning callouts**: Highlighted critical requirements
- **Cross-references**: Linked related documentation

---

## Key Success Factors

### What Made This Investigation Successful

1. **Artifact Availability**: Having both failed and successful test runs
2. **Systematic Approach**: Step-by-step hypothesis testing with evidence
3. **Code Tracing**: Following execution flow from test → creation → reconciliation
4. **Version Awareness**: Recognizing that validation logic changed over time
5. **Documentation Mindset**: Capturing knowledge as soon as patterns emerged

### Prompting Techniques That Worked

1. **Open-ended initial request**: "build a theory for the rootcause"
2. **Specific follow-up questions**: "where in the code is that decided?"
3. **Verification requests**: "double check if..."
4. **Comparative analysis**: "compare failed vs successful"
5. **Meta-documentation**: "what agent instructions would help?"

### Claude Code Strengths Demonstrated

1. **Parallel tool usage**: Reading multiple files simultaneously
2. **Code navigation**: Following references across multiple files
3. **Pattern recognition**: Identifying version-gated validations
4. **Documentation synthesis**: Creating comprehensive, cross-referenced docs
5. **Iterative refinement**: Building on previous findings without losing context

---

## Time Investment

### Conversation Flow
- **Initial investigation**: ~10 prompts exploring the failure
- **Code tracing**: ~5 prompts following execution paths
- **Hypothesis testing**: ~3 prompts testing external-DNS theory
- **Documentation**: ~5 prompts creating and refining docs
- **Total**: ~23 prompts over multiple hours

### Efficiency Gains
- **Without Claude Code**: Would require manual file searching, grep across thousands of files, reading multiple codebases
- **With Claude Code**: Systematic exploration with artifact references, parallel file reads, instant code navigation
- **Documentation**: Claude Code synthesized 808 lines of comprehensive docs from conversation context

---

## Lessons Learned

### What Worked Well

1. **Starting broad, narrowing down**: "analyze the test failure" → "check external-DNS" → "trace route creation"
2. **Comparison-driven debugging**: Always comparing failed vs successful
3. **Evidence-based conclusions**: Every hypothesis tested with artifacts or code
4. **Capturing knowledge immediately**: Documenting as soon as patterns emerged
5. **Cross-referencing**: Linking related docs instead of duplicating

### What Could Be Improved

1. **Earlier version comparison**: Could have checked OCP version differences sooner
2. **Platform type awareness**: Should have verified Platform=None assumption earlier
3. **Validation history**: Could have searched for test validation changes first

### Prompting Best Practices

**DO**:
- Use `@` to reference specific files/directories
- Ask for comparison between failed and successful runs
- Request specific file:line references in code explanations
- Ask for verification of assumptions
- Request meta-documentation to capture knowledge

**DON'T**:
- Make assumptions without verification
- Skip comparison when artifacts available
- Accept first hypothesis without testing alternatives
- Forget to document findings for future reference

---

## Reusable Investigation Workflow

### Step 1: Initial Analysis
```
Prompt: "analyze the test failure under @<failure-artifacts>/
using the collected artifacts. build a theory for the rootcause."
```

### Step 2: Comparative Analysis (if available)
```
Prompt: "compare with successful run under @<success-artifacts>/
to identify differences"
```

### Step 3: Code Tracing
```
Prompt: "where in the code is <observed-behavior> decided?
trace the execution flow"
```

### Step 4: Hypothesis Testing
```
Prompt: "check if <alternative-explanation> could be the cause"
```

### Step 5: Verification
```
Prompt: "double check <assumption> by examining <specific-artifact>"
```

### Step 6: Documentation
```
Prompt: "document this investigation and update existing docs
to reflect new knowledge"
```

### Step 7: Duplication Check
```
Prompt: "verify what you've added doesn't duplicate existing docs
and add cross-references"
```

---

## Outcome Summary

### Technical Results
- **Root cause identified**: False positive due to incomplete validation for Platform=None
- **Infrastructure bug found**: Routes created with empty `spec.host` cannot work with shared ingress
- **Fix options proposed**: Short-term (skip validation) and long-term (3 alternatives)

### Documentation Created
```
AGENTS.md                                          | +151 lines (troubleshooting methodology)
docs/content/how-to/aws/external-dns.md           | +17 lines (platform compatibility)
docs/content/how-to/common/exposing-services-from-hcp.md | +58 lines (route hostname patterns)
docs/content/reference/architecture/managed-azure/shared-ingress.md | +48 lines (route processing)
docs/content/reference/test-information-debugging/Azure/test-ha-etcd-chaos-rca.md | +534 lines (full RCA)
─────────────────────────────────────────────────────────────────────────────────
Total: 5 files, 808 insertions(+)
```

### Knowledge Captured
- Route hostname assignment patterns (Pattern A vs Pattern B)
- External-DNS vs Shared Ingress differences
- Platform=None limitations
- False positive detection methodology
- Troubleshooting workflow for future investigations

---

## Sharing This With Your Team

### For Developers
- Show them the **Reusable Investigation Workflow** section
- Demonstrate the **Prompting Patterns** that worked
- Highlight the **Code Tracing** technique for navigating complex codebases

### For QE/Test Engineers
- Focus on the **Comparative Analysis** approach
- Show the **False Positive Detection** methodology
- Emphasize **Artifact-driven investigation**

### For Technical Writers
- Review the **Documentation Strategy** section
- Note the **Cross-referencing approach**
- See how **Mermaid diagrams** enhanced explanations

### For Managers
- Review **Time Investment** vs traditional debugging
- See the **Outcome Summary** showing value delivered
- Note the **Knowledge Captured** for future reference

---

## Questions for Discussion

1. Should we standardize this investigation workflow for complex test failures?
2. How can we make artifact collection more consistent to enable comparison?
3. Should we create templates for RCA documentation based on this example?
4. What other use cases could benefit from this Claude Code approach?

---

## Appendix: Sample Prompts You Can Reuse

### For Test Failure Analysis
```
analyze the test failure under @<artifacts-dir>/build-log.txt
using the collected artifacts under @<artifacts-dir>/.
try to build a theory for the rootcause. think deeply.
```

### For Code Tracing
```
trace how <feature> is implemented from @<entry-point-file>
through the controllers to final reconciliation
```

### For Comparison Analysis
```
compare @<failed-run>/ with @<successful-run>/ and identify
key differences in configuration, status, or behavior
```

### For Documentation
```
based on this investigation, update existing docs to reflect
new knowledge. favor updating existing docs over creating new ones.
check for duplication and add cross-references.
```

### For Verification
```
double check <assumption> by examining the actual <resource-type>
in the artifacts at @<path>
```

---

**Key Takeaway**: Claude Code excels at systematic investigation when guided with specific prompts, comparative analysis, and iterative refinement. The conversation becomes a collaborative problem-solving session where the AI handles tedious file searching and code navigation while you provide strategic direction.

**Commit**: `14d23c9339` - Full investigation results documented and committed
**Model**: claude-sonnet-4-5@20250929 (Claude Code 2.0.5)
