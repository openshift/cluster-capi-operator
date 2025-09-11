# Workflow

Do the requested task, using the following workflow

### Making Changes
- [ ] Propose plan to user
- [ ] **Use thinker agent** to validate complex plans
- [ ] Verify git status clean
- [ ] Research existing patterns
- [ ] **Use gemini agent** to verify API signatures/patterns
- [ ] Implement changes
- [ ] **Use thinker agent** for implementation feedback
- [ ] Run `make lint`
- [ ] Run `make unit`
- [ ] Commit only when user explicitly requests

### Testing Changes
- [ ] **Use gemini agent** to verify testing frameworks/patterns
- [ ] Use `make unit` for test execution
- [ ] Follow existing test patterns
- [ ] Add tests for new functionality
- [ ] Remove `F` prefixes before committing

### Code Reviews
- [ ] Use code-reviewer agent
- [ ] **Use thinker agent** for design review
- [ ] Return agent responses verbatim
- [ ] Include line numbers, code quotes, and diffs
- [ ] Check project conventions compliance

## Important Notes

- **Never commit** unless explicitly asked
- **Never create files** unless absolutely necessary  
- **Always edit existing files** instead of creating new ones
- **No documentation files** unless explicitly requested
- **Research first** - look for existing patterns before implementing

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

