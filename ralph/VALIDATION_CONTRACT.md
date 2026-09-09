# Validation contract — milestone 17, `crswd-axi`

**What "done" means for lm-90, decided before the work was decomposed.**

A later pass checks the built system against this file as a **black box**: no
diff, no history, no knowledge of how any of it was built. Every assertion below
therefore describes behaviour a stranger can observe from a shell, and names the
command that observes it.

## How to run the checks

The client ships in the repository as `deploy/crswd-axi/bin/crswd-axi`, and every
command below is written out in full and run from the repository root. Where a
command needs the id of a session, `ID` stands for the id printed by the create
or list command just before it.

Assertions 1, 2, 6 and 7 need nothing but a checkout and Node 22. Assertions 3,
4 and 5 need a running `crswd` daemon and its credentials, exactly as
`deploy/crswd-api` does.

---

## The assertions

- **A caller who types nothing is oriented rather than refused.** Checked: `env CRSWD_HOST=http://127.0.0.1:1 deploy/crswd-axi/bin/crswd-axi` exits 0 and prints, at minimum, the path it was run from, a one-line description of what it drives, a count of the sessions currently live, and at least one suggestion spelled as a runnable command — an unreachable daemon is reported inside that summary rather than as a failure.

- **All six operations are reachable as named verbs, and each explains itself.** Checked: `deploy/crswd-axi/bin/crswd-axi --help` exits 0 and its output names all six of list, show, create, prompt, output and close; and each verb answers for itself, so `deploy/crswd-axi/bin/crswd-axi create --help` exits 0 and describes that verb's own flags, as do the same help commands for the other five verbs.

- **Nobody hand-builds a signature, a header or a JSON body.** Checked against a live daemon, using flags alone: `deploy/crswd-axi/bin/crswd-axi create --name axicheck --dir /tmp` exits 0 and prints a session id, `deploy/crswd-axi/bin/crswd-axi prompt ID --text hello` exits 0, `deploy/crswd-axi/bin/crswd-axi output ID` exits 0 and prints pane text, `deploy/crswd-axi/bin/crswd-axi close ID` exits 0, and a following `deploy/crswd-axi/bin/crswd-axi list` exits 0 without naming axicheck — the operator types no signature, no timestamp, no bearer header and no JSON literal anywhere in that sequence.

- **The client acquires and keeps bearer credentials itself; a caller never has to.** A credential for an adopted session is disclosed in exactly one response, the first list by its owner, and is unrecoverable after it. Checked against a daemon restarted so it re-adopts a live session, starting from no stored state with `rm -rf /tmp/axicheck-state`: run `env CRSWD_AXI_STATE_DIR=/tmp/axicheck-state deploy/crswd-axi/bin/crswd-axi list` twice, then `env CRSWD_AXI_STATE_DIR=/tmp/axicheck-state deploy/crswd-axi/bin/crswd-axi output ID` exits 0 and prints pane text — a client that let the first list drop the credential cannot reach it.

- **Every verb has a machine-readable form.** Checked: `deploy/crswd-axi/bin/crswd-axi list --json` exits 0 and writes exactly one JSON document to stdout and nothing else to stdout, so its first stdout byte is an opening brace and `jq -e .` reading that output exits 0; the same holds for the show, create, prompt, output and close verbs.

- **Failures are typed, distinguishable, and leak nothing.** Checked: `deploy/crswd-axi/bin/crswd-axi --no-such-flag` and `deploy/crswd-axi/bin/crswd-axi show 000000000000` both exit non-zero and their two exit codes differ, so a script can branch on them, and each prints one line to stderr in which `grep -i -E 'secret|bearer|[0-9a-f]{40,}'` finds no match and so exits 1 — no shared secret, no bearer token and no pane content.

- **The client's own behaviour is provable offline, and the daemon is untouched.** Checked from the repository root on a clean checkout, with no dependency install, no network, no daemon and no credential manager available: `node --test deploy/crswd-axi/test/` exits 0; and the Go project stays exactly as green as it was, so `go build ./...` exits 0, `go vet ./...` exits 0 and `go test ./...` exits 0.
