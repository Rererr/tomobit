// Package config is the machine-local wiring file (ADR-0021): which Claude
// profile runs, where the truth DB lives, which local perception server
// (Ollama / MLX LM, ADR-0029) serves perception.
// It deliberately lives OUTSIDE the experience DB — the SQLite file is the
// portable experience (ADR-0018: 住む・持ち運べる形), while paths and
// profiles describe this machine and must not travel with it.
//
// Every read site resolves flag > env > config: config is the durable
// choice `tomobit setup` writes, env is a session-scoped override (one
// shell, one daemon unit, one test — without touching the durable choice),
// a flag is a one-run override.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Config is the file's whole shape (v1). ClaudeConfigDir is a pointer
// because absent and empty mean different things: nil = never chosen, so
// `do` refuses to launch claude-code (ADR-0006 実装追記); "" = explicitly
// inherit the parent environment.
type Config struct {
	ClaudeConfigDir *string  `json:"claude_config_dir,omitempty"`
	ClaudeArgs      []string `json:"claude_args,omitempty"`
	DB              string   `json:"db,omitempty"`
	OllamaURL       string   `json:"ollama_url,omitempty"`
	OllamaModel     string   `json:"ollama_model,omitempty"`
	// PerceiveBackend selects the local perception server (ADR-0029): "ollama"
	// or "mlx-lm". A plain string (not a pointer) is enough here — unlike
	// ClaudeConfigDir, the domain has no valid empty value to confuse with
	// absence, so "" unambiguously means "let ResolveBackend decide".
	PerceiveBackend string `json:"perceive_backend,omitempty"`
	MLXURL          string `json:"mlx_url,omitempty"`
	MLXModel        string `json:"mlx_model,omitempty"`
	// FaceAutoLaunch is a pointer for the same absent-vs-set reason as
	// ClaudeConfigDir: nil = never chosen, so the ADR-0025 default (on) holds;
	// an explicit false is the user turning the face window off. A plain bool
	// could not tell "left at the default" from "deliberately disabled".
	FaceAutoLaunch *bool `json:"face_auto_launch,omitempty"`
	// FaceResident is the same absent-vs-set pointer, orthogonal to
	// FaceAutoLaunch (ADR-0027): the latter is "open the window", this is "keep
	// the window after the conversation ends". nil = the default, which is
	// false = ephemeral (the window self-closes once no chat/do is alive); an
	// explicit true keeps the old ADR-0025 behavior (stays until Esc/Q).
	FaceResident *bool `json:"face_resident,omitempty"`
	// SplitProtocol is the ADR-0028 kill switch (Decision 1). A pointer for the
	// same absent-vs-set reason as the face fields, but the default is on: nil =
	// key absent = the split protocol rides every eligible do (always-on), and an
	// explicit false is the opt-out that stops it without reverting to opt-in
	// (judgment stays zero — a machine either splits by Provider judgment or not
	// at all). A plain bool's zero value would read a config that predates the
	// key as "disabled", silently downgrading every existing machine.
	SplitProtocol *bool `json:"split_protocol,omitempty"`
	// QuotaObserve gates the ADR-0044 route-A observers (ADR-0049). A pointer
	// for the same absent-vs-set reason as the others, but unlike SplitProtocol
	// the default is OFF: nil = key absent = do not read the Keychain and do not
	// call the vendor usage endpoint. The asymmetry is deliberate — this is the
	// one organ that reads a credential and touches a network endpoint the
	// vendor never documented, so "the user never said yes" must not mean yes.
	QuotaObserve *bool `json:"quota_observe,omitempty"`
	// IsolateProtocol is the ADR-0050 kill switch, shaped exactly like
	// SplitProtocol and defaulting the same way: nil = key absent = the
	// isolation protocol rides every eligible run, an explicit false stops it.
	// Opt-out, not a reversion to opt-in — a machine either asks Providers to
	// split their workspace off or it does not, and that is not a per-run
	// judgment anyone should have to make.
	IsolateProtocol *bool `json:"isolate_protocol,omitempty"`
	// TestCommands maps a working directory to the command that observes whether
	// its tests pass (ADR-0052 Decision 2). "How this project runs its tests" is
	// wiring, in the same sense ADR-0047 gave the word: it describes this machine
	// and this checkout, not an experience, so it lives here and never in the DB.
	//
	// tomobit holds one string per place and knows no test runner — `go`, `npm`
	// and `pytest` are outside its vocabulary, the way ADR-0050 keeps VCS names
	// outside it. An absent map (or no matching place) means nothing runs: the
	// feature is opt-in by construction, so silence stays silence (ADR-0049).
	//
	// The command runs at every task boundary of a matching place, through
	// `sh -c`, and only its exit code is read.
	TestCommands map[string]string `json:"test_commands,omitempty"`
	// TestTimeoutSec bounds that command (ADR-0052 Decision 4 / 実装時ノブ). 0 or
	// absent means the default; a timeout records nothing, since a runner that
	// never finished observed nothing about the deliverable.
	TestTimeoutSec int `json:"test_timeout_sec,omitempty"`
}

