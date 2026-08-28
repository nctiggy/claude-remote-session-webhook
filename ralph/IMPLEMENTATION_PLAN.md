# Implementation Plan

**Milestone 16 — Voice conversation inside Claude Code.**

> *"Thinking out loud away from a keyboard, and having real work continue. The
> problem is turn-taking, not speech-to-text. I pause mid-thought to collect
> myself, so they start answering before I am done, I talk over them, and the
> exchange degrades."*

Nine tasks. Read `ralph/VALIDATION_CONTRACT.md` first — it is what done means,
and it was written before any of this.

---

## The problem is turn-taking

Every voice assistant that has frustrated the operator ends a turn on **silence
duration**. Too short interrupts a thinking pause; too long feels dead. There is
no threshold that is both, because the signal is wrong: silence does not mean
finished.

So this milestone ships the **explicit verbal terminator** — radio protocol. The
operator says *"go ahead"* or *"over"*, and that, and only that, ends a turn. It
is unglamorous and it is immune to a pause of any length.

**And it is enforced by the daemon, not by the client.** The route that delivers
a dictated turn requires the terminator and strips it. A client that
misheard, looped, or was written by somebody else cannot fire half a thought into
an unsandboxed shell — which is Principle VI's argument applied to a new input
surface rather than a convention written in a script.

Semantic end-of-turn — a cheap model judging "is this a complete thought?" — is
the real fix and is **deliberately not built here**. It layers onto a working
terminator; building it first would mean shipping neither.

---

## Where it lives, and why nothing new is served

It goes on the **session page an operator is already looking at**, as a
`<details>` disclosure beside Rename and Continue. No new page, no new template
file, no service worker, no manifest.

That is Principle IV, and it is also what the page already supports: the
disclosure pattern, the page token, the pane viewer, the submit interceptor and
the action toast are all there and all tested. A second page would be a second
spelling of a session.

## ⚠️ Wispr's realtime WebSocket cannot be reached from this page

The task brief calls Wispr viable and it is — from a client this daemon does not
serve. `internal/httpapi/render.go` sends

```
default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; …
```

with no exceptions, on every dashboard response. `connect-src 'self'` means the
page may open a socket to **this daemon and nothing else**. Reaching Wispr would
need one of two things, and neither is this milestone:

| Route to Wispr | What it actually costs |
|---|---|
| Widen `connect-src` | An edit to `docs/security.md`, which is binding, to admit a third-party origin onto the one page that renders unsandboxed output |
| Proxy it through the daemon | A Wispr API key in the daemon's configuration, an outbound network dependency in a loopback-bound process, and a new streaming route carrying audio |

The brief already names the answer: *"Fall back to Web Speech if the WS path is
awkward."* It is awkward, for a reason written in the source rather than a
preference. **`SpeechRecognition` and `speechSynthesis` are browser APIs, not
fetches** — CSP does not gate them, so the page contacts nobody but this daemon
and the header does not move. Wispr is deferred, with the reason recorded.

---

## ⚠️ Do not speak the whole response

Claude Code prints code blocks, tables, file paths and tool results. Read aloud
they are unusable and actively irritating — this is a design requirement, not
polish, and getting it wrong makes the feature worse than silence.

**The reduction is written in Go, not in the script.** Two reasons: it is the
only half that can be table-tested with the repository's existing commands, and
it keeps the decision about what a session is allowed to say aloud on the server,
next to every other decision about what leaves a pane.

It travels as its own SSE stream rather than as a second event on the pane's.
`specs/002-access-dashboard/contracts/stream.md` fixes **one write per tick** on
that route, and a second event would break an invariant a running client depends
on to serve a new one.

---

## What already exists — reuse it, do not rebuild it

A task that reimplements one of these is a task that has gone wrong.

| Need | What answers it today |
|---|---|
| Deliver text into a session | `session.Manager.Prompt` — the path `POST /sessions/{id}/prompt` already uses. **There is to be no second delivery path.** |
| Authorise a browser write | `handleAction` — layer 1, the cross-site check, the page token and the audit record, inherited rather than restated |
| Read a session's screen live | `sessionStream`, the shared `panes` buffer and `streamCap` in `stream.go` |
| Answer an action | `redirectOutcome` plus a code in `outcome.go`, rendered by the toast the page already carries |
| Submit without navigating | the global submit interceptor in `crswd.js` — a voice turn calls `requestSubmit()` and the existing path carries the answer |

