package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/config"
)

func TestClaudeProfileCandidatesListsClaudeDirsOnly(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{".claude", ".claude-personal", ".claude-work", ".config"} {
		if err := os.Mkdir(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A file with the prefix must not appear: profiles are directories.
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := claudeProfileCandidates(home)
	want := []string{
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".claude-personal"),
		filepath.Join(home, ".claude-work"),
	}
	if len(got) != len(want) {
		t.Fatalf("candidates: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidates[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

// wireClaude resolution: env wins over config, config wins over nothing,
// and "nothing" refuses (claudeProfileSet=false).
func TestWireClaudePrecedence(t *testing.T) {
	savedCfg, savedErr := cfg, cfgErr
	defer func() { cfg, cfgErr = savedCfg, savedErr; wireClaude() }()

	dir := "/cfg/profile"
	cfg, cfgErr = config.Config{ClaudeConfigDir: &dir, ClaudeArgs: []string{"--from-config"}}, nil

	t.Run("config alone wires the adapter", func(t *testing.T) {
		// Ensure the env var is absent even if the developer's shell has it.
		t.Setenv("TOMOBIT_CLAUDE_CONFIG_DIR", "x")
		os.Unsetenv("TOMOBIT_CLAUDE_CONFIG_DIR")
		t.Setenv("TOMOBIT_CLAUDE_ARGS", "x")
		os.Unsetenv("TOMOBIT_CLAUDE_ARGS")
		wireClaude()
		if !claudeProfileSet || claudeAdapter.ConfigDir != "/cfg/profile" {
			t.Errorf("config must wire the profile: set=%v dir=%q", claudeProfileSet, claudeAdapter.ConfigDir)
		}
		if len(claudeAdapter.ExtraArgs) != 1 || claudeAdapter.ExtraArgs[0] != "--from-config" {
			t.Errorf("config must wire the args: %v", claudeAdapter.ExtraArgs)
		}
	})

	t.Run("env overrides config", func(t *testing.T) {
		t.Setenv("TOMOBIT_CLAUDE_CONFIG_DIR", "/env/profile")
		t.Setenv("TOMOBIT_CLAUDE_ARGS", "")
		wireClaude()
		if claudeAdapter.ConfigDir != "/env/profile" {
			t.Errorf("env must win over config: %q", claudeAdapter.ConfigDir)
		}
		if len(claudeAdapter.ExtraArgs) != 0 {
			t.Errorf("empty env args must mean none, not config's: %v", claudeAdapter.ExtraArgs)
		}
	})

	t.Run("no choice anywhere refuses", func(t *testing.T) {
		t.Setenv("TOMOBIT_CLAUDE_CONFIG_DIR", "x")
		os.Unsetenv("TOMOBIT_CLAUDE_CONFIG_DIR")
		cfg = config.Config{}
		wireClaude()
		if claudeProfileSet {
			t.Error("with no env and no config there is no choice — do must refuse")
		}
	})
}

func TestAskProfileNumberPicksCandidateAndZeroMeansInherit(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude-personal"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	var c config.Config
	if err := askProfile(bufio.NewReader(strings.NewReader("1\n")), &bytes.Buffer{}, &c); err != nil {
		t.Fatal(err)
	}
	if c.ClaudeConfigDir == nil || *c.ClaudeConfigDir != filepath.Join(home, ".claude-personal") {
		t.Errorf("1 must pick the first candidate: %v", c.ClaudeConfigDir)
	}

	if err := askProfile(bufio.NewReader(strings.NewReader("0\n")), &bytes.Buffer{}, &c); err != nil {
		t.Fatal(err)
	}
	if c.ClaudeConfigDir == nil || *c.ClaudeConfigDir != "" {
		t.Errorf(`0 must store the explicit "" (inherit): %v`, c.ClaudeConfigDir)
	}

	c = config.Config{}
	if err := askProfile(bufio.NewReader(strings.NewReader("\n")), &bytes.Buffer{}, &c); err == nil {
		t.Error("Enter with nothing configured must not silently choose")
	}
}
