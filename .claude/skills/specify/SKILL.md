---
name: specify
description: >
  Guide the user through a spec-driven development workflow for feature requirements
  gathering and implementation planning. Use when the user wants to specify a new feature,
  write requirements, generate technical specifications, or create an implementation plan.
  Requires the specware CLI tool to be installed.
---

# Specify — Spec-Driven Development Workflow

Guide the user through comprehensive requirements gathering and implementation planning
for a feature, using the specware tool for artifact management.

## Usage

```
/skill:specify <feature-description-or-short-name>
```

**Arguments:**
- `feature-description-or-short-name` (required): Either:
  1. Feature requirements text — starts a new feature specification
  2. Feedback on existing requirements/plans — continues finalization/review
  3. A short name — resumes work on an existing feature

If the input is ambiguous, ask for clarification.

## Configuration

Read `.spec/config.json` for question counts:
- `requirements.discovery_questions`: Discovery questions (default: 5)
- `requirements.expert_questions`: Expert questions (default: 4)
- `implementation.plan_questions`: Implementation plan questions (default: 5)
- `implementation.testing_questions`: Testing questions (default: 2)

If the config file doesn't exist, use defaults.

## Prerequisites

The `specware` CLI tool must be installed. If it's not available, stop immediately and
instruct the user to install it.

## Workflow

### Phase 1: Requirements Building

#### Step 1: Feature Specification File Setup
- Generate a descriptive short-name from the feature description
- Run `specware feature new-requirements <short-name>` to create the feature directory and base `requirements.md`

#### Step 2: Requirements Gathering
- Run `specware feature update-state <short-name> "Requirements Gathering"`
- Fill in basic sections and metadata of the requirements spec
- Create initial content in `requirements.md` and `context-requirements.md`
- Generate the configured number of yes/no discovery questions:
  - Questions informed by codebase structure
  - Questions about user interactions and workflows
  - Questions about similar features currently in use
  - Questions about data/content being worked with
  - Write all questions to `context-requirements.md` with smart defaults
  - Ask the user ALL questions at once with smart default options
- Record answers in `context-requirements.md`

#### Step 3: Context Gathering
- Run `specware feature update-state <short-name> "Requirements Context Gathering"`
- Research the codebase to become an expert on relevant topics
- Deep dive into existing similar practices, patterns, and features
- Use web searches for best practices or library documentation
- Document findings in `context-requirements.md`

#### Step 4: Expert Requirements Questions
- Run `specware feature update-state <short-name> "Requirements Expert Q&A"`
- Generate the configured number of yes/no expert questions:
  - External integrations or third-party services
  - Access control considerations
  - Performance or scale expectations
  - Questions that clarify expected behavior with deep code understanding
  - Ask ALL questions at once with smart default options
- Record answers

#### Step 5: Finalize Requirements
- Generate comprehensive requirements based on the template in `requirements.md`
- Do not delete or modify existing template sections
- Run `specware feature update-state <short-name> "Requirements Complete"`
- Offer three options:
  1. Interactive review session
  2. Stop for asynchronous review
  3. Move to next phase (not recommended)

#### Step 6: (Optional) Interactive Review
- Run `specware feature update-state <short-name> "Requirements Interactive Review"`
- For each section of `requirements.md`:
  1. Show verbatim section content
  2. Show a 1-3 sentence summary below
  3. Ask the user for changes or approval
  4. Apply changes, updating other sections as needed
- After all sections approved: `specware feature update-state <short-name> "Requirements Complete"`

### Phase 2: Technical Specification Creation

#### Step 1: Determine Necessary Technical Specs
Present three options:
1. User provides specific technical specification types
2. Use analysis to determine the best specs to generate (top 1-2)
3. Skip (not recommended)

**Wait for user selection before proceeding.**

#### Step 2: Generate Technical Specifications
- **Ask user to confirm which specifications to generate**
- For each approved specification:
  - Review requirements for technical details
  - Generate the specification content
  - Store in the spec sub-directory (use appropriate format: OpenAPI → YAML, etc.)
  - Keep limited to technical details only — no summaries or descriptions
  - Show to user and ask for approval before proceeding

