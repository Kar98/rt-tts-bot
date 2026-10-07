---
name: create-plan
description: Write an implementation plan for a feature or change as a Markdown file in .agents/created-plans/, for another coding agent to carry out
disable-model-invocation: true
argument-hint: <what to plan>
---

# Plan

Write a plan for: $ARGUMENTS

If that is empty, ask the user what they want planned before doing anything else.

The plan is for another coding agent to carry out, not for a human to skim. That agent
starts with a fresh context: it has not seen this conversation and knows only what the
plan and the repo tell it. So the plan has to stand on its own. Give exact paths, names,
commands and decisions, and leave out anything it could not act on.

**Do not implement anything.** The only file you write is the plan.

## 1. Understand the request and the repo

- Read `AGENTS.md` (if present) for the project's goals and constraints.
- Look at the code the change touches, the module layout (`go.mod`), and the existing
  conventions the new code should follow.
- When the plan depends on a library API (e.g. ADK Go, genai, OpenTelemetry), check the
  real API in the module cache (`go list -m -f '{{.Dir}}' <module>`) or its docs instead
  of writing it from memory. A plan that names functions that don't exist sends the
  implementing agent down a dead end.
- If a decision is genuinely the user's (a trade-off, a missing requirement, a choice of
  service), ask it now in one batch. Do not guess and bury the guess in the plan. Make
  minor calls yourself and record them under **Decisions**.

## 2. Write the plan

Save it to `.agents/created-plans/<YYYY-MM-DD>-<short-kebab-slug>.md` (create the folder
if needed). Use this structure:

```markdown
# <Title>

## Goal
What this change achieves and why, in 2–4 sentences.

## Context
What the implementing agent needs to know about the current state of the repo:
relevant files, existing patterns to follow, library versions, environment
(e.g. GCP project, env vars). Link to files by path.

## Decisions
Choices already made, each with a one-line reason, so the implementer does not
reopen them. Include anything the user decided while you were planning.

## Out of scope
What not to do in this change.


## Open questions / risks
Anything unresolved, and what to do if it bites (e.g. "if Agent Engine does not
support X, fall back to Y and note it").
```

Leave out a section only if it would be empty. Don't pad.

## 3. Hand off

Tell the user the plan's path, give a short summary, and list any open questions. Stop there.