// QuotaObserveEnabled resolves the ADR-0049 gate: absent key = off. Kept on
// Config (not in cmd/) because both the CLI and any future renderer must read
// the same answer — a second copy of this default is a second place to get the
// "silence means no" wrong.
func (c Config) QuotaObserveEnabled() bool {
	return c.QuotaObserve != nil && *c.QuotaObserve
}

// ResolveBackend picks which perception backend serves this machine
// (ADR-0029 Decision 3). goos is injected (rather than read from runtime.GOOS
// here) so the "unwired machine" branch is pinnable in a test without
// actually running on darwin.
func (c Config) ResolveBackend(goos string) (string, error) {
	switch c.PerceiveBackend {
	case "ollama", "mlx-lm":
		return c.PerceiveBackend, nil
	case "":
		// fall through to the legacy/goos resolution below
	default:
		return "", fmt.Errorf("config: unknown perceive_backend %q (ollama, mlx-lm)", c.PerceiveBackend)
	}
	// A machine already wired to Ollama before perceive_backend existed keeps
	// running on Ollama — key-absence must never silently move a configured
	// machine to a different backend, Mac or not.
	if c.OllamaURL != "" || c.OllamaModel != "" {
		return "ollama", nil
	}
	// Measured on the dev machine (Mac, Ollama run at its own defaults): its
	// on-disk config held only claude_config_dir/claude_args — ollama_url and
	// ollama_model were never written because the defaults already worked, so
	// "this machine has been using Ollama" is NOT detectable from the
	// ollama_* keys alone (a defaults-only wiring leaves no trace in config).
	// Without this check that config misread as a virgin Mac and jumped to
	// mlx-lm — a 404 against whatever unrelated process happened to be
	// listening on :8080. So ANY other field already set means this config
	// predates perceive_backend and must not be silently moved off Ollama.
	if c.hasAnyOtherFieldSet() {
		return "ollama", nil
	}
	if goos == "darwin" {
		return "mlx-lm", nil
	}
	return "ollama", nil
}

// hasAnyOtherFieldSet reports whether any field besides PerceiveBackend and
// the already-checked Ollama fields carries a value — the signal that this
// config was written before perceive_backend existed (a config nobody has
// ever touched is exactly zero in every field).
//
// Fields added AFTER perceive_backend must NOT be listed here: they cannot
// appear in a config that predates it, so counting them would misread a fresh
// Mac that only set the new key as a legacy Ollama machine and silently pin it
// to the wrong backend. TestCommands / TestTimeoutSec (ADR-0052) are excluded
// for exactly this reason — this list is a fossil marker, not a "config is
// non-empty" check.
func (c Config) hasAnyOtherFieldSet() bool {
	return c.ClaudeConfigDir != nil ||
		len(c.ClaudeArgs) > 0 ||
		c.DB != "" ||
		c.MLXURL != "" ||
		c.MLXModel != "" ||
		c.FaceAutoLaunch != nil ||
		c.FaceResident != nil ||
		c.SplitProtocol != nil ||
		c.QuotaObserve != nil
}

// Path is ~/.tomobit/config.json — beside the default DB, never inside it.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tomobit", "config.json"), nil
}

// Load reads Path. A missing file is not an error — it is the zero Config
// (nothing configured yet). A present-but-broken file IS an error, so a
// typo never silently downgrades the machine to defaults.
func Load() (Config, error) {
	p, err := Path()
	if err != nil {
		return Config{}, err
	}
	return LoadFile(p)
}

// LoadFile is Load for an explicit path (tests, alternate homes).
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Save writes the whole Config to Path via temp-file-and-rename, so a crash
// never leaves half a config behind.
func Save(c Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	return SaveFile(p, c)
}

// SaveFile is Save for an explicit path.
func SaveFile(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
