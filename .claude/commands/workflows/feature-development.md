---
model: opus
---

Implement support for a new HCP feature using specialized agents with explicit Task tool invocations:

[Extended thinking: This workflow orchestrates multiple specialized agents to implement a new HCP feature from design to deployment. Each agent receives context from previous agents to ensure coherent implementation.]

Use the Task tool to delegate to specialized agents in sequence:

1. **HCP architect design**
   - Use Task tool with subagent_type="hcp-architect-sme" 
   - Prompt: "Design the API and main abstractions for supporting a new platform feature: $ARGUMENTS. Include API changes, cli changes and controller changes"
   - Save the API design and main abstractions for next agents

2. **Control Plane Implementation**
   - Use Task tool with subagent_type="control-plane-sme"
   - Prompt: "Implement the control plane changes needed to support the new feature: $ARGUMENTS. Use the hints from hcp-architect-sme [include output from step 1]"
   - Include unit, integration, and e2e tests

3. **Data plane Implementation**
   - Use Task tool with subagent_type="data-plane-sme"
   - Prompt: "Implement the data plane changes needed to support the new feature: $ARGUMENTS"
   - Include unit, integration, and e2e tests

4. **Cloud provider integration**
   - Use Task tool with subagent_type="cloud-provider-sme"
   - Prompt: "Review the control plane and data plane changes and implement any further changes needed to support the new feature and ensure it has proper cloud integration: $ARGUMENTS. Add support to create a new HostedCluster in the new platform via CLI"
   - Include unit, integration, and e2e tests

5. **HCP architect review**
- Use Task tool with subagent_type="hcp-architect-sme" 
- Prompt: "Review the changes implemented by the other agents for supporting a new feature: $ARGUMENTS. [Use the output from steps 2,3 and 4]. Report feedback and suggest changes"

Aggregate results from all agents and present a unified implementation plan.

Feature description: $ARGUMENTS