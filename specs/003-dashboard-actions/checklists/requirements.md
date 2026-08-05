# Specification Quality Checklist: Dashboard Actions

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-08-05
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

**All three [NEEDS CLARIFICATION] markers are resolved.** They were raised rather than guessed —
the instruction for this spec was to lay out the cross-site options and mark the choice if it was
genuinely the operator's call. Constitution II (*Unknowns are surfaced, never invented*) made that
the correct state to draft in; the answers then made it the wrong state to stay in.

| Marker | FR | Resolution |
|---|---|---|
| Cross-site defence | FR-002 | Origin check **and** per-page token bound to identity (D1) |
| Definition of "compact" | FR-016 | Deliver Claude Code's own `/compact` (D2) |
| Fleet update mechanism | FR-019 | Fleet-level event stream, contract first (D3) |

Two options were removed from consideration by evidence rather than preference, and both are
recorded in D1 so they are not proposed again: a **`SameSite` policy is not available** (the Access
cookie is Cloudflare's, issued under its own domain policy — outside this project's control and
untestable by it), and a **browser-computed HMAC is excluded outright** (it would require shipping
the layer-2 secret to the page, which FR-006 forbids and which would hand out the API).

**On "no implementation details":** the spec names Cloudflare Access, tmux, and HMAC signing.
These are not choices this milestone is making — they are the existing system's vocabulary,
inherited from milestones 1 and 2, and the security argument is unstateable without them. The
same judgement was applied in the milestone 1 and 2 specifications.

**Status**: all checklist items pass. Ready for `/speckit-plan`.

One thing the plan must carry forward: FR-019a requires a **written contract for the fleet stream
before it is built**. It is a new authenticated route, and this repo has never added one without
one. The plan should produce that contract alongside the others rather than treating the stream as
an extension of an existing route.
