package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
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

	// The GUI exports TOMOBIT_CLAUDE_ARGS_APPEND (speaking style), so a shell
	// that ran it would leak an append into every resolution below.
	t.Setenv("TOMOBIT_CLAUDE_ARGS_APPEND", "x")
	os.Unsetenv("TOMOBIT_CLAUDE_ARGS_APPEND")

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

func TestAskFaceAutoLaunch(t *testing.T) {
	yes := true

	// "off" stores an explicit false — the user turning the window off.
	var c config.Config
	if err := askFaceAutoLaunch(bufio.NewReader(strings.NewReader("off\n")), &bytes.Buffer{}, &c); err != nil {
		t.Fatal(err)
	}
	if c.FaceAutoLaunch == nil || *c.FaceAutoLaunch {
		t.Errorf("off must store explicit false: %v", c.FaceAutoLaunch)
	}

	// "on" stores nil, not &true: absent already means on (the default).
	c = config.Config{FaceAutoLaunch: &yes}
	if err := askFaceAutoLaunch(bufio.NewReader(strings.NewReader("on\n")), &bytes.Buffer{}, &c); err != nil {
		t.Fatal(err)
	}
	if c.FaceAutoLaunch != nil {
		t.Errorf("on must reset to nil (default on), not pin a value: %v", *c.FaceAutoLaunch)
	}

	// Enter keeps the current choice untouched.
	no := false
	c = config.Config{FaceAutoLaunch: &no}
	if err := askFaceAutoLaunch(bufio.NewReader(strings.NewReader("\n")), &bytes.Buffer{}, &c); err != nil {
		t.Fatal(err)
	}
	if c.FaceAutoLaunch == nil || *c.FaceAutoLaunch {
		t.Errorf("Enter must keep the current choice (off): %v", c.FaceAutoLaunch)
	}
}

// TestAskPerceiveBackendExplicitChoiceWrites pins that typing a backend name
// writes it (ADR-0029 Decision 5 step 1), and that the question then
// delegates to that backend's own URL/model question (2 further Enters here
// keep those at their defaults).
func TestAskPerceiveBackendExplicitChoiceWrites(t *testing.T) {
	var c config.Config
	in := bufio.NewReader(strings.NewReader("mlx-lm\n\n\n"))
	if err := askPerceiveBackend(in, &bytes.Buffer{}, &c, "ollama"); err != nil {
		t.Fatal(err)
	}
	if c.PerceiveBackend != "mlx-lm" {
		t.Errorf("typing mlx-lm must write it: %q", c.PerceiveBackend)
	}
}

// TestAskPerceiveBackendEnterKeepsAnAlreadyExplicitChoice pins the existing
// setup convention (Enter = keep) applied to the new backend question: an
// already explicit choice survives an Enter untouched, regardless of what
// resolved says.
func TestAskPerceiveBackendEnterKeepsAnAlreadyExplicitChoice(t *testing.T) {
	c := config.Config{PerceiveBackend: "ollama"}
	in := bufio.NewReader(strings.NewReader("\n\n\n"))
	if err := askPerceiveBackend(in, &bytes.Buffer{}, &c, "mlx-lm"); err != nil {
		t.Fatal(err)
	}
	if c.PerceiveBackend != "ollama" {
		t.Errorf("Enter must keep the current choice: %q", c.PerceiveBackend)
	}
}

// TestAskPerceiveBackendEnterWritesTheResolvedDefaultWhenAbsent pins the
// ADR-0029 Decision 3 reversal: unlike this setup's other "Enter = leave
// unwritten" questions, Enter on an absent perceive_backend now WRITES the
// presented default explicitly. An unwritten key here would make a config
// this very run just saved indistinguishable from one that predates the key
// — which is exactly the ambiguity the legacy-detection branch depends on
// never happening for configs this binary writes.
func TestAskPerceiveBackendEnterWritesTheResolvedDefaultWhenAbsent(t *testing.T) {
	var c config.Config
	in := bufio.NewReader(strings.NewReader("\n\n\n"))
	if err := askPerceiveBackend(in, &bytes.Buffer{}, &c, "mlx-lm"); err != nil {
		t.Fatal(err)
	}
	if c.PerceiveBackend != "mlx-lm" {
		t.Errorf("Enter on an absent choice must pin the resolved default, got %q", c.PerceiveBackend)
	}
}

