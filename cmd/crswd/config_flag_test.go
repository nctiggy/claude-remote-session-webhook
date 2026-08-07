package main

// --config is the first step of the precedence chain (#65), and it is the one
// step that lives outside internal/config. A flag that quietly stopped being
// registered would leave the daemon reading the default path and reporting
// nothing, which is the failure this file exists to make loud.

import (
	"flag"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

func TestConfigFlagIsRegistered(t *testing.T) {
	f := flag.Lookup("config")
	if f == nil {
		t.Fatal("--config is not registered, so the daemon can only ever read the default path")
	}
	if f.DefValue != "" {
		t.Errorf("--config defaults to %q; the empty default is what means \"look in the XDG path\"", f.DefValue)
	}
	if f.Usage == "" {
		t.Error("--config carries no usage text, so -h does not say where the file goes")
	}
}

func TestConfigOptionsFollowTheFlag(t *testing.T) {
	// Not parallel: it writes the flag both halves of the bypass seam read.
	original := *configPath
	t.Cleanup(func() { *configPath = original })

	*configPath = ""
	if got := configOptions(); len(got) != 0 {
		t.Errorf("an unset --config produced %d options; unset must mean the default path, not a file named %q", len(got), "")
	}

	named := filepath.Join(t.TempDir(), "config")
	*configPath = named
	opts := configOptions()
	if len(opts) != 1 {
		t.Fatalf("--config produced %d options, want exactly WithFile", len(opts))
	}

	// The option is proven by what it does rather than by its identity: a
	// WithFile naming a file that is not there is a startup failure, and a
	// loader that ignored the option would refuse for some other reason or not
	// at all.
	_, err := config.Load(opts...)
	if err == nil {
		t.Fatal("config.Load() started without the file --config named")
	}
	if !strings.Contains(err.Error(), named) {
		t.Errorf("error = %q, want the path --config named", err)
	}
}