---

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`,
  plus `-tags tmux` / `-tags quickstart` where touched.
- **Check the linter is v2 before trusting it** (#26).
- `go.sum` must never appear.
- **AR-008: no refactoring outside the task.**
- **A transcript is secret**, exactly as prompt text and pane content are: never
  in an error string, never in the trail, never handed back to a caller.
- **The CSP does not move.** A task that needs it to has gone wrong; log it.
- **A new class needs a stylesheet rule and a `docs/components.md` entry in the
  same commit** — the markup, stylesheet and components sweeps hold all three
  together and fail if any one is missing.

---

## Tasks

- [ ] **T001** The spoken reduction, in a new `speech.go` under `internal/httpapi`: `speakable(screen string) string` keeps prose and drops fenced code, box-drawing and table rows, file paths, tool-result markers and the TUI input chrome, capped at 400 characters. Table-driven over real Claude Code screens written as string literals. Test: `go test ./internal/httpapi -run TestSpeakable`.

- [ ] **T002** A speech stream: `GET /sessions/{id}/speech` on the browser door via `handleBrowser`, under a new `audit.ActionSpeechOpen`. It reuses the pane registry, the stream cap, the ownership re-evaluation and the terminal `end` event, and writes a JSON-string event only when T001's reduction changes. Test: `go test ./internal/httpapi -run TestSpeech && go test ./internal/audit`.

- [ ] **T003** End of turn, in a new `say.go` under `internal/httpapi`: a terminator set (`go ahead`, `over`, `over to you`) and `endOfTurn(transcript string) (text string, done bool)` — done only when a terminator ends the transcript, `text` being what remains once it is removed. Pauses, filler and self-correction never complete a turn. Test: `go test ./internal/httpapi -run TestEndOfTurn`.

- [ ] **T004** `POST /dashboard/sessions/{id}/say` via `handleAction`, under a new `audit.ActionDashboardSay`. Reads one `transcript` field, requires T003's terminator, delivers what remains through `Manager.Prompt`. Outcome codes for delivered, unfinished, undelivered. The transcript reaches neither trail nor error. Test: `go test ./internal/httpapi -run TestSay && go test ./internal/audit`.

- [ ] **T005** The voice control on the session page: a `<details>` beside Rename and Continue holding a mic toggle, a transcript live region and the say form with its page token. Markup, `crswd.css` rules and the `docs/components.md` entry land in one task, because the markup, stylesheet and components sweeps fail if any one of the three is missing. Test: `go test ./internal/httpapi`.

- [ ] **T006** The dictation half of `web/static/crswd.js`: the mic toggle starts `SpeechRecognition`, accumulates results into the transcript region with `textContent` and never `innerHTML`, and on hearing a terminator calls `requestSubmit()` on the say form so the existing submit interceptor carries the answer. Test: `go test ./internal/httpapi -run TestTheVoiceClientDictates`.

- [ ] **T007** The speaking half of `web/static/crswd.js`: subscribe to T002's stream, speak each arriving summary with `speechSynthesis`, close on the `end` event, and cancel synthesis the instant recognition reports the operator has started — barge-in. Nothing is spoken while the mic is off. Test: `go test ./internal/httpapi -run TestTheVoiceClientSpeaks`.

- [ ] **T008** The drift guard: one test reads the terminator array out of the embedded `crswd.js` and asserts it is exactly T003's Go set, failing if either side gains, loses or respells a phrase. A client and a server that disagree about what ends a turn is a turn that is heard and never delivered. Test: `go test ./internal/httpapi -run TestTheTerminatorsAgree`.

- [ ] **T009** Document it: `README.md` gains the terminator protocol, what is spoken and what is never spoken, and the browser support this needs. `docs/mobile-open-questions.md` gains Q4 — does the terminator survive real hands-free use — as UNANSWERED with semantic end-of-turn named as its fallback. Verify: both files carry those sections and `go test ./...` exits 0.

---

## Files touched

The blast radius. A diff outside this list is rejected.

```
internal/httpapi/speech.go
internal/httpapi/speech_test.go
internal/httpapi/say.go
internal/httpapi/say_test.go
internal/httpapi/server.go
internal/httpapi/server_test.go
internal/httpapi/stream.go
internal/httpapi/stream_test.go
internal/httpapi/outcome.go
internal/httpapi/outcome_test.go
internal/httpapi/browser_test.go
internal/httpapi/isolation_test.go
internal/httpapi/partials_test.go
internal/httpapi/stylesheet_test.go
internal/audit/audit.go
internal/audit/audit_test.go
internal/audit/leak_test.go
web/templates/session.html
web/static/crswd.js
web/static/crswd.css
docs/components.md
docs/mobile-open-questions.md
README.md
ralph/IMPLEMENTATION_PLAN.md
ralph/PROGRESS.md
```

`internal/session/` is **absent on purpose**. `Manager.Prompt` already delivers
text into a session, and a milestone that edited it would be a milestone that
grew a second way to type into an unsandboxed shell.

`ralph/VALIDATION_CONTRACT.md` is absent for a different reason: it is what done
means, decided before anyone knew how this would be built. An iteration that
edited it would be an iteration moving its own goalposts. If a task cannot be
squared with it, that is a blocked task and a `NEEDS CLARIFICATION` note in
`ralph/PROGRESS.md`, not an edit to the contract.

---

## Out of scope

- **Wispr transcription.** Deferred with the reason above: `connect-src 'self'`.
  Revisiting it is a `docs/security.md` decision, which is a reviewed change to a
  binding document and not something a loop iteration may take.
- **Semantic end-of-turn.** Option 2 in the brief and the right eventual answer.
  It layers onto a working terminator; it does not replace building one.
- **Silence-based VAD, in any form.** This is the thing that already fails, and
  the contract asserts its absence.
- **A PWA manifest, a service worker, or offline anything.** Installability is
  not what hands-free needs, and a service worker in front of an
  Access-authenticated origin is a milestone of its own.
- **A general "type into the session" surface.** `actions.go` already names this
  as the thing that must not arrive sideways. The route added here carries a
  dictated turn, requires a terminator, and is not a text box.
- **A rate limit on the new write.** The signed API's prompt route has none, and
  adding one here would be two answers to one question. Worth revisiting; not
  worth inventing mid-milestone.
- **Push-to-talk.** Reliable, and it defeats the requirement.
