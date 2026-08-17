# Specification Quality Checklist: New Session Modal

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-08-17
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs) — *with the standing
      exception below*
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders — *see note*
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable
- [x] Success criteria are technology-agnostic (no implementation details)
- [x] All acceptance scenarios are defined
- [x] Edge cases are identified
- [x] Scope is clearly bounded
- [x] Dependencies and assumptions identified

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into specification

## Notes

**Two items are marked passing against the letter of the template, deliberately,
and the reason is recorded rather than hidden.**

*"No implementation details" and "written for non-technical stakeholders":* this
repo's specs are read by its single operator and by the agents working under
`AGENTS.md`, and specs 001–010 are all written that way — 009 reasons about
`#{session_activity}` and a constitutional principle in its second paragraph.
Rewriting this one for an audience the repo does not have would cost the
precision the plan is built from.

What is kept out is the part that matters: **no requirement here names a file, a
CSS class, a function or a template**. `<dialog>`, `Esc` and "top layer" appear
because they are the platform's own vocabulary for the thing being specified and
there is no shorter true way to say them; `ValidateName` and `ResolveWorkDir`
appear once each, in a "what this is not" clause whose entire job is to name the
existing controls that must not move. The Success Criteria are clean of all of
it.

**Nothing was marked NEEDS CLARIFICATION.** Five decisions that could have been
were resolved into the Assumptions section instead, each with the reason: the
trigger's position and label, field persistence across a close, backdrop dismissal
as enhancement, the unsupported-browser floor, and the absence of a `dict` in the
template set. Any of the five is cheap for the operator to overturn at plan time,
which is the test for whether a guess was safe to make.
