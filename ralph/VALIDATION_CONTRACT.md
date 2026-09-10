# Validation contract — milestone 17

What **done** means for this milestone, written before the work was decomposed.

A later pass checks the built system against this file as a **black box**: no diff,
no git history, no plan — just the repository and these commands. Every assertion
below is a claim about observable behaviour and how to see it, and each is written
on one line so it can be read on its own.

## Running the checks

All commands run from the repository root.

`GOFLAGS=-buildvcs=false` is not decoration and it is not optional. This repository
is worked in a **git worktree**, and the Go toolchain's VCS stamping fails there
(`error obtaining VCS status: exit status 128`). Two suites shell out to `go build`
from inside a test — the acceptance harness builds the daemon it drives — so
without that flag they fail before asserting anything, in a way that says nothing
about the daemon. Measured on this host, 2026-09-10: with the flag, everything
below is green; without it, `internal/release` and the whole `quickstart` suite
fail identically on `main` with no change applied at all.

The acceptance suite needs `tmux`, `jq` and `127.0.0.1:8765` free. It takes ~37s.

Throughout, "a session's pane reports X" means: create a session through the
daemon's API, read that session's output back with `GET /sessions/{id}/output`, and
find the line the program running in that pane printed. It is a claim about what
the process in the pane received, never about what the daemon computed.

## The assertions

- **A session gets the 401 back-off even when nothing on the host set it.** With `CLAUDE_CODE_OAUTH_401_WAIT_MS` absent from the daemon's own environment, a session created through the API has that variable set to `60000` in its pane, read back through `GET /sessions/{id}/output`. Checked by `GOFLAGS=-buildvcs=false go test -tags quickstart ./cmd/crswd -count=1`, which exits 0.

- **A value the operator states beats the default, and appears once.** With `CLAUDE_CODE_OAUTH_401_WAIT_MS=0` in the daemon's own environment *and* `CRSW_SESSION_ENVIRONMENT=CLAUDE_CODE_OAUTH_401_WAIT_MS` naming it — the override route `README.md` and `config.example` document — a session's pane reports `0`, not `60000`, and not both. Checked by `GOFLAGS=-buildvcs=false go test -tags quickstart ./cmd/crswd -count=1`, which exits 0.

- **Those two claims cannot be satisfied by the daemon's own bookkeeping alone.** Find the single place the daemon supplies the wait (`grep -rn 60000 --include='*.go' .`), change it to `59000`, touch no test, and `GOFLAGS=-buildvcs=false go test -tags quickstart ./cmd/crswd -count=1` exits **non-zero**; restore `60000` and the same command exits 0 again. A fix that reached one start command and not the others would pass a map-level assertion and fail this one.

- **Nothing else regressed.** `GOFLAGS=-buildvcs=false go test ./...` exits 0, `go vet ./...` exits 0, `go vet -tags quickstart ./cmd/crswd` exits 0, `golangci-lint run` prints `0 issues.`, and the acceptance command above passes in **full** — every case, not only the two new ones, because the stand-in program they all share now prints one extra line, so "the new cases pass" is not the same claim as "the suite passes" and only the second one counts.
