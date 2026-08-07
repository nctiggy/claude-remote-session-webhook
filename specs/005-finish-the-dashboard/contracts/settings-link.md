# Contract: Reaching the settings page

**File**: `web/templates/partials/header.html`
**Tests**: `internal/httpapi/partials_test.go`
**Satisfies**: FR-011, FR-012, FR-013

---

## Today

```html
<div class="masthead-bar">
<h1 class="brand"><a class="brand-link" href="/">crswd</a> <span class="brand-tag">session control</span></h1>
<p class="operator" title="{{ .Email }}">{{ .Email }}</p>
</div>
```

One anchor, `href="/"`. `/settings` shipped in milestone 4 and can only be reached
by typing the address.

## The change

One anchor added to `.masthead-bar`, after the operator, **outside** the `<h1>`:

```html
<p class="operator" title="{{ .Email }}">{{ .Email }}</p>
<a class="masthead-link" href="/settings">Settings</a>
```

## Why there and not inside the heading

#46 made the wordmark the link home, and it lives inside the `<h1>` — the page's
one first-level heading. A second anchor there would compete for that role, and a
heading holding two links is a heading that has become a menu.

Beside the operator's identity it reads as what it is: something about *this
daemon and this operator*, not a section of a site.

**One link is not a navigation bar.** If a third arrives, that is the moment to
reconsider the shape.

## What must not change

- The wordmark stays the first anchor in the header and keeps `href="/"`
- `.brand`, `.brand-link` and `.brand-tag` keep their structure
- `/settings` stays read-only: **no mutating verb is registered on it** (FR-013)
- The card's one-anchor rule is untouched — this is the header, not a card

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestHeaderLinksToSettings` | Rendered header contains `href="/settings"` | The page ships unreachable again |
| `TestSettingsLinkIsOutsideTheBrandHeading` | The settings anchor is not a descendant of `.brand` | It is dropped into the `<h1>`, turning the heading into a menu |
| `TestWordmarkIsStillTheFirstAnchor` | First `<a>` in the header is `.brand-link` with `href="/"` | The settings link is placed before it and becomes the primary anchor |
| `TestHeaderHasExactlyTwoAnchors` | Exactly two | A third arrives without the shape being reconsidered |
| `TestEveryPageCarriesTheHeader` | Dashboard, session page and settings page all render it | A page composes its own header and loses the link |
| `TestSettingsLinkHasVisibleFocusRing` | Focusable with a visible indicator | `outline: none` |
| `TestSettingsStillHasNoMutatingVerb` | POST, PUT, PATCH, DELETE to `/settings` all 405 | Reachability is mistaken for permission to add editing |
