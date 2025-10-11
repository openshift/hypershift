---
description: Create a JIRA ticket with proper formatting and components.
---

Create a JIRA ticket with proper formatting and components based on HyperShift project requirements.

[Extended thinking: This command creates JIRA tickets for the HyperShift project, handling both feature, epic, story, or task requests (CNTRLPLANE) and bug reports (OCPBUGS) with proper formatting, components, and security settings according to the project's standards.]

**JIRA Ticket Creation**

## Usage Examples:

1. **Create a Story**:
   `/jira-create story "Implement new control plane feature" --component="HyperShift"`

2. **Create an Epic**:
   `/jira-create epic "Add metrics collection and export" --component="HyperShift" --description="Epic for implementing comprehensive metrics collection and export functionality"`

3. **Create a Story with full description**:
   `/jira-create story "User can view control plane metrics" --component="HyperShift / ROSA" --description="As a cluster administrator, I want to view control plane metrics, so that I can monitor cluster health"`

4. **Create a Task**:
   `/jira-create task "Update documentation for new API endpoint" --component="HyperShift" --description="Update API documentation to include the new metrics endpoint"`

5. **Create a Bug ticket**:
   `/jira-create bug "Pod fails to start with specific configuration" --component="HyperShift / ROSA" --description="Control plane pods fail to start when using custom storage configuration"`

6. **Create with specific priority and assignee**:
   `/jira-create bug "Critical security vulnerability in authentication" --component="HyperShift" --priority="Critical" --assignee="jdoe@redhat.com"`

## Implementation Details:

- Uses Jira MCP server to create tickets via API
- Automatically sets appropriate project (CNTRLPLANE for features, OCPBUGS for bugs)
- Applies standard labels and security settings
- Formats descriptions according to Jira standards
- Follows Red Hat OpenShift Control Planes (CNTRLPLANE) and OpenShift Bugs (OCPBUGS) project conventions
- Story describes product functionality from customer's perspective as collaboration tool
- Stories support satisfying customer through early and continuous delivery of valuable software
- Epic serves to manage work and should be broader than user story but fit inside time box

## Process Flow:

1. **Input Validation**: Validate command arguments:
   - Determine ticket type (feature/bug)
   - Validate component selection
   - Check for required fields

2. **Project Selection**:
   - Features, epics, stories, tasks → CNTRLPLANE project
   - Bugs → OCPBUGS project
   - Set appropriate components:
     - "HyperShift / ARO" for ARO HCP issues
     - "HyperShift / ROSA" for ROSA HCP issues
     - "HyperShift" when platform is unclear

3. **Ticket Creation - Two-Step Process**:
   **IMPORTANT**: Use reliable two-step creation process due to API limitations:

   **Step 1: Create Basic Ticket**
   ```
   ticket = mcp__atlassian__jira_create_issue(
     project_key="<project>",
     summary="<summary>",
     issue_type="<type>",
     description="<formatted-description>"
   )
   ```

   **Step 2: Update with Required Fields**
   ```
   mcp__atlassian__jira_update_issue(
     issue_key=ticket["issue"]["key"],
     fields={},
     additional_fields={
       "components": [{"name": "<component>"}],
       "security": {"name": "Red Hat Employee"},
       "labels": ["ai-generated-jira"],
       "customfield_12319940": [{"name": "openshift-4.21"}]  # CNTRLPLANE only
     }
   )
   ```

   **Required Fields by Project**:
   - **ALL tickets**: Components, Security Level, Labels
   - **CNTRLPLANE**: Target Version (`customfield_12319940`)
   - **OCPBUGS**: Affected Version (`versions`)
   - Never set Fix Version/s or fixVersions field
   - Format description using Jira Wiki/ADF formatting
   - For epics: Epic name field is required (make it same as summary)
   - For bugs: Summary, component, and affects version/s fields are required

4. **Error Handling**:
   - If ticket creation fails, check component name spelling exactly matches Jira components
   - Verify all required fields are included in `additional_fields`
   - If creation succeeds but fields are missing, immediately use update API
   - Always use exact field names and value formats as specified

5. **Content Templates**:

   **For Stories**:
   ```
   h3. Description
   As a [User/Who], I want to [Action/What], so that [Purpose/Why].

   [User/Who] is the description of the person, device, or system that will benefit from or use the output of the story.
   [Action/What] is what they can do with the system
   [Purpose/Why] is why they want to do the activity.

   h3. Acceptance Criteria
   Expresses the conditions that need to be satisfied for the customer. Provides context for the team, more details of the story, and helps the team know when they are done. Written by the Product Owner or dev team members and refined by the agile team during backlog grooming and iteration planning.

   Formats for Acceptance Criteria:
   - Test that [criteria]
   - Demonstrate that [this happens]
   - Verify that when [role] does [action] they get [result]
   - Given [context] when [event] then [outcome]

   Note: You have enough AC when you have enough to size the story, testing won't become too convoluted, you have made 2-3 revisions of the criteria. With more AC, split story into more stories.
   ```

   **For Bug Tickets**:
   ```
   h3. Description of problem:
   [Problem description]

   h3. Version-Release number of selected component:
   [Version info]

   h3. How reproducible:
   [Always/Sometimes/Rarely]

   h3. Steps to Reproduce:
   1. [Step 1]
   2. [Step 2]
   3. [Step 3]

   h3. Actual results:
   [What actually happens]

   h3. Expected results:
   [What should happen]

   h3. Additional info:
   [Any additional context]
   ```

   **For Epics**:
   ```
   h3. Epic Description
   [Strategic objective description - should be a more narrow strategic objective than a market problem, broader than a user story, fit inside the time box (quarter/release)]

   h3. Epic Name
   [Same as summary field - required]

   h3. Parent Link
   [Feature ID to which this epic belongs]

   h3. Acceptance Criteria
   [When is this epic considered done - lets us know when the epic is done]

   h3. User Stories
   [List of user stories that comprise this epic - stories should fit inside a sprint]
   ```

   **For Tasks**:
   ```
   h3. Task Description
   [Detailed description of the task to be completed]

   h3. Acceptance Criteria
   [Conditions that need to be satisfied for completion]
   ```

6. **Security Considerations**:
   - Never include credentials, secrets, API tokens, kubeconfigs, SSH keys, or certificates
   - Redact sensitive information from descriptions, comments, and attachments
   - Apply "Red Hat Employee" security level by default

7. **Final Verification**:
   After the two-step creation process, ALWAYS verify all required fields:

   a) **Retrieve final ticket state**:
   ```
   final_ticket = mcp__atlassian__jira_get_issue(
     issue_key=ticket["issue"]["key"],
     fields="components,security,labels,customfield_12319940,versions"
   )
   ```

   b) **Confirm all required fields are present**:
   - ✅ Component: Should show the specified HyperShift component
   - ✅ Security Level: Should be "Red Hat Employee"
   - ✅ Labels: Should include "ai-generated-jira"
   - ✅ Target Version (CNTRLPLANE): Should be "openshift-4.21"
   - ✅ Affected Version (OCPBUGS): Should be "4.21"

   c) **Report completion status**:
   ```
   ✅ JIRA Ticket Created Successfully
   - Issue Key: <ticket-key>
   - All required fields verified and set
   - URL: https://issues.redhat.com/browse/<ticket-key>
   ```

   d) **If verification fails**: Retry the update step with missing fields

## Arguments:
- $1: Jira issue type ("story", "epic", "bug", "task") (required)
- $2: Summary/title of the ticket (required)
- --component: HyperShift component ("HyperShift", "HyperShift / ARO", "HyperShift / ROSA")
- --description: Detailed description of the issue
- --priority: Priority level (optional)
- --assignee: Assignee (optional)

## Prerequisites:
- Jira MCP server must be configured with appropriate authentication
- User must have permissions to create tickets in CNTRLPLANE and OCPBUGS projects

The command will provide confirmation of ticket creation with the generated ticket ID and URL.