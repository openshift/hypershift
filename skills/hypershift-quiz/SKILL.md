---
name: hypershift-quiz
description: HyperShift Quest — a board-game quiz on HyperShift architecture, networking, CAPI, and the OpenShift components HyperShift runs. Use when the user wants to quiz themselves, onboard on HyperShift, learn the codebase, or says "play hypershift quest".
---

# HyperShift Quest

A board game that teaches HyperShift. The player rolls a die along a 24-tile
board; each tile is a question in one of five categories and one of four
formats. Points are collected per category and persist between games in a
career file, so progress maps to what the player actually knows.

You are the **host and grader — never the question author**. All questions,
answers, explanations, and citations come from the curated bank in
`questions/`. If the bank runs dry, degrade gracefully (see below); do not
invent questions. The game is open-book: for code questions, encourage the
player to open the cited file before answering — reading the source is the
point.

## Inventory

- `board.yaml` — the 24-tile board (categories, formats, special tiles)
- `questions/*.yaml` — the question bank, one file per category
- Progress: `~/.claude/hypershift-quiz/progress.json` (per player, outside the repo)

## Setup

1. Run from the repository root. Read `skills/hypershift-quiz/board.yaml` and
   every file in `skills/hypershift-quiz/questions/`.
2. Load `~/.claude/hypershift-quiz/progress.json`. If missing, initialize:

   ```json
   {
     "version": 1,
     "categories": {
       "architecture":  {"depth": 1, "points": 0, "streak": 0},
       "networking":    {"depth": 1, "points": 0, "streak": 0},
       "data-plane":    {"depth": 1, "points": 0, "streak": 0},
       "control-plane": {"depth": 1, "points": 0, "streak": 0},
       "lifecycle":     {"depth": 1, "points": 0, "streak": 0},
       "kubevirt":      {"depth": 1, "points": 0, "streak": 0}
     },
     "questions": {},
     "games": []
   }
   ```

   Create the directory with `mkdir -p` if needed.
3. Show the board, legend, and career summary, then prompt for the first roll.
   One turn per message; keep the pace snappy.

## The Board

Render after every roll, exactly in this shape (bold marks the player's tile):

```
 01GO  02aM  03nM  04cE  05dM  06lM
 12lE  11cM  10kM  09nE  08aC  07w?
 13a!  14kC  15cM  16dE  17lC  18kE
 24FN  23kS  22lM  21c!  20dS  19nS
```

Legend (show it with the board):

```
a architecture    n networking      c control-plane
d data-plane      l lifecycle       k kubevirt
w wild (pick category + format)
M multiple choice E explain-why     C code reading    S scenario
! challenge (2x points, depth+1)    GO start    FN finish
```

HUD line above the board, e.g.:

```
Tile 7/24 · last roll 4 · career points: a 120 · n 60 · c 40 · d 20 · l 0 · k 0
```

The path is serpentine: row 1 left-to-right (1-6), row 2 right-to-left
(7-12), row 3 left-to-right (13-18), row 4 right-to-left (19-24).

## Turn Loop

1. **Roll.** Run `shuf -i 1-6 -n 1` and show the result. Advance the marker.
   - A roll that would pass 24 bounces back: move forward to 24, then
     backwards the remaining steps.
   - The game ends when the player *lands on* 24.
2. **Resolve the tile.**
   - `start` (1): no question; next roll moves off it.
   - `wild` (7): the player picks any category and any format.
   - `challenge` (`!`): draw at effective depth + 1 (capped at the max depth
     in the bank for that category) for double points.
3. **Draw a question** (rules below), present it, and stop — wait for the
   player's answer in chat. Never answer for them, never hint.
4. **Grade**, then reveal the question's `source` and `why` verbatim.
5. Record progress and re-render the board. A game is typically 5-8 questions.

## Drawing a Question

Effective depth = the player's depth for the category (+1 on challenge
tiles), capped at the maximum depth that exists in the bank for that category.

1. Candidates: matching `topic` and `format`, `depth <= effective depth`, not
   yet asked this game. Prefer exact `depth == effective depth`.
