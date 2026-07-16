package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFileMissingIsZeroConfigNotError(t *testing.T) {
	c, err := LoadFile(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if c.ClaudeConfigDir != nil {
		t.Errorf("missing file must mean 'never chosen': %+v", c)
	}
}

func TestLoadFileBrokenIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(p); err == nil {
		t.Fatal("a broken config must error, never silently downgrade to defaults")
	}
}

func TestRoundTripKeepsAbsentVsEmptyDistinct(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")

	empty := ""
	if err := SaveFile(p, Config{ClaudeConfigDir: &empty, ClaudeArgs: []string{"--x"}}); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.ClaudeConfigDir == nil || *c.ClaudeConfigDir != "" {
		t.Errorf(`explicit "" (inherit) must survive the round trip: %+v`, c.ClaudeConfigDir)
	}
	if len(c.ClaudeArgs) != 1 || c.ClaudeArgs[0] != "--x" {
		t.Errorf("args round trip: %v", c.ClaudeArgs)
	}

	if err := SaveFile(p, Config{DB: "/x/db"}); err != nil {
		t.Fatal(err)
	}
	if c, err = LoadFile(p); err != nil {
		t.Fatal(err)
	}
	if c.ClaudeConfigDir != nil {
		t.Errorf("absent field must load as nil (never chosen): %+v", c.ClaudeConfigDir)
	}
}

func TestSaveFileCreatesParentDir(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "config.json")
	if err := SaveFile(p, Config{DB: "x"}); err != nil {
		t.Fatalf("save must create the parent dir: %v", err)
	}
}
