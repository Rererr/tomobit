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
func (c Config) hasAnyOtherFieldSet() bool {
	return c.ClaudeConfigDir != nil ||
		len(c.ClaudeArgs) > 0 ||
		c.DB != "" ||
		c.MLXURL != "" ||
		c.MLXModel != "" ||
		c.FaceAutoLaunch != nil ||
		c.FaceResident != nil ||
		c.SplitProtocol != nil
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
