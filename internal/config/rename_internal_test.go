package config

// The rename mechanism, exercised against a fixture table because renamedKeys is
// empty today and the whole reason it exists is not to be written for the first
// time during the release that renames something.
//
// It is an internal test for that reason alone: parseConfigFile takes the table
// as a parameter precisely so this can be proven without a real rename, and an
// external test cannot reach it.

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestParseConfigFileAcceptsAFormerSpelling(t *testing.T) {
	t.Parallel()

	renames := map[string]string{"old_listen": KeyForVar(EnvListen)}
	var warn bytes.Buffer

	values, err := parseConfigFile("config", []byte("old_listen = 127.0.0.1:9999\n"), renames, &warn)
	if err != nil {
		t.Fatalf("parseConfigFile() refused a renamed key: %v", err)
	}
	if got := values[EnvListen]; got != "127.0.0.1:9999" {
		t.Errorf("%s = %q, want the value the former spelling carried", EnvListen, got)
	}

	// Loud, and naming both spellings: that is what tells an operator whose file
	// predates a rename apart from one who made a typo.
	said := warn.String()
	for _, want := range []string{"config", "old_listen", KeyForVar(EnvListen)} {
		if !strings.Contains(said, want) {
			t.Errorf("startup said %q, want it to name %q", said, want)
		}
	}
}

// The rule the comment on #65 asked not be softened by migration existing.
func TestParseConfigFileStillRefusesAnUnknownKeyWithRenamesInPlay(t *testing.T) {
	t.Parallel()

	renames := map[string]string{"old_listen": KeyForVar(EnvListen)}
	_, err := parseConfigFile("config", []byte("alowed_roots = /tmp\n"), renames, &bytes.Buffer{})
	if err == nil {
		t.Fatal("parseConfigFile() accepted an unknown key")
	}
	if !errors.Is(err, ErrConfigFile) {
		t.Errorf("error = %v, want ErrConfigFile", err)
	}
}

// A warning nobody receives is a warning that was not emitted, which is the same
// rule warnDefaultRoot follows.
func TestParseConfigFileFailsWhenTheRenameWarningCannotBeEmitted(t *testing.T) {
	t.Parallel()

	renames := map[string]string{"old_listen": KeyForVar(EnvListen)}
	_, err := parseConfigFile("config", []byte("old_listen = 127.0.0.1:9999\n"), renames, failingWriter{})
	if err == nil {
		t.Fatal("parseConfigFile() carried on after the rename warning could not be written")
	}
}

// renamedKeys is consulted by key, so an entry pointing at a key no variable
// backs would be an accepted spelling for a setting that does not exist.
func TestEveryRenameTargetsARealKey(t *testing.T) {
	t.Parallel()

	keys := make(map[string]bool, len(Vars()))
	for _, name := range Vars() {
		keys[KeyForVar(name)] = true
	}
	for former, current := range renamedKeys {
		if !keys[current] {
			t.Errorf("renamedKeys maps %q to %q, which no variable backs", former, current)
		}
		if keys[former] {
			t.Errorf("renamedKeys lists %q as a former spelling and it is still a current key", former)
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no writer") }