#### Step 3: Interactive Review
- Generate 1-2 clarifying questions, ask all at once
- Display the technical specification without modification
- Consider answers and update as needed
- Show updated specification and ask for changes
- When approved, proceed to requirements integration

#### Step 4: Requirements Integration
- Review each section of requirements against new technical specs
- Update requirements to reflect specification changes
- Inform user of changes
- Offer: stop for async review, or move to implementation planning

### Phase 3: Implementation Planning

#### Step 1: Implementation Plan Setup
- Run `specware feature new-implementation-plan <short-name>`
- Run `specware feature update-state <short-name> "Implementation Planning"`
- Read the feature's `requirements.md`

#### Step 2: Codebase Analysis
- Understand existing codebase structure and patterns
- Dive deep into code that needs modification
- Review patterns, best practices, and similar features
- Check CONTRIBUTING.md and code style guidelines
- Record findings in `context-implementation-plan.md`

#### Step 3: Implementation Plan Q&A
- Run `specware feature update-state <short-name> "Implementation Plan Q&A"`
- Generate configured number of yes/no questions about:
  - Best practices and patterns to follow
  - Packaging and file structure
  - Data model details and conventions
  - Error handling and logging
  - Performance and security concerns
  - Testing requirements
  - Write to `context-implementation-plan.md` with smart defaults and code examples
  - Ask ALL questions at once
- Record answers

#### Step 4: Testing Q&A
- Review existing tests for similar features
- Generate configured number of yes/no testing questions:
  - Unit, integration, and e2e testing needs
  - Test-driven development approach
  - How to run specific tests
  - Write to `context-implementation-plan.md` with defaults and examples
  - Ask ALL questions at once
- Record answers

#### Step 5: Finalize Implementation Plan
- Generate comprehensive plan with detailed testing steps
- Break large operations into smaller tasks
- Be specific about test expectations and outputs
- Write to `implementation-plan.md`
- Run `specware feature update-state <short-name> "Implementation Plan Generated"`

#### Step 6: Identify Scope Creep
- Review the implementation plan for areas exceeding approved requirements
- Collect no more than 2-3 suggestions (ignore minor feedback)
- Present suggestions to user — offer to apply all, none, or individual changes
- Update plan as requested

#### Step 7: Interactive Review
- Run `specware feature update-state <short-name> "Implementation Plan Interactive Review"`
- For each section of `implementation-plan.md`:
  1. Show verbatim section content
  2. Show 1-3 sentence summary below
  3. Ask for changes or approval
  4. Apply changes as needed
- After all sections approved: `specware feature update-state <short-name> "Implementation Planning Complete"`

## Question Format

### Discovery Questions:
```
## Q1: Will users interact with this feature through a visual interface?
**Default if unknown:** Yes (most features have some UI component)

## Q2: Does this feature need to work on mobile devices?
**Default if unknown:** Yes (mobile-first is standard practice)
```

### Expert Questions:
```
## Q7: Should we extend the existing UserService at services/UserService.ts?
**Default if unknown:** Yes (maintains architectural consistency)

## Q8: Will this require new database migrations in db/migrations/?
**Default if unknown:** No (based on similar features not requiring schema changes)
```

## Important Rules

- Always use the `specware` tool to track state and create artifacts
- Maintain one feature at a time in active development
- Support stopping and resuming at any point
- Record all Q&A in appropriate context files
- Follow existing codebase patterns and conventions
- Use actual file paths and component names in artifacts

### Q&A Rules
- ONLY yes/no questions with smart defaults
- Ask ALL questions at once
- Write ALL questions to file BEFORE asking
- Stay focused on requirements during requirements phase — exclude implementation details
- Document WHY each default makes sense
- "I don't know" → use the default

### Specware Commands Reference

```
specware feature new-requirements <short-name>         # Create feature dir + requirements.md
specware feature new-implementation-plan <short-name>  # Add implementation plan
specware feature update-state <short-name> <status>    # Update feature status
```

**Directory structure:**
```
.spec/
  001-feature-name/
    .spec-status.json         # Feature status tracking
    requirements.md           # Requirements specification
    context-requirements.md   # Q&A and context for requirements
    implementation-plan.md    # Implementation plan
    context-implementation-plan.md  # Q&A and context for implementation
```
