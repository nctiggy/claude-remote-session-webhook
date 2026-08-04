// Verification harness for ../open-pr.sh — one case per guard the script claims
// to enforce, plus the two "must not regress" invariants from issue #30 that
// have no other enforcement: `Closes #N` in the body, and never an empty PR.
//
//	go run ./.github/scripts/open-pr-check
//
// It lives under .github/ deliberately: Go tooling ignores directories starting
// with a dot, so this file is invisible to `go build ./...`, `go vet ./...` and
// `go test ./...` and cannot affect the module's gate. Run it explicitly.
//
// Why Go and not a shell test next to the shell script: the claude-fix runner's
// own tool allowlist permits `go run` but not `bash`, so this is the only form
// of the test the automation that wrote it could actually execute.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var failures int

func must(err error, what string) {
	if err != nil {
		fmt.Printf("SETUP FAILED: %s: %v\n", what, err)
		os.Exit(2)
	}
}

func git(dir string, args ...string) {
	full := append([]string{
		"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	must(err, "git "+strings.Join(args, " ")+": "+string(out))
}

type result struct {
	code   int
	out    string
	ghArgs string
}

func runScript(script, dir string, env map[string]string) result {
	log := filepath.Join(dir, ".ghlog")
	_ = os.Remove(log)

	cmd := exec.Command("bash", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GH_TOKEN=stub",
		"GH_STUB_LOG="+log,
		"PATH="+filepath.Join(dir, "..", "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	out, _ := cmd.CombinedOutput()
	logged, _ := os.ReadFile(log)
	return result{code: cmd.ProcessState.ExitCode(), out: string(out), ghArgs: string(logged)}
}

func check(desc string, ok bool, detail string) {
	if ok {
		fmt.Printf("  ok   %s\n", desc)
		return
	}
	fmt.Printf("  FAIL %s\n       %s\n", desc, strings.ReplaceAll(strings.TrimSpace(detail), "\n", " | "))
	failures++
}

func main() {
	root, err := os.Getwd()
	must(err, "getwd")
	script := filepath.Join(root, ".github", "scripts", "open-pr.sh")
	_, err = os.Stat(script)
	must(err, "locating open-pr.sh (run this from the repo root)")

	work, err := os.MkdirTemp("", "openpr")
	must(err, "mktemp")
	defer os.RemoveAll(work)

	// A stub `gh`: `pr list` answers from GH_STUB_PR_LIST, `pr create` records
	// its argv so the assertions can read the title, body and flags back.
	binDir := filepath.Join(work, "bin")
	must(os.MkdirAll(binDir, 0o755), "mkdir bin")
	stub := `#!/usr/bin/env bash
if [ "$1" = "pr" ] && [ "$2" = "list" ]; then printf '%s' "${GH_STUB_PR_LIST:-}"; exit 0; fi
if [ "$1" = "pr" ] && [ "$2" = "create" ]; then printf '%s\n' "$@" >> "$GH_STUB_LOG"; exit 0; fi
echo "stub gh: unexpected $*" >&2; exit 1
`
	must(os.WriteFile(filepath.Join(binDir, "gh"), []byte(stub), 0o755), "write gh stub")

	// origin: `main` with one commit, `feat` one ahead of it, `stale` level with it.
	origin := filepath.Join(work, "origin.git")
	git(work, "init", "--bare", "--initial-branch=main", origin)

	clone := filepath.Join(work, "clone")
	git(work, "clone", origin, clone)
	must(os.WriteFile(filepath.Join(clone, "a.txt"), []byte("base\n"), 0o644), "write a.txt")
	git(clone, "add", "a.txt")
	git(clone, "commit", "-m", "base")
	git(clone, "push", "origin", "main")

	git(clone, "switch", "-c", "stale")
	git(clone, "push", "origin", "stale")

	git(clone, "switch", "-c", "feat")
	must(os.WriteFile(filepath.Join(clone, "b.txt"), []byte("work\n"), 0o644), "write b.txt")
	git(clone, "add", "b.txt")
	git(clone, "commit", "-m", "the actual fix")
	git(clone, "push", "origin", "feat")
	git(clone, "switch", "main")

	base := map[string]string{"ISSUE": "30", "ISSUE_TITLE": "A title with spaces", "BASE": "main"}
	with := func(kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	quiet := func(r result) string {
		return fmt.Sprintf("code=%d gh=%q out=%s", r.code, r.ghArgs, r.out)
	}

	fmt.Println("open-pr.sh: must NOT open a PR")

	r := runScript(script, clone, with("BRANCH", ""))
	check("no branch -> exit 0, no PR", r.code == 0 && r.ghArgs == "", quiet(r))

	r = runScript(script, clone, with("BRANCH", "main"))
	check("head == main -> nonzero, no PR", r.code != 0 && r.ghArgs == "", quiet(r))

	r = runScript(script, clone, with("BRANCH", "master"))
	check("head == master -> nonzero, no PR", r.code != 0 && r.ghArgs == "", quiet(r))

	r = runScript(script, clone, with("BRANCH", "never-pushed"))
	check("branch absent on remote -> exit 0, no PR", r.code == 0 && r.ghArgs == "", quiet(r))

	r = runScript(script, clone, with("BRANCH", "stale"))
	check("zero commits ahead -> exit 0, no empty PR", r.code == 0 && r.ghArgs == "", quiet(r))

	r = runScript(script, clone, with("BRANCH", "feat", "GH_STUB_PR_LIST", "7"))
	check("PR already open -> exit 0, no duplicate", r.code == 0 && r.ghArgs == "", quiet(r))

	r = runScript(script, clone, with("BRANCH", "feat", "ISSUE", ""))
	check("missing issue number -> nonzero, no PR", r.code != 0 && r.ghArgs == "", quiet(r))

	r = runScript(script, clone, with("BRANCH", "feat", "ISSUE", "not-a-number"))
	check("non-numeric issue -> nonzero, no PR", r.code != 0 && r.ghArgs == "", quiet(r))

	fmt.Println("open-pr.sh: must open a PR")

	r = runScript(script, clone, with("BRANCH", "feat"))
	check("happy path -> exit 0", r.code == 0, quiet(r))
	check("happy path -> gh pr create called", strings.Contains(r.ghArgs, "create"), r.ghArgs)
	check("body carries 'Closes #30'", strings.Contains(r.ghArgs, "Closes #30"), r.ghArgs)
	check("base is main", strings.Contains(r.ghArgs, "--base\nmain\n"), r.ghArgs)
	check("head is feat", strings.Contains(r.ghArgs, "--head\nfeat\n"), r.ghArgs)
	check("title carries issue title and number", strings.Contains(r.ghArgs, "A title with spaces (#30)"), r.ghArgs)
	check("body lists the branch's commits", strings.Contains(r.ghArgs, "- the actual fix"), r.ghArgs)
	check("successful run is NOT a draft", !strings.Contains(r.ghArgs, "--draft"), r.ghArgs)

	r = runScript(script, clone, with("BRANCH", "feat", "AGENT_OUTCOME", "failure"))
	check("cut-off run -> PR still opened", r.code == 0 && strings.Contains(r.ghArgs, "create"), quiet(r))
	check("cut-off run -> opened as a draft", strings.Contains(r.ghArgs, "--draft"), r.ghArgs)
	check("cut-off run -> body says it is partial", strings.Contains(r.ghArgs, "did not complete"), r.ghArgs)
	check("cut-off run -> body still carries 'Closes #30'", strings.Contains(r.ghArgs, "Closes #30"), r.ghArgs)

	fmt.Println()
	if failures > 0 {
		fmt.Printf("%d check(s) FAILED\n", failures)
		os.Exit(1)
	}
	fmt.Println("all checks passed")
}
