# Specification Quality Checklist: Ship it to someone else

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-08-07
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

**One decision reverses the source issue, on the operator's challenge.** #66
specified the update route as unreachable from the browser. The operator asked
why, and the answer did not hold.

Its reasoning was that an updater runs downloaded code as a daemon executing
unsandboxed shells. True — and it does not distinguish between doors. An attacker
holding the dashboard can already create a session in an approved root running a
permission-skipping assistant, which **is** code execution on the host. An update
route grants them the ability to install a release this project signed, which is
strictly less than what they already have. The boundary was crossed before this
milestone; the signature bounds the damage, not the door.

Against that sits the product's actual premise: crswd exists to be operated when
its operator is **not at the machine**. An update reachable only from a terminal
is an update they cannot apply from where they are. FR-029 now requires the
browser path, with both halves of the cross-site gate and a confirming step —
the same treatment destroy already gets.

The one attack this opens is a **downgrade** to a version with a known hole. It is
accepted, and recorded in Assumptions, for the same reason: an attacker who could
perform it already has a shorter path to the same outcome.

**On the signing design.** Ed25519 verification is in the standard library, so CI
can sign the checksum file with a key held as a repository secret and the daemon
can verify against a public key embedded at build time — all within FR-034's zero
dependencies. Sigstore and cosign are better known and both require tooling the
daemon cannot carry. Not marked NEEDS CLARIFICATION because the constraint decides
it: a verification the daemon cannot perform is not a verification.

**On the success criteria.** SC-001, SC-002 and SC-003 name a host that has never
had this project. That phrasing is load-bearing. Milestone 5 was taught twice that
a check which only ever runs on the author's machine proves nothing about anyone
else's — once by a suite that had rotted for months, once by a race dismissed as
flakiness. This milestone is entirely about other people's machines, so "it worked
here" must not be able to satisfy it.

**US4 and US3 are both milestone-sized** and must be decomposed at the task stage,
as #65, #59 and #94 were.
