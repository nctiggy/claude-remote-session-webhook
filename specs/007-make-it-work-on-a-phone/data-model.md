# Data Model: Make it work on a phone

**There is no data.** No entity, no persisted state, no schema, no migration. This
milestone adds two constants to a stylesheet and rearranges rules.

Recorded rather than omitted, because an empty data model is a claim worth making
explicitly: nothing here touches the daemon's state, so nothing here can corrupt
it, and a reviewer looking for the storage implications can stop reading.

## The two new tokens

The only durable additions. Both are design system vocabulary, and both are
**transcribed by hand into three places** — see `contracts/guards.md` G4.

| Token | Value | What it means | Why it must be a token |
|---|---|---|---|
| `--tap` | `44px` | The minimum touch target in the block dimension | The value sweep fails a literal length anywhere below the token block, including inside a media query. There is no way to write it inline. |
| `--fs-input` | `16px` | The font size at or above which mobile browsers do not zoom on focus | Same. And naming it records *why* the number is what it is — the next person changing an input's size needs the reason, not the digits. |

**Where each lives, all three in one commit:**

1. `docs/design-system.md` — the declaration and its rationale
2. `web/static/crswd.css` — the token block, which is positionally the **first**
   rule of the file
3. `internal/httpapi/stylesheet_test.go` — the `designTokens` map at line 31

The map is deliberately a transcription of the **document**, not a read of the
stylesheet. A test comparing the file against its own spelling would still pass on
a palette that had quietly drifted.

## State transitions

None.

The one thing in this milestone that resembles state is which settings section is
current, and it is unchanged: a query parameter, one section per GET, marked with
`aria-current`. No task may alter that mechanism — the section menu stays real
links precisely so the page keeps working with no JavaScript.
