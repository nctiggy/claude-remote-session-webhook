# Validation contract — Milestone 16, voice conversation inside Claude Code

What **done** means, written before the plan and independent of it. A later pass
checks the built daemon against this file as a **black box**: no diff, no commit
history, no knowledge of how any of it was built. Every assertion below therefore
describes something an operator or a command can observe from outside, and each
one is written on a single line so it can be read, and checked, on its own.

The whole feature exists because turn-taking is the problem, not transcription.
The first three assertions are that claim made checkable; the rest are the
conditions under which shipping it is allowed at all.

Commands are run from the repository root.

---

## Behavioural assertions

- **A turn ends when the operator says it ends, never when they stop talking.** A dictated turn reaches the session only after the operator speaks a terminator phrase; a transcript carrying no terminator delivers nothing, however long it is and however many pauses it holds. Checked by `go test ./internal/httpapi`, and observably by dictating a rambling instruction, never speaking the terminator, and finding that the session received nothing.

- **The length of a pause changes nothing.** The same words dictated slowly, with pauses of a minute or more, and dictated quickly produce one identical turn — never several fragments, never a turn sent early. Checked by `go test ./internal/httpapi`, and observably by pausing mid-thought for as long as the operator likes and seeing exactly one turn arrive, after the terminator and not before.

- **The terminator is spoken, not typed, and it does not survive into the session.** What arrives in the session is the operator's words with the terminator phrase removed, so an assistant is never asked to read "go ahead" as part of the instruction. Checked by `go test ./internal/httpapi`, and observably by reading the delivered turn in the session and finding no terminator phrase in it.

- **Nothing is spoken aloud that is useless aloud.** What is offered to be spoken for a given screen is short prose: no fenced code block, no box-drawing or table row, no absolute file path, no tool-result marker, and bounded in length. Checked by `go test ./internal/httpapi`, and observably by putting a screen dominated by tool output and code in front of it and hearing only prose.

- **The operator can see what was heard before it acts.** A session page served to a signed-in operator offers a dictation control and a visible region showing the accumulated transcript, so a mis-heard instruction is catchable before it is sent. Observable outcome: load a session page and both are present, with the transcript rendered as text rather than as markup. Checked by `go test ./internal/httpapi`.

- **Speaking over the assistant silences it immediately.** When the operator starts speaking, speech already being read aloud stops within that utterance rather than finishing it. Checked by `go test ./internal/httpapi`, and observably by talking over a reply on a phone and hearing it stop mid-sentence.

- **Nothing an operator dictates is written down.** No transcript, and no part of one, appears in the audit trail or in any error the daemon emits — the same rule prompt text and pane content already live under. Checked by `go test ./internal/audit`, and observably by dictating a distinctive phrase and searching the audit trail for it without a hit.

- **Voice adds no new origin.** The page contacts this daemon and nothing else: every dashboard response still carries a Content-Security-Policy with `connect-src 'self'`, otherwise unchanged. Checked by `go test ./internal/httpapi`, and observably by `curl -sI` against a served dashboard page.

- **The tree is green by the repository's own definition.** `go build ./... && go vet ./... && go test ./... && golangci-lint run` exits 0, and so do `go test -tags tmux ./...` and `go test -tags quickstart ./cmd/crswd`.

---

## What this contract deliberately does not assert

Recorded so a later pass does not read an absence as a failure. These are not
assertions and nothing is checked against them.

**Nothing about transcription quality.** Which engine hears the operator, and how
well it copes with a stutter, is not something this repository can test.

**Nothing about installability.** A home-screen icon and an offline shell are not
what "hands-free" needs, and neither is asserted here.

**Nothing about semantic end-of-turn.** A model judging whether a thought is
complete is the better answer, and it is explicitly the next milestone's rather
than this one's. Shipping without it must not be read as a failure of this file.
