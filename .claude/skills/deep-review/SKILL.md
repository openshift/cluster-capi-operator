---
name: deep-review
description: Run a two-pass parallel code review with synthesis
disable-model-invocation: false
---

Run a two-pass code review of the changes described below.

## Context

Changes to review: $ARGUMENTS

Current branch: !`git branch --show-current`

Recent commits on this branch:
!`git log --oneline -20`

## Pre-flight: Gemini availability

Before launching any agents, verify Gemini is available by running this smoke test using the Bash tool:

```
echo "Reply with exactly: OK" | gemini -p - 2>&1
```

If the command fails for any reason — binary not found, authentication error, configuration issue, network error, or unexpected output — Gemini is not available. In that case:

1. **Print this message verbatim before any other output:**

> **NOTE: Gemini is not available** — the `gemini` CLI is not working (not installed, not authenticated, or misconfigured). The Gemini review passes (agents 2 and 3) will be skipped. This review will use Claude-only analysis. Fix the `gemini` CLI setup to enable multi-model review.

2. Skip agents 2 and 3 below. Continue with the rest of the review.

## Pass 1: Parallel analysis

Before launching agents, assess the scope. If the changeset is large or spans distinct areas (e.g., framework code, platform-specific tests, migration logic), split the work across multiple agents by area rather than sending everything to one. Each agent should review a coherent subset.

Launch the following agents **in parallel** using the Agent tool, with `run_in_background: true`:

1. **code-reviewer agent** (`subagent_type: code-reviewer`) — Claude/Opus. Full code review:
   - Correctness and bugs
   - Error handling
   - Architecture and edge cases
   - Adherence to project conventions (see CLAUDE.md)
   - Naming and clarity

2. **gemini agent** (`subagent_type: gemini`) — Gemini via CLI. Independent second opinion from a different model:
   - Architectural soundness
   - Edge cases and failure modes
   - Race conditions and concurrency
   - Blind spots and what's missing
   - Whether existing patterns in the repo are followed

3. **gemini agent (verifier)** (`subagent_type: gemini`) — Second Gemini instance focused on verification:
   - Validate correctness of the implementation against any referenced specs or APIs
   - Check for subtle bugs the first pass might miss
   - Verify test coverage adequacy

**If the changes include test files** (`_test.go`, `suite_test.go`, or test helper files), ask the user whether to also launch a dedicated test quality reviewer. If yes, launch an additional agent:

4. **code-reviewer agent** (`subagent_type: code-reviewer`) — test quality focus:
   - Read and apply `.claude/skills/test-standards/SKILL.md` as the review checklist
   - Test level appropriateness (unit vs integration vs e2e)
   - Debuggable failures (assertion messages, GinkgoHelper, stack traces)
   - Flakiness risks (sleeps, timeouts, shared state, ordering dependencies)
   - Use of existing shared helpers vs duplication

Provide all agents with sufficient context: commit range, relevant file paths, and what the feature does.

Wait for all agents to complete.

## Pass 2: Synthesis

Once all agents have returned, synthesise their findings yourself (do NOT delegate this to a subagent). Analyse the sets of findings and:

- **Corroborated issues**: Flag where multiple reviewers independently identified the same problem. These are high confidence.
- **Contradictions**: Note any disagreements between reviews.
- **Gaps**: Identify areas no reviewer covered.
- **Prioritise**: Rank all findings by severity (bugs > correctness > code quality > style).

## Output

Write the final review to `.claude/reviews/review-$0.md`, structured with sections that fit the findings. Use categories that match what was actually found rather than a fixed template. Common categories include:

- Summary (2-3 sentences)
- Bugs
- Correctness
- Flakiness / Reliability
- Debuggability
- Performance / Slowness
- Code Quality
- Architectural Notes

Only include sections that have findings. Be specific — include file paths with line numbers and suggested diffs where applicable. Be concise — don't restate code that is already clear from the file path and line number.