// TestAskPerceiveBackendInvalidAnswerPinsTheDisplayedDefault mirrors the same
// reversal for an unrecognized answer: it must not abort setup, and — since
// leaving the key unwritten is no longer safe — it pins whatever was
// displayed as current instead of silently passing through unwritten.
func TestAskPerceiveBackendInvalidAnswerPinsTheDisplayedDefault(t *testing.T) {
	c := config.Config{PerceiveBackend: "mlx-lm"}
	in := bufio.NewReader(strings.NewReader("bogus\n\n\n"))
	if err := askPerceiveBackend(in, &bytes.Buffer{}, &c, "ollama"); err != nil {
		t.Fatal(err)
	}
	if c.PerceiveBackend != "mlx-lm" {
		t.Errorf("an invalid answer must keep the displayed current choice: %q", c.PerceiveBackend)
	}
}

// TestAskPerceiveBackendUsesTheProvidedResolvedArgumentNotTheWorkingCopy pins
// that askPerceiveBackend never re-derives a default from c itself: c may
// already carry an earlier question's edits from the same setup run (e.g.
// the claude profile), and re-resolving against that copy was the measured
// bug (a virgin machine's config, once claude was answered, looked
// already-wired and misresolved to ollama on darwin). The caller must supply
// the on-disk resolution instead (resolvePerceiveBackend).
func TestAskPerceiveBackendUsesTheProvidedResolvedArgumentNotTheWorkingCopy(t *testing.T) {
	// c already looks "wired" (a ClaudeConfigDir set, as an earlier question
	// in the same run would leave it) — if askPerceiveBackend re-derived a
	// default from c, ResolveBackend's legacy-detection branch would force
	// "ollama" regardless of what the caller resolved from disk.
	claude := "/already/answered"
	c := config.Config{ClaudeConfigDir: &claude}
	var out bytes.Buffer
	in := bufio.NewReader(strings.NewReader("\n\n\n"))
	if err := askPerceiveBackend(in, &out, &c, "mlx-lm"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "現在: mlx-lm") {
		t.Errorf("the presented default must be the resolved argument verbatim, got: %s", out.String())
	}
	if c.PerceiveBackend != "mlx-lm" {
		t.Errorf("the written value must be the resolved argument verbatim, not re-derived from c: %q", c.PerceiveBackend)
	}
}

// TestAskPerceiveBackendHealsACorruptOnDiskValue pins that a corrupt
// perceive_backend never survives a setup run: it is neither displayed as
// the current choice nor written back on Enter — setup doubles as the
// diagnosis, so the corrupt key is replaced by the caller's resolved
// fallback.
func TestAskPerceiveBackendHealsACorruptOnDiskValue(t *testing.T) {
	c := config.Config{PerceiveBackend: "bogus"}
	var out bytes.Buffer
	in := bufio.NewReader(strings.NewReader("\n\n\n"))
	if err := askPerceiveBackend(in, &out, &c, "ollama"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "bogus") {
		t.Errorf("a corrupt value must not be displayed as the current choice: %s", out.String())
	}
	if c.PerceiveBackend != "ollama" {
		t.Errorf("Enter must write the resolved fallback, not keep the corrupt value: %q", c.PerceiveBackend)
	}
}

