# Contract: The startup dependency check

**File**: `internal/config/depcheck.go` (new)
**Tests**: `internal/config/depcheck_test.go`
**Satisfies**: FR-012 … FR-015, SC-011

---

## What is probed, and how hard it fails

| Dependency | How it is found | Missing means |
|---|---|---|
| `tmux` | `exec.LookPath("tmux")` | **Fatal.** The daemon refuses to start. |
| The configured start command's binary | First word of each entry in `start_commands`, via `LookPath` **in the daemon's own process** | **Warning**, and one that says which PATH it read. The daemon starts. |

The split is the point. Without `tmux` this daemon can do nothing at all, so
starting would only defer the failure to the operator's first request. Without a
start command's binary it can still serve the dashboard, adopt existing sessions,
and tell the operator what is wrong — so refusing would be worse than warning.

FR-015 is why the second row reads "configured" rather than `claude`: a daemon
configured to run something else is checked for **that**, by reading
`start_commands` rather than a fixed name.

## The install command is derived, never guessed

Read from the system's own identification (FR-013):

| Platform | Detected by | Command named |
|---|---|---|
| Debian/Ubuntu | `/etc/os-release` `ID` or `ID_LIKE` contains `debian` | `sudo apt install tmux` |
| Fedora/RHEL | `ID_LIKE` contains `rhel` or `fedora` | `sudo dnf install tmux` |
| Arch | `ID` is `arch` | `sudo pacman -S tmux` |
| Alpine | `ID` is `alpine` | `sudo apk add tmux` |
| macOS | `runtime.GOOS == "darwin"` | `brew install tmux` |
| Unrecognised | — | `install tmux using your platform's package manager` |

The last row matters: an unrecognised platform gets an honest sentence, not a
confidently wrong command. Naming `apt` on a host that has never had it is worse
than naming nothing.

## The daemon never installs

`depcheck.go` contains no code that executes an install. It names the command;
the operator runs it (FR-014). A daemon that installs software is a daemon that
can be made to install software.

## Worked example

```
crswd: tmux is not installed, and this daemon cannot manage a session without it.
crswd: install it with: sudo apt install tmux
crswd: refusing to start.
```

```
crswd: warning: the start command "rc" runs "claude", which is not on this daemon's PATH.
crswd: sessions type that command into a shell in a tmux pane, and that shell's PATH is the one that decides, so this may be a difference between the two environments rather than a missing binary.
crswd: starting anyway; if "rc" reports "command not found" in its pane, install "claude" or correct start_commands in /home/nctiggy/.config/crswd/config.
```

## The second probe reads a PATH that is not the one that decides (issue #96)

`LookPath` answers for the **daemon's** process. The start command never runs
there — it is typed into a shell in a tmux pane, which loads the operator's
profile. A systemd user manager and a login shell on the same host routinely
disagree: `~/.local/bin` is on one and not the other, and a `claude` installed
there is invisible to this probe and perfectly present to every session.

So the warning states its scope instead of predicting an outcome. It may not say
"sessions will fail" — it does not know that, and it said it on every start of a
healthy deployment for the whole of milestone 4.

Resolving the pane's PATH honestly would mean `bash -lc 'command -v …'` at
startup, which executes the operator's profile in the daemon and is forbidden
here by `TestNeverExecutesInstall`: this package reaches `os/exec` for `LookPath`
and nothing else.

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestMissingTmuxRefusesToStart` | Empty `PATH` → startup error naming tmux | The check warns instead of refusing |
| `TestMissingStartCommandWarnsOnly` | Start command absent → daemon **starts**, warning emitted | The warning is promoted to fatal |
| `TestChecksConfiguredCommandNotClaude` | `start_commands = x=frobnicate` probes `frobnicate` | The check hardcodes `claude` (FR-015) |
| `TestProbesFirstWordOnly` | `claude --dangerously-skip-permissions` probes `claude` | The whole line is passed to `LookPath` |
| `TestInstallCommandFromOsRelease` | A Debian `ID` yields `sudo apt install tmux` | The command is chosen by `GOOS` alone |
| `TestUnknownPlatformSaysSo` | Unrecognised `ID` yields the generic sentence | A package manager is guessed (FR-013) |
| `TestNeverExecutesInstall` | No `exec.Command` in the package runs a package manager | An "auto-install" convenience is added (FR-014) |
| `TestMessageNamesConfigFile` | The warning names the file to correct | The operator is told what is wrong but not where |
| `TestWarningNamesThePathItProbed` | The warning names the daemon's PATH and the pane's, and predicts no failure | The message claims a session "will fail" from a probe of the wrong environment (#96) |
