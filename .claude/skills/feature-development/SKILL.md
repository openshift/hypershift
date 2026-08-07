---
name: feature-development
description: >
  Implement support for a new HCP feature using a multi-agent workflow that progresses
  through architecture design, control plane implementation, data plane implementation,
  cloud provider integration, and architect review. Use when the user wants to build a
  new platform feature end-to-end with specialized agents handling each layer.
---

# Feature Development Workflow

Orchestrate specialized subagents to implement a new HCP feature from design through
to deployment, with each agent building on the output of previous stages.

## Usage

```
/skill:feature-development <feature-description>
```

**Arguments:**
- `feature-description` (required): Description of the new platform feature to implement

## Workflow Stages

Delegate to specialized subagents in sequence. Each agent receives context from previous
agents to ensure coherent implementation.

### 1. HCP Architect Design

Delegate to an architect-focused subagent:

**Prompt:** "Design the API and main abstractions for supporting a new platform feature:
`<feature-description>`. Include API changes, CLI changes, and controller changes."

Save the API design and main abstractions for subsequent agents.

### 2. Control Plane Implementation

Delegate to a control-plane-focused subagent:

**Prompt:** "Implement the control plane changes needed to support the new feature:
`<feature-description>`. Use the design hints from the architect phase:
[include output from step 1]."

Include unit, integration, and e2e tests.

### 3. Data Plane Implementation

Delegate to a data-plane-focused subagent:

**Prompt:** "Implement the data plane changes needed to support the new feature:
`<feature-description>`."

Include unit, integration, and e2e tests.

### 4. Cloud Provider Integration

Delegate to a cloud-provider-focused subagent:

**Prompt:** "Review the control plane and data plane changes and implement any further
changes needed to support the new feature and ensure proper cloud integration:
`<feature-description>`. Add support to create a new HostedCluster in the new platform
via CLI."

Include unit, integration, and e2e tests.

### 5. HCP Architect Review

Delegate to an architect-focused subagent for final review:

**Prompt:** "Review the changes implemented by the other agents for supporting a new
feature: `<feature-description>`. [Include output from steps 2, 3, and 4].
Report feedback and suggest changes."

### 6. Aggregate Results

Combine results from all agents and present a unified implementation plan including:
- API design decisions and rationale
- Control plane changes with test coverage
- Data plane changes with test coverage
- Cloud provider integration with CLI support
- Architect review feedback and suggested improvements