// TestResolvePerceiveBackendResolvesTheGivenOnDiskSnapshot pins that the
// helper resolves exactly the snapshot it is handed — cmdSetup and
// askClaudeProfile pass the config as loaded at process start, BEFORE any
// question in the current run has mutated a working copy.
func TestResolvePerceiveBackendResolvesTheGivenOnDiskSnapshot(t *testing.T) {
	want := "ollama"
	if runtime.GOOS == "darwin" {
		want = "mlx-lm"
	}
	if got := resolvePerceiveBackend(config.Config{}); got != want {
		t.Errorf("a virgin on-disk config must resolve to the goos default (%s), got %q", want, got)
	}

	dir := "/legacy"
	if got := resolvePerceiveBackend(config.Config{ClaudeConfigDir: &dir}); got != "ollama" {
		t.Errorf("any legacy field set on disk must resolve to ollama, got %q", got)
	}
}

// TestResolvePerceiveBackendInvalidValueFallsBackToTheRestOfTheWiring pins
// that a broken on-disk perceive_backend falls back to resolving the SAME
// config with only the broken key blanked — every legacy signal (ollama_*
// keys and any other pre-existing field) must keep counting, so an
// already-wired machine is never misread as virgin just because
// perceive_backend itself is corrupt.
func TestResolvePerceiveBackendInvalidValueFallsBackToTheRestOfTheWiring(t *testing.T) {
	c := config.Config{PerceiveBackend: "bogus", OllamaURL: "http://legacy:1"}
	if got := resolvePerceiveBackend(c); got != "ollama" {
		t.Errorf("an invalid perceive_backend must still resolve via the ollama_* wiring, got %q", got)
	}

	// The measured shape of the real dev machine: claude keys only, no
	// ollama_* trace. With a corrupt perceive_backend on top, the fallback
	// must still see the claude wiring and stay on ollama (on every goos).
	dir := "/legacy"
	c = config.Config{PerceiveBackend: "bogus", ClaudeConfigDir: &dir}
	if got := resolvePerceiveBackend(c); got != "ollama" {
		t.Errorf("an invalid perceive_backend on a claude-only config must stay on ollama, got %q", got)
	}
}

// TestAskClaudeProfilePartialSavePinsPerceiveBackendFromOnDiskConfig pins
// that a partial save (do finding no claude profile mid-run) still upholds
// "every config this binary writes carries perceive_backend" (ADR-0029
// Decision 3), resolved from the on-disk config as it stood before this
// save — an already-wired machine keeps resolving to ollama.
func TestAskClaudeProfilePartialSavePinsPerceiveBackendFromOnDiskConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	savedCfg, savedErr := cfg, cfgErr
	t.Cleanup(func() { cfg, cfgErr = savedCfg, savedErr; wireClaude() })

	// An already-wired desk: ollama_url set, perceive_backend never written
	// (the exact shape of the measured regression).
	cfg, cfgErr = config.Config{OllamaURL: "http://legacy:1"}, nil

	in := bufio.NewReader(strings.NewReader("0\n")) // 0 = inherit
	if err := askClaudeProfile(in, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if cfg.PerceiveBackend != "ollama" {
		t.Errorf("a partial save on an already-wired machine must pin ollama, got %q", cfg.PerceiveBackend)
	}

	onDisk, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.PerceiveBackend != "ollama" {
		t.Errorf("the saved file itself must carry perceive_backend, got %q", onDisk.PerceiveBackend)
	}
}

// TestAskClaudeProfilePartialSaveOnVirginMachinePinsTheGOOSDefault mirrors
// the above for a machine with nothing on disk at all: the partial save must
// still pin ResolveBackend's goos-based default (mlx-lm on darwin).
func TestAskClaudeProfilePartialSaveOnVirginMachinePinsTheGOOSDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	savedCfg, savedErr := cfg, cfgErr
	t.Cleanup(func() { cfg, cfgErr = savedCfg, savedErr; wireClaude() })

	cfg, cfgErr = config.Config{}, nil

	in := bufio.NewReader(strings.NewReader("0\n"))
	if err := askClaudeProfile(in, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	want := "ollama"
	if runtime.GOOS == "darwin" {
		want = "mlx-lm"
	}
	if cfg.PerceiveBackend != want {
		t.Errorf("a virgin machine's partial save must pin the goos default (%s), got %q", want, cfg.PerceiveBackend)
	}
}
