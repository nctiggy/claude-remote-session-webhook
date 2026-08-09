# Specification Quality Checklist: Make it work on a phone

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-08-09
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs)
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders
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

**On "no implementation details" — the judgement call, recorded rather than
glossed.**

This spec names CSS concepts in its *Resolved decisions* section: wrapping,
overscroll containment, pointer-coarse conditioning, a width breakpoint. A strict
reading calls that an implementation detail leaking into a specification.

It is deliberate, and it matches milestones 1 through 6.

Two reasons. First, the constitution makes the design system **binding**, not
advisory — so the design system's shape is a governing constraint on this project
in the same way an API contract is on another, and a spec that hid it would be
hiding a real boundary. Second, and more practically: these decisions were
*argued* during the audit, with alternatives priced and rejected. If the spec
omits the argument, the plan re-derives it, and a Ralph iteration re-litigates it
at 3am with less context than the audit had.

The requirements themselves (FR-001 … FR-031) are written as outcomes — what must
be true for the operator — and the success criteria are measured in panning,
tapping, zooming and reading, not in declarations. The technique lives in
*Resolved decisions*, which is the section that exists to carry it.

**On the honesty of SC-012 and the section it points at.**

The most important thing in this spec is not a requirement. It is the table under
*What a test in this repository cannot settle*. Milestone 4 shipped three green
tasks while the control they were about went unchanged, and the mechanism that
allowed it — an assertion standing in for an experience — is available on every
task in this milestone, because nothing in this repository renders CSS.

Three questions are therefore left open **with their fallbacks named in advance**.
That is not incompleteness; naming the fallback before the answer is known is what
turns a later "this reads badly on my phone" into a decision instead of a
redesign. A reviewer should check that those three are still open and still
recorded when the milestone closes, and should treat any of them being quietly
marked resolved without a device check as the failure this spec was written to
prevent.
