# Research: Finish the dashboard

Six questions were open. Two more were opened by running the daemon rather than
reading it, and those are the interesting ones — they are defects in shipped code
that no amount of reading the source would have surfaced.

---

## R1 — What the remote-control control is

**Decision.** A single checkbox, `name="remote_control"`, `value="on"`, styled as
a switch from the design system's tokens.

| Submitted | Means |
|---|---|
| `remote_control=on` | remote |
| field absent | local |
| `remote_control=<anything else>` | uniform refusal |

**Rationale.** A checkbox is the platform's own two-state control: keyboard
operable, announced correctly, and it needs no script to work. "Absent means
local" is not a compromise — an unchecked checkbox posts nothing, and local is the
safe default, so the failure mode of a lost field is the *less* privileged state.

The switch appearance is CSS over a real `<input type="checkbox">`. The native
control stays the accessible core; only its presentation changes. That satisfies
the operator's "just be a button" without inventing a control a screen reader has
never met.

**Alternatives considered.** A radio pair (`local`/`remote`) — rejected as two
controls for one bit, and it puts a third state on the wire when neither is
selected. A `<button>` toggling hidden state — rejected because it cannot work
without script, which FR-015 forbids.

---

## R2 — What a default install offers as directories

**Decision.** The **approved roots themselves**, always. Union with the explicit
`workdir_suggestions` list and, when `discover_roots` is on, the discovered
children. Deduplicated and sorted.

**Rationale.** The roots are the one source that is guaranteed non-empty whenever
the daemon can create a session at all, and they cost **zero additional
disclosure**: the operator configured those paths, and listing them back reveals
nothing the daemon was not already told. That is what makes them the right default
rather than merely a convenient one.

Their children are a different matter — enumerating them reads the filesystem, and
that is exactly the disclosure `discover_roots` exists to keep opt-in.

A root is also a legitimate working directory in its own right, so offering it is
not a placeholder for the "real" answer.

**Alternatives considered.** Turning `discover_roots` on by default — rejected;
it inverts a deliberate privacy decision to fix a discoverability bug. Shipping an
empty picker and documenting the flag — rejected; that is the present state, and
the operator's conclusion was that the feature does not exist.

---

## R3 — Where the settings link goes

**Decision.** Inside `.masthead-bar`, as a sibling of the operator's email and
**outside** the `<h1 class="brand">`. Today's header is:

```html
<div class="masthead-bar">
<h1 class="brand"><a class="brand-link" href="/">crswd</a> <span class="brand-tag">session control</span></h1>
<p class="operator" title="{{ .Email }}">{{ .Email }}</p>
</div>
```

The settings link joins that row after the operator.

**Rationale.** #46 made the wordmark the link home, and it is inside the `<h1>` —
the page's one first-level heading. A second anchor placed there would compete for
that role. Placed beside the operator's identity it reads as what it is: something
*about this daemon and this operator*, not a section of the site.

One link is not a navigation bar. If a third ever arrives, that is the moment to
reconsider the shape, not now.

**Alternatives considered.** A nav element with home and settings — rejected as
building for a nav bar this interface does not have. A link in the footer —
rejected; there is no footer, and adding one to hold a single link is worse.

---

## R4 — The themed picker's exact structure

**Decision.** Progressive enhancement over the native control, in that order.

**The template renders honest, script-free markup:**

```html
<div class="combo" data-combo>
  <input class="field-input" id="create-work-dir" type="text" name="work_dir"
         list="workdir-suggestions" autocomplete="off" spellcheck="false" required>
  <datalist id="workdir-suggestions">
    <option value="/home/nctiggy/code"></option>
  </datalist>
  <ul class="combo-list" id="workdir-listbox" hidden></ul>
  <p class="combo-status" role="status" aria-live="polite"></p>
</div>
```

**The script upgrades it**, and only then:

1. removes the `list` attribute, which suppresses the native popup
2. adds `role="combobox"`, `aria-expanded`, `aria-controls`, `aria-autocomplete="list"`
3. adds `role="listbox"` to the `<ul>` and manages `role="option"` children
4. writes the FR-045 subset message into `.combo-status`

**Rationale.** The ARIA roles are added by script and **not** put in the template,
because without script `aria-expanded` would be a lie: there is nothing to expand.
Markup that describes a control that does not exist is worse for a screen reader
than markup that describes the plain field that does.

Removing `list` rather than emptying the `<datalist>` is what prevents two popups
appearing at once — the native one is suppressed only where a themed one has
actually taken over.

