// Package config is the machine-local wiring file (ADR-0021): which Claude
// profile runs, where the truth DB lives, which Ollama serves perception.
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
