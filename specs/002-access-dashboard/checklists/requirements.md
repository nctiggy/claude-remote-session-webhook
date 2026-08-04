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

All items pass. 63 functional requirements, 17 success criteria, 5 prioritized user
stories, no unresolved markers.

**Revised after a cross-model design review (2026-08-04), which found four blocking
problems in the first draft.** All are fixed; three of them were the spec instructing an
implementer to do the wrong thing, which is the failure mode that matters most in a
codebase built by fresh-context iterations:

| Found | Was | Now |
|---|---|---|
| `docs/components.md`'s canonical pane viewer used an htmx swap that inserts payloads **as markup**, which `docs/security.md` forbids in terms | FR-024 (reuse canonical components) and FR-028 (never interpreted as markup) were jointly unsatisfiable — copying the documented snippet opened the project's only XSS surface | `docs/components.md` corrected to a `textContent` append; FR-024 and FR-028 now agree |
| The daemon writes only `starting` and `running`; `SetState` has no production caller and dead records are deleted | Every state requirement was vacuous or unimplementable against the real daemon | FR-019a–c derive display state; `docs/design-system.md` amended to the states that can actually occur |
| FR-014 promised an unmodified client against the deployed hostname, while FR-013a required service-token headers there | An iteration would have found the test failing at the edge, with both available "fixes" catastrophic | FR-014 scoped to the daemon's listener; FR-014a states the edge difference explicitly |
| FR-017 said "every session the daemon knows about" | The simple wrong reading — an owner-blind read — passed FR-017 and broke the isolation rule | FR-017 is owner-scoped and names the trap |

Seven further findings became requirements rather than review comments: no CORS headers
(FR-034c) and cross-site stream refusal (FR-034d), a stream cap (FR-034e), streams not
delaying teardown or advancing the idle clock (FR-034f), an audit record at stream open
(FR-016a), adopted sessions' absent name and working directory (FR-018a), and the
canonical components' milestone-3 action affordances (FR-024a).

**Clarifications raised and answered (2026-08-04).** Three ambiguities were surfaced
rather than guessed at, per Constitution Principle II, and resolved by the operator:

| # | Question | Answer | Requirements |
|---|---|---|---|
| 1 | How the two front doors coexist, given Access refuses the API client as readily as a stranger | One hostname, two edge policies — identity provider for browsers, Access service token for the API. No edge bypass on any path. | FR-013a, FR-013b, SC-015 |
| 2 | What authorises a live output stream, since it can carry neither the signature nor the per-session token | The validated browser identity plus an ownership check, re-evaluated rather than established once. No credential in the URL. | FR-034, FR-034a, FR-034b, SC-016 |
| 3 | Whether the browser and API identities resolve to one owner | One owner, derived server-side by construction — **not** a configuration knob, since its only correct value is the constant milestone 1 deliberately refuses to make settable. Comparison still performed, and tested through the dashboard's own path. | FR-037a, FR-037b |
| 4 | How to display state the daemon does not produce | Derive at render time: idle from last activity against the reaper's threshold, running otherwise, dead never. Design system amended. | FR-019a–c |
| 5 | Append-only transcript vs repainting screen | Show the current screen, replaced on each update. A Claude Code session is a full-screen program, not a log. | FR-031a, FR-032, FR-032a |

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
