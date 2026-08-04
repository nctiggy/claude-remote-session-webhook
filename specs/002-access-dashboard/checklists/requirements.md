# Specification Quality Checklist: Access Validation & Read-Only Dashboard (Milestone 2)

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-08-04
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

All items pass. 47 functional requirements, 17 success criteria, 5 prioritized user
stories, no unresolved markers.

**Clarifications raised and answered (2026-08-04).** Three ambiguities were surfaced
rather than guessed at, per Constitution Principle II, and resolved by the operator:

| # | Question | Answer | Requirements |
|---|---|---|---|
| 1 | How the two front doors coexist, given Access refuses the API client as readily as a stranger | One hostname, two edge policies — identity provider for browsers, Access service token for the API. No edge bypass on any path. | FR-013a, FR-013b, SC-015 |
| 2 | What authorises a live output stream, since it can carry neither the signature nor the per-session token | The validated browser identity plus an ownership check, re-evaluated rather than established once. No credential in the URL. | FR-034, FR-034a, FR-034b, SC-016 |
| 3 | Whether the browser and API identities resolve to one owner | One owner by configuration; the ownership comparison is still performed and still tested against a synthetic second owner. | FR-037a, FR-037b, SC-017 |

Answer 3 is the one that would otherwise have shipped as a working-but-useless dashboard:
every individual requirement would have passed its test and the page would still have been
empty.

**Sub-lettered requirements** (FR-013a, FR-034a, and so on) elaborate the requirement they
follow rather than standing alone. The numbering is deliberate — it keeps the relationship
visible instead of scattering a decision across the list.

**On "no implementation details"**: the spec names protocol-level facts inherited from
the binding documents — a forwarded identity assertion, pinned audience and algorithm, a
cached key set, response headers forbidding external origins. These are constraints
`docs/security.md` and `docs/auth-and-sessions.md` impose, not choices this spec is free
to make, and removing them would make the requirements untestable. No language,
framework, library, or component name appears; where the README anticipates a streaming
mechanism, the spec records it as an assumption and requires only the observable
behaviour.

**On "written for non-technical stakeholders"**: the stakeholder is the operator running
an unsandboxed daemon on their own machine. Plain prose, no code, but it assumes that
reader.

**Design system**: Principle VII binds for the first time in this milestone. FR-019,
FR-023, FR-024, FR-027 and SC-009/010/011 encode the design system's non-negotiables
(text labels on state, tokens only, canonical components, focus, reduced motion) as
testable requirements rather than leaving them to review.
