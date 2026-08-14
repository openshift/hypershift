# :material-refresh: Retrospective Guidelines

## :thinking: What is a Retrospective?

A retrospective is a recurring team ceremony held at the end of a sprint or milestone. Its purpose is to reflect on how the team worked together — not what was delivered, but **how** it was delivered — and to identify concrete improvements.

!!! tip "The Goal"
    Continuous improvement: small, actionable changes each cycle that compound over time.

## :clipboard: Board Columns & What Goes Where

Our retro uses a **Start / Stop / Continue** format with two additional columns:

| Column | Prompt | What to Add |
| :---- | :---- | :---- |
| :rocket: **Start** | "What should we start doing?" | New practices, processes, or habits the team isn't doing yet but should try. |
| :octagonal_sign: **Stop / What could have gone better?** | "What should we stop doing? What could have gone better?" | Pain points, inefficiencies, or practices that aren't working. Things that slowed the team down or caused frustration. |
| :white_check_mark: **Continue** | "What should we continue doing?" | Things that worked well and should keep happening. Recognizing what's going right is just as important as identifying problems. |
| :hammer_and_wrench: **Action** | "What actions can be taken?" | Concrete, assignable action items that come out of the discussion. These should have an owner and ideally a linked Jira ticket. |
| :hourglass: **Incomplete actions** | (Carried forward) | Unfinished action items from previous retros. Reviewed at the start of each session. |

## :pencil2: Writing Good Retro Cards

Be specific and actionable. The best cards explain the *what* and hint at the *why*.

!!! example ":rocket: Start Examples"
    * "Define how to proceed if unrelated E2E tests fail on a PR" — clear gap in process
    * "Get the skill/Claude evals up and running so we have a baseline on what model works better (to help reduce costs)" — ties the action to a goal
    * "Choose the best IC model for our team… align on a preferred model so we can present it to Toni" — includes next step and stakeholder

!!! example ":octagonal_sign: Stop / Could Have Gone Better Examples"
    * "A lot of pain from CI (due release image). Still struggling" — honest about ongoing pain
    * "The process to get IT approved things is quite complicated these days — CMDB ID and PIA needed for more things than the past" — identifies the specific friction
    * "e2e v2 tests story could've gone better" — good start, even better with specifics on *what* could've gone better

!!! example ":white_check_mark: Continue Examples"
    * "Using chai-bot as helper during IC rotation was very effective and actually lowered the weight quite a bit" — specific about the impact
    * "Sharing things at the tech discussion" — short but clear
    * "Migrating e2e tests from v1 to v2 ginkgo so we can have CR/Sippy coverage" — recognizes ongoing work worth sustaining

!!! example ":hammer_and_wrench: Action Examples"
    * "Make a flow chart in the upstream docs — 'my e2e is failing, what do I do'" (CNTRLPLANE-3633) — concrete deliverable with a Jira ticket and owner

## :busts_in_silhouette: What's Expected from Each Team Member

### :calendar: Before the Retro

* Reflect on the past sprint. Think about what went well, what was painful, and what you'd change.
* Add your cards to Start, Stop, and Continue columns **before** the meeting. This gives everyone time to think and makes the discussion more productive.
* Use the voting/reactions (:+1:, :bulb:, etc.) on other people's cards to signal agreement before the meeting starts.

### :speech_balloon: During the Retro

* **Review incomplete actions first.** Check whether last retro's actions were completed. The target state is zero incomplete actions.
* **Be honest and blameless.** Focus on processes and systems, not individuals.
* **Discuss and group related items.** Use "Add group" to cluster similar cards.
* **Turn problems into actions.** Every significant pain point in "Stop" should produce a card in "Action" with an owner.

!!! warning "Keep it achievable"
    Keep actions small and achievable. One completed action is worth five ambitious ones that carry forward forever.

### :dart: After the Retro

* **Own your action items.** Follow through before the next retro.

!!! info "Don't wait"
    Check in mid-sprint. If an action is blocked, raise it early — don't wait for the next retro to surface it.
