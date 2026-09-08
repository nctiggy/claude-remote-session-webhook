# Validation contract — milestone 17

**What "done" means for `lm-86`: re-auth a logged-out session from the phone.**

Written before the plan was decomposed, and deliberately in terms of what an
operator can observe. A later pass checks the built system against this file as a
**black box** — no git history, no diff, no knowledge of how any of it was built.
So nothing below names a Go file, a function, a template or a CSS class.

The whole-tree command every assertion assumes is green:

```
go build ./... && go vet ./... && go test ./... && golangci-lint run
```

`go test ./...` reaches none of the tagged suites; `go test -tags tmux ./...`
and `go test -tags quickstart ./cmd/crswd` are the ones that touch a real tmux,
and neither is expected to change in this milestone. If either regresses, that
is a failure of this contract too.

Each assertion is one line and states on that line how it is checked.

---

## The assertions

- **A session sitting on Claude Code's login screen is not reported as healthy** — its own page labels it `needs-auth`, in text as well as colour, rather than `running`. Checked by `go test ./...` against a captured login pane, and observably by opening such a session's page and reading the pill.

- **A session that is up but whose every request is failing for want of a login also reads `needs-auth`, and finding that out changes nothing on the screen.** Checked by `go test ./...`: a pane holding the expired-login error renders `needs-auth` while the deliveries recorded for that render stay empty.

- **The parked session's own page offers the sign-in URL as a link a phone can tap and a box to paste the returned code into, and offers both only while that session is parked.** Checked by `go test ./...`; observably, a session that is not parked renders neither.

- **Submitting a pasted code delivers exactly those bytes into that session and nothing else** — not a normalised copy, not a re-encoded one, not a trimmed one with a guessed suffix. Checked by `go test ./...`: the bytes recorded as delivered to the host equal the bytes submitted.

- **The pasted code appears in no audit record, no log line, no error message and no rendered page** — it reaches the session and nowhere else. Checked by `go test ./...`: driving the relay with an unmistakable value and reading back everything the daemon wrote finds it only among the bytes delivered to the session.

- **The sign-in URL is rendered on the parked session's own page and nowhere else** — it is a one-shot challenge, so it is absent from every other page, from the audit trail and from the logs. Checked by `go test ./...`; observably, loading the fleet with a parked session in it produces a page containing no sign-in address.

- **Nothing is submitted that the operator did not type** — the daemon relays a code and never invents, completes or auto-submits one, and never presses the login screen's own selection on the operator's behalf. Checked by `go test ./...`: rendering a parked session's page delivers nothing into it.

- **An operator holding only a phone can bring a session that is up but unauthenticated to the login screen, without reaching a shell on the host.** Checked by `go test ./...`; observably, the action reports success and the bytes recorded as delivered are Claude Code's own login command.

- **Every new action refuses the ways an action route must refuse, and none of them answers 500** — the wrong owner, no confirming field, an id that does not exist, and an empty submitted value each get a stated refusal. Checked by `go test ./...`.

- **A session parked on the login screen is left alone by the daemon's own revival machinery** — not restarted, not marked failed, not destroyed while an operator is part-way through relaying a code. Checked by `go test ./...`: the calls recorded for a parked session contain no restart.

- **The shipped documentation states plainly what an operator sees on the fleet grid versus on a session's own page when a session needs auth**, rather than leaving it to be discovered. Checked by `go test ./...` and by reading the shipped docs: the sentence is there, or it is not.

---

## What this contract does not claim

It does not claim a live re-auth was performed. That needs a genuinely logged-out
Claude on a real host and a browser on a phone; see `ralph/IMPLEMENTATION_PLAN.md`
§ *What the loop cannot do*. The operator's end-to-end run is the last gate and it
is outside every command above.

It says nothing about API keys, workload identity federation, or any metered
credential. Those are off the table by decision (9/8/26), and a build that reached
for one would fail this contract by contradicting it.
