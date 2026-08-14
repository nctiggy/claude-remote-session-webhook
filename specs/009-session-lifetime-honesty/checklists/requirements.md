# Specification Quality Checklist: Session Lifetime Honesty

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-08-14
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs)
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain — both resolved 2026-08-14: FR-006 by the
      repo's own schema-version/`config migrate` mechanism, FR-021 by operator decision
      to read Claude's on-disk history (with FR-021a/FR-021b bounding the disclosure)
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

## Constitution

- [ ] Spec is consistent with `.specify/memory/constitution.md` — **not yet.** User
      Story 1 contradicts Principle VI (NON-NEGOTIABLE), which requires every session
      to have an idle timeout. The operator elected to amend it, stating the cost.
      The amendment is written on branch `009-session-lifetime-honesty`
      (constitution 1.0.0 → 2.0.0, MAJOR: a NON-NEGOTIABLE requirement was removed).
      This item closes when that amendment is reviewed and merged.
- [x] Principle I (security is a gate): FR-016, FR-017, FR-022, FR-023, FR-025 hold the
      line — no new execution surface, no new caller-supplied string reaching a command
      line unvalidated, no widening of the working-directory allowlist.
- [x] Principle II (unknowns surfaced, never invented): 2 markers raised rather than
      guessed; FR-010 and FR-012 refuse to invent a lifetime for a session that carries
      none.
- [x] Principle IV (smallest correct change): the idle removal is large but is exactly
      what was asked for; no adjacent refactor is specified.
- [x] Principle VII (design system binding): recorded as a dependency on
      `docs/design-system.md` and `docs/components.md`.

## Notes

- The two open markers are presented to the operator as Q1 and Q2. Both affect scope or
  security and neither has a safe default:
  - **FR-006** decides whether this upgrade is an outage for the currently deployed
    daemon, whose live config file carries the retired keys today.
  - **FR-021** decides whether the daemon gains a read of Claude's on-disk conversation
    history — a disclosure `docs/security.md` governs — or stays within what it started
    itself.
- The constitutional conflict is not a spec defect and cannot be resolved by editing the
  spec. It requires `/speckit-constitution` and a reviewed PR before `/speckit-plan`.