With no script the operator gets exactly today's control: a text field with the
browser's own suggestions. Unthemed, and working. That is the trade — the theme is
the enhancement, and the enhancement is the only part that can fail.

**Alternatives considered.** A `<select>` — rejected; FR-008 requires any path to
remain typeable. Rendering the ARIA roles in the template and hiding the listbox
with CSS — rejected for the honesty reason above. Waiting for
`appearance: base-select` — rejected; not available across this project's targets.

---

## R5 — What happens to the conversation-listing code

**Decision.** Delete `internal/session/conversation.go` and its test with the
field. Record the commit in #95 so the code is recoverable.

**Rationale.** Removing the field leaves the listing with no caller. This
repository has shipped code with no caller four times — a reaper, `Store.Touch`, a
PR-opener, and `CRSW_DESTROY_ON_SHUTDOWN` — and the cost has been real every time.
Keeping it "because #95 will want it" is how the fifth happens.

It also probably will not fit. #95's problem is *"resume this session's own
conversation after a crash"*, and `listConversations` answers *"what conversations
exist for this directory"*. Those differ in the way that matters: the second cannot
distinguish one session's conversation from another's, which is precisely the
ambiguity FR-032 refuses to resolve by guessing.

Deleting it is not losing it. The reasoning that made it careful — the root check
before the lookup, the listing that opens no file, the symlink exclusion, and the
separator-flattening that makes traversal structurally impossible — is in the file,
in git, at a commit named in the issue.

**Alternatives considered.** Keeping it unused — rejected above. Keeping it behind
the settings page as a read-only view — rejected as inventing a feature nobody
asked for to justify not deleting code.

---

## R6 — How the audit trail should be read

**Decision.** Two changes, because the documented command is a symptom and the
shared stream is the cause.

1. **Send human diagnostics to stderr and audit records to stdout.** They are
   different things for different readers and they currently share a stream.
2. **Document the filter anyway**, because systemd merges both into the journal:

```
journalctl --user -u crswd -o cat | grep '^{' | jq .
```

**Rationale — and this was measured, not guessed.** On the live daemon:

| Command | Result |
|---|---|
| `journalctl --user -u crswd -o cat \| jq .` | **fails** — the documented one |
| `journalctl --user -u crswd _COMM=crswd -o cat \| jq .` | **fails** |
| `journalctl --user -u crswd -o cat \| grep '^{' \| jq .` | works |

The `_COMM` result is the informative one. Filtering to the daemon's own process
still fails, which means the non-JSON lines are **the daemon's own** — not
systemd's, as #88 assumed. Its dependency warnings and its `log` package errors go
to the same place as the audit records.

So no journal-side filter can fix this alone. `grep '^{'` works because the audit
records are the only JSON, and that stays true only if diagnostics keep out of
stdout — which is change 1.

**Alternatives considered.** Priority filtering (`-p`) — rejected; systemd's
stream-to-priority mapping is not a contract this should lean on. Writing the
audit trail to its own file — rejected; it abandons the journal, which is where an
operator already looks and what the unit already configures.

---

## R7 — Why the dependency check warns on a working daemon

**Found by running it, not reading it.** The live daemon has been printing this on
every start:

```
crswd: warning: the start command "rc" runs "claude", which is not on PATH.
```

Sessions using `rc` work. The check is wrong.

**Cause.** It resolves the binary with `exec.LookPath` in the daemon's own
process. The command does not run there — it runs in a login shell inside a tmux
pane. Measured on this host:

| Environment | has `~/.local/bin` | finds `claude` |
|---|---|---|
| systemd user manager, which the daemon inherits | no | **no** |
| login shell, which tmux spawns | yes, via `.profile` | yes |

`claude` is at `/home/nctiggy/.local/bin/claude`. Both answers are correct about
their own environment; the check asks the wrong one.

**Decision.** Resolve it the way the session will: probe through a login shell,
the same way the command will actually be found. Where that cannot be answered,
say what was checked rather than asserting absence.

The tmux probe is untouched and stays fatal — the daemon really does exec `tmux`
itself, so its own PATH is the right one to ask.

**Worth recording.** FR-012 made this a warning rather than fatal, reasoning that
a daemon without a start command can still serve the dashboard. That decision is
the only reason this did not refuse to start a healthy deployment — and it was
made for an entirely different reason than the one that saved it. The lesson is
not "the reasoning was lucky"; it is that **fatality should be reserved for what
the daemon itself cannot do without**, and that rule held under a case it was not
designed for.
