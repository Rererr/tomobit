package config

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestFaceAutoLaunchAbsentVsExplicit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")

	// Absent → nil, so the ADR-0025 default (on) is free to hold.
	if err := SaveFile(p, Config{DB: "x"}); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.FaceAutoLaunch != nil {
		t.Errorf("absent face_auto_launch must load as nil (never chosen): %v", *c.FaceAutoLaunch)
	}

	// Explicit false must survive — it is the user turning the window off.
	no := false
	if err := SaveFile(p, Config{FaceAutoLaunch: &no}); err != nil {
		t.Fatal(err)
	}
	if c, err = LoadFile(p); err != nil {
		t.Fatal(err)
	}
	if c.FaceAutoLaunch == nil || *c.FaceAutoLaunch {
		t.Errorf("explicit false must survive the round trip: %v", c.FaceAutoLaunch)
	}

	// Explicit true must survive as its own value, distinct from absent.
	yes := true
	if err := SaveFile(p, Config{FaceAutoLaunch: &yes}); err != nil {
		t.Fatal(err)
	}
	if c, err = LoadFile(p); err != nil {
		t.Fatal(err)
	}
	if c.FaceAutoLaunch == nil || !*c.FaceAutoLaunch {
		t.Errorf("explicit true must survive the round trip: %v", c.FaceAutoLaunch)
	}
}

// TestSplitProtocolAbsentVsExplicit mirrors the face fields' absent-vs-set
// contract for the ADR-0028 kill switch: an absent key loads as nil so the
// default (on) can hold, and an explicit false survives the round trip so a
// config predating the key is never confused with a deliberate opt-out.
func TestSplitProtocolAbsentVsExplicit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")

	if err := SaveFile(p, Config{DB: "x"}); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.SplitProtocol != nil {
		t.Errorf("absent split_protocol must load as nil (default on): %v", *c.SplitProtocol)
	}

	no := false
	if err := SaveFile(p, Config{SplitProtocol: &no}); err != nil {
		t.Fatal(err)
	}
	if c, err = LoadFile(p); err != nil {
		t.Fatal(err)
	}
	if c.SplitProtocol == nil || *c.SplitProtocol {
		t.Errorf("explicit false must survive the round trip (the kill switch): %v", c.SplitProtocol)
	}
}

func TestResolveBackend(t *testing.T) {
	cases := []struct {
		name    string
		c       Config
		goos    string
		want    string
		wantErr bool
	}{
		{"explicit ollama wins outright", Config{PerceiveBackend: "ollama"}, "darwin", "ollama", false},
		{"explicit mlx-lm wins outright", Config{PerceiveBackend: "mlx-lm"}, "linux", "mlx-lm", false},
		{"unwired ollama_url pins ollama regardless of goos", Config{OllamaURL: "http://x:1"}, "darwin", "ollama", false},
		{"unwired ollama_model pins ollama regardless of goos", Config{OllamaModel: "qwen3:8b"}, "darwin", "ollama", false},
		{"claude-only config (the measured regression) pins ollama even on darwin", Config{ClaudeConfigDir: strPtr("/x")}, "darwin", "ollama", false},
		{"mlx fields alone (config predates perceive_backend) pin ollama", Config{MLXURL: "http://y:2"}, "darwin", "ollama", false},
		{"completely empty config (virgin machine) defaults to mlx-lm on darwin", Config{}, "darwin", "mlx-lm", false},
		{"completely empty config (virgin machine) defaults to ollama off darwin", Config{}, "linux", "ollama", false},
		{"invalid perceive_backend errors", Config{PerceiveBackend: "bogus"}, "darwin", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.c.ResolveBackend(tc.goos)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got backend %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHasAnyOtherFieldSetEnumeratesEveryConfigField pins Config's field
// count: hasAnyOtherFieldSet lists its fields by hand (all but the three the
// caller checks first), and nothing else forces that list to grow with the
// struct. A new field left out of it would make a machine wired only through
// that field look virgin — and silently move it off Ollama, the exact
// regression ResolveBackend's legacy branch exists to prevent. When this
// fails: add the new field to hasAnyOtherFieldSet, then bump the count.
func TestHasAnyOtherFieldSetEnumeratesEveryConfigField(t *testing.T) {
	const known = 11 // 3 checked by ResolveBackend + 8 in hasAnyOtherFieldSet
	if n := reflect.TypeOf(Config{}).NumField(); n != known {
		t.Errorf("Config grew to %d fields (knew %d): update hasAnyOtherFieldSet and this count together", n, known)
	}
}

func TestSaveFileCreatesParentDir(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "config.json")
	if err := SaveFile(p, Config{DB: "x"}); err != nil {
		t.Fatalf("save must create the parent dir: %v", err)
	}
}

func strPtr(s string) *string { return &s }
