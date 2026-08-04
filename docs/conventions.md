# Go Conventions

> Loaded when: writing or changing any Go in this repo.
> `AGENTS.md` states these rules; this file shows them.

## Naming

Standard Go. Packages lowercase and singular; no `util`, no `common` — a package
named for what it contains rather than what it does becomes a drawer.

```go
package session       // files: manager.go, manager_test.go
func (m *Manager) Create(ctx context.Context, req CreateRequest) (*Session, error)
```

## Errors

Never swallow one. Wrap with `%w` and context; return, do not log-and-continue.

```go
if err := m.tmux.Kill(ctx, s.Target()); err != nil {
    return fmt.Errorf("kill session %s: %w", s.ID, err)  // never a bare `return err`
}
```

Sentinel errors for conditions a caller branches on (`ErrSessionNotFound`), checked
with `errors.Is` rather than by string.

**Never put a secret, a prompt, or pane content in an error string.** Errors travel
to logs, and `docs/security.md` §3 makes all three secret. A bare `return err` from a
package that touched the host is how tmux's own stderr ends up in the audit trail.

## Tests

Table-driven, `t.Parallel()`, no network, no real tmux — `tmuxctl.Controller` is an
interface with a fake, and the fake records exact argv so a test can assert no shell
string was ever built.

Every PR needs a test that fails without the change. Auth and session code also needs
the **negative** cases: bad signature, stale timestamp, replay, wrong owner.

```go
req := signedRequest(t, a, `{"cwd":"/repo"}`)
mustVerify(t, a, req)                            // first use passes
if _, err := a.Verify(clone(t, req)); err == nil {
    t.Fatal("replayed request was accepted")     // this is the whole point
}
```

Tests that need a real tmux or a real binary live behind a build tag — `//go:build
tmux` for the integration suite, `//go:build quickstart` for an acceptance run — so
that `go test ./...` stays fast and hermetic. Both are in the command table in
`AGENTS.md`; neither runs by default.

**A test that cannot fail is not a test.** Two defects in this repo shipped with full
green suites because the thing under test had no production caller: the reaper, and
the idle clock. Assert the caller, not only the callee.

## Comments

Explain *why*, never *what*. If the code needs a "what" comment, rewrite the code.

The comments worth writing are the ones that stop a later reader "fixing" something
correct. This repo has several — `path` vs `PATH` in a zsh function, `=name` vs
`=name:` for a tmux target, `alg` read only to reject — and each exists because
somebody lost time to it once.
