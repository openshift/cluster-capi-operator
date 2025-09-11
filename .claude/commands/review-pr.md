---
name: pr-review
description: Run review workflow for PR changes
parameters:
  - name: pr_url
    description: GitHub PR URL to review
    required: true
---

## Output Format Requirements
You MUST use this EXACT format for ALL review feedback:


+LineNumber: Brief description
**Current (problematic) code:**
```go
[exact code from the PR diff]
```

**Suggested change:**
```diff
- [old code line]
+ [new code line]
```

**Explanation:** [Why this change is needed]

## Workflow

- Use the code-reviewer agent to review the provided PR. Do not review lock files: `.sum` or `.lock`
- Get comments on the PR using the gh command, ensure all comments requesting changes are addressed.
- Use the Thinker or Gemini agents if relevant.
- Output the review in line with the formatting requirements.

### Error Handling
- If the diff is too large: `PullRequest.diff too_large` then check out the branch locally: `gh pr checkout <url>`

## Agent Usage Guidelines

### Agent Response Handling Rules
**CRITICAL:** When using any specialized agent:
- **Return agent response VERBATIM** - no summarizing
- **DO NOT apply conciseness rules** 
- **Preserve ALL formatting and examples**
- Agent responses are exempt from "be concise" instruction

### Thinker Agent (Expert Feedback)
**Use proactively for:**
- **Plan validation** - Before implementing complex changes
- **Design review** - After proposing architectural changes
- **Implementation feedback** - After completing significant features
- **Problem-solving approach** - When tackling complex issues

### Gemini Agent (Verification)
**Use proactively for:**
- **API/function signatures** - Verify controller-runtime, Kubernetes APIs
- **Design pattern validation** - Confirm architectural approaches
- **Technical accuracy** - Verify Go patterns, testing frameworks
- **Concept validation** - Check understanding of CAPI, operator patterns

### Code-Reviewer Agent
**ALWAYS use for:**
- Code reviews and PR reviews
- Change analysis and compliance checking
