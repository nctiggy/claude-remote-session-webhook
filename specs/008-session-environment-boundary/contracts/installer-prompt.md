# Contract: The installer's question

**Feature**: 008-session-environment-boundary

## Why this is a contract and not a UX detail

The operator is consenting to a path from an authenticated request to **root on the
host**. Constitution Principle VI is non-negotiable and its standard for any widening is
naming what becomes reachable. A prompt reading "Enable sudo in sessions? [y/N]" does not
meet that standard: it names a convenience and hides the consequence.

## Where the answer is read from

**`/dev/tty`, never standard input.**

The documented install path is:

```
curl -fsSL https://raw.githubusercontent.com/.../install.sh | bash
```

Under it, **stdin is the script**. A `read` would consume the script's own remaining
bytes, and `[ -t 0 ]` is false even with a human at the keyboard — so the obvious
interactivity test fails in the direction that silently skips the question.

| Situation | `[ -t 0 ]` | `/dev/tty` opens | Behaviour |
|---|---|---|---|
| `curl … \| bash`, human present | false | yes | **ask** |
| `bash install.sh`, human present | true | yes | **ask** |
| CI, cron, container build | false | no | **do not ask; answer no** |

## The question

Asked once, after the unit is installed and before the "next steps" summary.

```
Sessions run as you, with hardening that blocks sudo.

Allowing sudo means a request that passes authentication can reach root on
this host, not just your account. Your allowed_roots setting does not bound
this.

You can change this later: see deploy/README.md.

Allow sudo inside sessions? [y/N]
```

## Answers

| Input | Result |
|---|---|
| `y` / `Y` / `yes` | write the drop-in; report the path written |
| `n` / `N` / `no` / empty | write nothing; hardening intact |
| anything else | re-ask, up to a small bounded number of times, then treat as no |
| no `/dev/tty` | write nothing; **do not** print the question as though it were answered |

**Default is no in every ambiguous case.** The direction to be wrong in is the one where
a host does not silently gain a path to root.

## Idempotence

Re-running the installer on a host that already has the drop-in:

- does **not** ask again;
- does **not** rewrite, duplicate or remove it;
- reports that an override is present and where.

The operator's answer is theirs, and an installer that re-asked would eventually get a
different answer by accident.

## Observable test

- Run with stdin closed and no controlling terminal → exits successfully, no drop-in.
- Run twice with `y` → exactly one drop-in file, unchanged content after the second run.
- Answer `n` → the effective `NoNewPrivileges` of the started service is `1`.
- Answer `y` → it is `0`, and `sudo` works inside a session.
