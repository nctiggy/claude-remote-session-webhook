# Mobile open questions

> Loaded when: changing anything the 780px breakpoint or the `(pointer: coarse)`
> block touches, or closing out milestone 7.

Three questions about the mobile sweep that **nothing in this repository can
answer**. Nothing here renders CSS and nothing here has a thumb. Every task in
milestone 7 lands green, and green proves a declaration exists in the parsed
stylesheet — not that a page is usable on a phone.

So these ship open, on purpose, each with its fallback named **in advance**. A bad
answer is then a decision, not a redesign.

## How a question gets answered

**A question is answered by the operator's report replacing it, never by a task
deciding everything looks fine.**

That sentence is the whole mechanism. The failure this file exists to prevent is a
closing task ticking all three because the suite is green — milestone 4's failure
with a different subject, where three tasks reported success while the control they
were about went unchanged.

Concretely, to answer one:

1. Open the surface on a real phone, in a real session, at a real width.
2. Replace the question's **UNANSWERED** marker with what you saw, dated.
3. If the answer is bad, take the fallback in the same commit. It is already
   specced, and for Q1 it is a two-line revert.

Nobody may answer a question by reasoning about the CSS. If you have not looked at
it on a device, it stays UNANSWERED — and a milestone closing with all three still
open is **correct and expected**.

---

## Q1 — Does the wrapped pane read acceptably against Claude Code's real TUI chrome?

**Status: ANSWERED — 2026-08-09, by the operator, on a phone with a live session.**

> *"I think Claude renders ok in it."*

Recorded with the hedge, because the hedge is part of the answer. This is "good
enough to keep", not "no cost" — the cost was known and priced when the wrap
shipped: Claude Code draws its chrome at full terminal width, so box borders and
dividers wrap into a line plus a stub, and anything whose meaning is its alignment
is misrepresented. What the answer settles is that reading what Claude said, which
is the dominant task on a phone, is worth that.

**The wrap stays.** The fallback below is not taken, and it remains one revert of
two declarations if a later session changes the verdict.

**What this does NOT settle**: whether an operator reaches for pinch-zoom often
enough to want a wrap toggle (#121). That needs the wrap used in anger, not a
first look. #121 is unblocked by this answer and is still not evidenced.

Below 780px the pane wraps (`white-space: pre-wrap`, `overflow-wrap: anywhere`).
This is a known trade, not a fix: Claude Code draws box borders, rules and dividers
that are alignment-dependent, and each of those wraps into a line plus a stub. What
it buys is that reading prose — the dominant phone task — stops requiring a
horizontal pan per line, which today fails outright. Shrink-to-fit was rejected with
arithmetic: 80 columns in a 390px viewport needs a ~6.9px font.

Unknown until an operator reads a live session on a phone: whether the wrapped
chrome is merely ugly or actually misleading.

**How it gets answered:** open a live session with real TUI output on a phone.

**Fallback if the answer is bad:** delete `white-space: pre-wrap` and
`overflow-wrap: anywhere` from the `@media (max-width: 780px)` block in
`web/static/crswd.css`. The base rule still carries `white-space: pre`, so the pane
returns to today's behaviour. One-line revert, no template change, no token change.

## Q2 — Once settings rows are stacked, does a bare provenance word read as part of the value?

**Status: UNANSWERED**

Below 780px the settings table's rows stack and the column headers are hidden
accessibly. A row that read `SESSION CAP │ 4 │ file` across three columns becomes
three stacked lines, and "default" or "file" arrives with no visible header above
it. Whether that reads as provenance or as part of the value is a question about a
reader, not about the markup.

**How it gets answered:** read a settings section on a phone — one row whose
provenance is `file` and one whose provenance is `default`.

**Fallback if the answer is bad:** render an explicit label in the row, so the
provenance carries its own word instead of borrowing the hidden header's. That is a
`web/templates/settings.html` change — **specced, deliberately not built**, because
building the heavier answer before knowing the lighter one is inadequate is the
wrong order.

## Q3 — Does the scrolling section menu disorient when the current chip starts offscreen?

**Status: ANSWERED — 2026-08-09, by the operator, on a phone. The answer was yes.**

> *"In portrait mode the settings are off the screen. Landscape is ok."*

Worse than the question anticipated. It asked whether the *current* chip might
start out of view; what happened is that most of the seven were unreachable —
about two fit a 358px window and the rest needed a horizontal swipe nothing
advertised.

**Portrait only, and that is the tell.** A phone on its side is roughly 844px,
which is wider than the 780px breakpoint, so landscape got the desktop sidebar
and looked fine. The failure lived entirely in the layout this milestone added.

**The fallback was taken**, and it was already written down: the scrolling row is
now a `<details>` disclosure, and the row inside it wraps so nothing is off screen
once it is open. The operator asked for the same thing independently — *"maybe we
fold settings into a pancake menu to tighten up the header"* — which is the
fallback and the request meeting in the middle.

Shipped in `fix/settings-menu-disclosure`. The question stays in this file,
answered rather than deleted: what it recorded is that this was foreseen, priced,
and left for a device to settle, and removing it would erase the evidence that the
mechanism worked.

Below 780px the settings section menu reflows from a column beside the panel into a
horizontally scrolling row above it, and the links stay links. If the operator is
in a section far along that row, the chip marked `aria-current` may start outside
the viewport, so the page opens with no visible indication of where they are.

**How it gets answered:** switch between settings sections on a phone, including
the last one in the menu.

**Fallback if the answer is bad:** replace the scrolling row with a `<details>`
disclosure, which shows the current section as its summary and the rest on demand.
That is a `web/templates/settings.html` change — **specced, deliberately not
built**. It was priced against the scrolling row in
`specs/007-make-it-work-on-a-phone/research.md` R8 and lost; a bad answer here is
what would change that.

---

## Where else these are written down

Each question also sits inline in the contract for the change that raises it, so an
iteration reads the ambiguity in the same document as the rule:

| Question | Contract |
|---|---|
| Q1 | `specs/007-make-it-work-on-a-phone/contracts/pane.md` |
| Q2, Q3 | `specs/007-make-it-work-on-a-phone/contracts/settings.md` |

Milestone 7's final task re-reads **this** file and confirms all three are still
listed, still UNANSWERED, and still carrying their fallbacks. Its proof is this
file's content, not a claim about it.
