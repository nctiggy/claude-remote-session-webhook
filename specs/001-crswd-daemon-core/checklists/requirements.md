# Specification Quality Checklist: crswd Daemon Core (Milestone 1)

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-08-02
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

All items pass. 43 functional requirements, 18 success criteria, 6 prioritized user
stories, no unresolved markers.

**Clarifications raised and answered (2026-08-02).** Three ambiguities were surfaced
rather than guessed at, per Constitution Principle II, and resolved by the operator:

| # | Question | Answer | Requirements |
|---|---|---|---|
| 1 | Source of the approved working-directory roots | Optional environment value, defaulting to `~/code`, with the fallback announced loudly at every start that uses it | FR-003, FR-004 |
| 2 | Credential lifetime vs. absolute session lifetime | Credential TTL raised to 24h to match; no renewal, no re-issue | FR-015 |
| 3 | Owner and lifetime clock of an adopted session | Owner is the configured operator; absolute clock runs from the host session's own start time, idle clock resets at adoption | FR-023, FR-024, FR-025 |

**Binding document amended.** Answer 2 changes the Lifetimes table in
`docs/auth-and-sessions.md` (token TTL 12h → 24h) plus a short note on why the two
numbers must move together. That edit is part of this change so the binding document
and the spec never disagree — per the constitution, an amendment to a binding
document is a reviewed change like any other, so it must land in the same PR.

**Two values still assumed, recorded in Assumptions rather than as markers**: the
concurrent-session cap and the creation rate limit. Both are required and
operator-configurable; only their default numbers are assumed, and neither is a
correctness question. Confirm during `/speckit-plan`.

**On "no implementation details"**: the spec names protocol-level facts inherited
from the binding documents — a signed request over timestamp plus raw body, a
300-second window, a per-session bearer credential, structured records on standard
output. These are product constraints set by `docs/security.md` and
`docs/auth-and-sessions.md`, not implementation choices this spec is free to make,
and removing them would make the requirements untestable. No language, framework,
library, package, or function name appears.

**On "written for non-technical stakeholders"**: the stakeholder for this feature is
the operator running an unsandboxed daemon on their own machine. The spec is plain
prose with no code, but it assumes that reader — not a general audience.