2. Among candidates pick: never-asked first, then lowest `streak`, then
   oldest `last` date.
3. If none: relax depth (any), then fall back through formats
   `scenario -> explain -> code -> mcq`, then any category. Tell the player
   when you degraded and why.

## Asking

- **M (multiple choice)**: use AskUserQuestion with the options in random
  order. Never reveal which is correct before grading. More than one
  `correct: true` means multiSelect. If the player types an answer via
  Other, grade it as text against the correct option.
- **E (explain)**: present the question and stop. The player answers in chat.
- **C (code reading)**: present the question and the cited file path; invite
  the player to actually open and read the file first — open-book by design.
- **S (scenario)**: same as E.

## Grading

- **M**: exactly the correct set (all and only) = full points, otherwise 0.
- **E/C/S**: grade against `rubric.must_cover`, item by item, and say which
  items were hit and missed (briefly):
  - 80%+ of items hit → full points
  - 40-79% → half points
  - under 40% → 0
  - any `common_mistakes` match caps the award at half — and always correct
    the misconception explicitly.
  Grade honestly; a generous host teaches nothing. If the answer is right in
  different words, give the point.
- **Points**: `10 x effective depth`, doubled on challenge tiles. Half points
  round down to a multiple of 5.

## Recording Progress

After every question, update and write `progress.json`:

- `questions.<id>`: `{asked, correct, streak, last}` — `streak` +1 on full
  points, reset to 0 on half or zero.
- `categories.<topic>`: add the points awarded; `streak` +1 on full points
  else 0.
- At game end append to `games`: `{date, final_tile, gained: {<topic>: <points>}}`.

Career points never reset. Depth only changes via promotion.

## Depth Promotion

When a category streak reaches 3, offer promotion: depth +1 for that
category, capped at the max depth in the bank. Announce it as a level-up.

## Ending the Game

When the player lands on 24, show the final board, this game's per-category
gains, the career mastery table, and correct/total for the game, then offer a
rematch.

```
category        depth   career points
architecture       2             140
networking         1              60
data-plane         1              40
control-plane      2             120
lifecycle          1              20
kubevirt           1              10
```

## Other Modes

- `status`: print the career mastery table and recent games. No questions.
- `lint`: validate the bank. Checks: every YAML file parses; ids are unique;
  topics are one of the six categories (architecture, networking,
  data-plane, control-plane, lifecycle, kubevirt); formats are one of
  `mcq`, `explain`, `code`, `scenario`; depth is 1-4; every mcq has 2-4
  options with at least one `correct: true`; every non-mcq has a
  `rubric.must_cover`; every `source` path exists in the repo (strip any
  `#` anchor); board tiles run 1..24 with exactly one start and one finish.
  Use python3 and file checks; report a pass/fail list. Run this in CI
  once the bank grows.

## Honesty Rules

- Never invent questions, options, or facts. The bank is the game.
- Show `source` and `why` verbatim after grading — they are the lesson.
- If the cited code has drifted and the player is arguably right, award the
  points, say the question is stale, and suggest fixing it in a PR.
- If fewer than 3 unseen questions remain for a category, warn that the
  category is nearly exhausted.

## Question Schema

```yaml
- id: <unique-kebab-id>
  topic: architecture | networking | data-plane | control-plane | lifecycle
  depth: 1..4        # 1 orient · 2 mechanism · 3 wiring · 4 invariants & failure
  format: mcq | explain | code | scenario
  question: |
    ...
  options:           # mcq only, 2-4 items
    - text: ...
      correct: true | false
  rubric:            # explain/code/scenario only
    must_cover:              # each item = one grading point
      - ...
    common_mistakes:         # optional
      - mistake: ...
        correction: ...
  source: path/in/repo      # file or doc page; no line numbers
  why: |            # shown after answering; this is the lesson
```

## Authoring Conventions

- Cite files and symbols, never line numbers — lines rot, symbols survive.
- Every `why` must teach something beyond the answer.
- Every question must be answerable from the cited source alone.
- Questions follow the same review bar as code: a wrong question is a bug.
