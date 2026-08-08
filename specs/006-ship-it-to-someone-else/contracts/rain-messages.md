# Contract: The rain says something

**Files**: `web/static/crswd.js`, `web/templates/partials/header.html`
**Tests**: `internal/httpapi/partials_test.go`, `internal/httpapi/stylesheet_test.go`
**Satisfies**: FR-031, FR-032, FR-033

---

## Drawn, never inserted

Messages are drawn **on the existing canvas**. Nothing is inserted into the DOM.

Canvas content is not in the accessibility tree, so a message drawn there is
inaudible to a screen reader **by construction** rather than by an attribute
somebody could remove. The canvas also carries `aria-hidden="true"` — belt and
braces on the element itself.

## Silent under reduced motion

FR-032 needs no new code: the rain already stops under `prefers-reduced-motion`,
and no rain means no messages. **Nothing may be added that runs when the rain does
not** — a message that appeared on a still page would be the one piece of this
feature that reached someone who asked for stillness.

## The messages live beside the rain

In `crswd.js`, not in a template. They are decoration with no server involvement,
and routing them through a template would make them look like content — something
the daemon is saying rather than something the page is doing.

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestRainCanvasIsAriaHidden` | The canvas carries `aria-hidden="true"` | A message becomes announceable |
| `TestNoMessageInRenderedMarkup` | No message text appears in any template output | They are inserted as DOM nodes, which puts them in the accessibility tree |
| `TestNothingRunsUnderReducedMotion` | With the preference set, neither rain nor message code runs | A message path is added outside the rain's guard |
| `TestMessagesAreNotServerSupplied` | No route or view carries message text | They become content, and the daemon starts having opinions |
