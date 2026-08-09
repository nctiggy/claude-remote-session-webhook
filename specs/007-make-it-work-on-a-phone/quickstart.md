# Quickstart: Make it work on a phone

How to validate this milestone. **Two halves, and the second is the one that
matters** — because everything in the first half can pass while the product is
still rough on a phone.

---

## Half one — what the repository can prove

```bash
cd /home/nctiggy/code/claude-remote-session-webhook

golangci-lint version        # MUST print 2.12.2 — v1 reads the v2 config,
                             # runs a subset, and reports a false green

go build ./...
go vet ./...
go test ./...
go test -tags tmux ./...
go test -tags quickstart ./...
golangci-lint run
```

**The stylesheet suite specifically:**

```bash
go test ./internal/httpapi/ -run 'Test.*(Stylesheet|Token|Breakpoint|Hidden|Pane|Settings|Touch|Coarse|Card|Masthead)' -v
```

**What green means here**: every declaration this milestone specifies exists, in
the rule it was specified for, spelled with tokens, in a position where it takes
effect. That is a real and useful claim.

**What green does not mean**: that any page is usable on a phone. Nothing in this
repository renders CSS.

---

## Half two — what only a phone can prove

Run against the deployed daemon from an actual phone. Not a desktop browser
narrowed, not a device emulator — **the responsive-design emulator has a mouse**,
so `@media (pointer: coarse)` does not match in it and every touch-target change
in this milestone is invisible there.

### 1. The pane — the one most likely to need reverting

Open a session that has real Claude Code output in it, including its interface
chrome.

- [ ] Prose reads top to bottom with **no horizontal panning**
- [ ] Pan a wide line to its edge and keep going — the browser does **not**
      navigate away
- [ ] Flick vertically starting inside the pane — the **page** scrolls; you are
      not trapped in the box
- [ ] Pinch-zoom works
- [ ] **Look at the wrapped TUI chrome and judge it.** Box borders and dividers
      wrap into a line plus a stub. Is that acceptable, or is it worse than the
      panning it replaced?

**If the last one is bad**: the fallback is deleting `white-space: pre-wrap` and
`overflow-wrap: anywhere` from the 780px block. One line reverted, nothing else
touched.

### 2. Settings — the surface that prompted the milestone

- [ ] The section content is visible **without scrolling past a screen of menu**
- [ ] Switching sections lands you on content, not on the menu again
- [ ] A section with wide values pans **the panel only** — the menu stays put
- [ ] Edit one setting: the input and its Save button are both fully visible, and
      **the page does not zoom** when you tap into the field
- [ ] The current section is visibly marked — and covered by more than colour
      (check the border is there too)
- [ ] **Read a stacked row and judge it.** Does the provenance word ("default",
      "file") beneath a value read as part of the value?
- [ ] **Switch to a section late in the list.** Is the current chip visible, or
      has the row scrolled it offscreen?

### 3. Touch targets

- [ ] Tap Destroy on a card. Confirm you can hit it reliably **without** hitting
      Compact
- [ ] Rename a session — no zoom on focus
- [ ] Create a session, open the directory picker, tap an option — reachable
- [ ] Tap the Settings link in the masthead

### 4. Cards and masthead

- [ ] A long working directory is **readable in full** — no tooltip needed
- [ ] The masthead lines up with the cards beneath it
- [ ] The masthead is **one row**, not two

### 5. Desktop — the regression check

Open the same pages on a desktop and confirm **nothing changed**:

- [ ] Pane output is unwrapped, aligned, 80 columns
- [ ] Cards still truncate with an ellipsis
- [ ] Every button is the size it was
- [ ] Settings is two columns with a three-column table
- [ ] The settings menu now **has a background** — this one *is* a visible desktop
      change, and it is the `--glow` bug being fixed. It has never rendered before.

### 6. No JavaScript

Disable JavaScript entirely and reload.

- [ ] Every settings section reachable
- [ ] Every form submittable
- [ ] The dashboard renders and its links work

---

## The three open questions

Three of the boxes above are **judgements, not checks** — marked in bold. They
cannot be answered by any test here, and they are the reason half two exists.

They are recorded in `docs/mobile-open-questions.md` with a fallback named for
each. **A question is answered by the operator's report replacing it, never by a
task ticking it.**

If the milestone closes with all three still listed as unanswered, that is correct
and expected. If one has been marked resolved without a device check, that is the
failure this whole structure was built to prevent — milestone 4 shipped three
green tasks while the control they were about went unchanged, and the mechanism
that allowed it is available to every task in this milestone.
