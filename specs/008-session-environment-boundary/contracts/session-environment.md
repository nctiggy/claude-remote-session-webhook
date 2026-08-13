# Contract: What a session receives

**Feature**: 008-session-environment-boundary

The daemon's interface to the code it starts. It is a contract because a session is
unsandboxed code running as the operator: what crosses this line is what an attacker who
reaches a session gets for free.

## The guarantee

> A session receives a **composed** environment. It never receives the daemon's own.

## Always present

| Name | Value | Note |
|---|---|---|
| `HOME` | the daemon's | omitted entirely if unset — never passed empty |
| `PATH` | the daemon's, unchanged | so a session finds the same commands it does today |
| `SHELL`, `USER`, `LOGNAME` | the daemon's | a working login-shaped shell |
| `TERM` | the daemon's | tmux needs it |
| `LANG`, `LC_*` | the daemon's | locale-dependent tooling |
| `XDG_RUNTIME_DIR` | the daemon's | tmux and the systemd user manager both use it |

## Never present

| Name | Reason |
|---|---|
| `CRSW_SHARED_SECRET` | layer-2 auth; holding it means signing valid API requests |
| `CRSW_ACCESS_ALLOWED_EMAILS` | classified secret — names who may reach the host |
| `CRSW_DASHBOARD_PASSWORD` | the browser door itself |
| Any other `CRSW_*` | the daemon's configuration is not a session's business |
| Anything else in the daemon's environment not listed above or named by the operator | the allowlist is the whole set |

**The three secret names are not a second list.** They are whatever
`config.IsSecret` says, reused. A fourth secret added there is excluded here on the same
day, without an edit.

## Operator extension

The operator may name additional variables in their configuration file. The daemon:

- passes through each named variable that exists in its environment;
- ignores a named variable that does not exist — the operator is describing intent;
- **refuses to start** if an entry names a secret or a `CRSW_` variable, saying which
  entry and why.

The refusal is a startup failure rather than a warning, because a warning about a
credential is a credential that ships.

## What this contract does not cover

Sessions **already running** when the daemon is upgraded. A process's environment cannot
be changed from outside, so those panes keep what they were started with until they are
recreated. The daemon does not claim otherwise, and the deployment documentation says so.

## Observable test

From inside a freshly created session:

```
env | grep -c '^CRSW_'        # expect: 0
```

and the daemon's own suite passes when run there, which it does not today.
