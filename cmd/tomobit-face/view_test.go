package main

// ADR-0048: `--view json` is the one path through this binary that must not
// touch the desktop. These tests pin the shape a third renderer decodes and
// the rejection of a --view nobody implements.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/facewin"
)

func TestWriteSpriteViewEncodesOneObjectPerLine(t *testing.T) {
	var buf bytes.Buffer
	if err := writeSpriteView(&buf, facewin.BreedShiba); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Count(strings.TrimRight(out, "\n"), "\n") != 0 {
		t.Errorf("output spans more than one line — assets are one object, not a stream")
	}

	var sheet facewin.Sheet
	if err := json.Unmarshal([]byte(out), &sheet); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sheet.Type != "sprite" || sheet.Breed != "shiba" {
		t.Errorf("type/breed = %q/%q, want \"sprite\"/\"shiba\"", sheet.Type, sheet.Breed)
	}
	if len(sheet.Stages) != 6 || len(sheet.Overlays) != 2 {
		t.Errorf("%d stages / %d overlays, want 6 / 2", len(sheet.Stages), len(sheet.Overlays))
	}
}

// A --view the binary does not implement must fail loudly. Silently opening
// the window instead would hand a machine reader a mascot and no stdout.
func TestRunRejectsUnknownView(t *testing.T) {
	err := run([]string{"--view", "ndjson"})
	if err == nil {
		t.Fatal("run(--view ndjson) = nil, want an error")
	}
	if !strings.Contains(err.Error(), "ndjson") {
		t.Errorf("error = %v, want it to name the rejected view", err)
	}
}

func TestRunRejectsUnknownBreedBeforeAnythingOpens(t *testing.T) {
	err := run([]string{"--view", "json", "--breed", "wolf"})
	if err == nil || !strings.Contains(err.Error(), "wolf") {
		t.Fatalf("run(--breed wolf) = %v, want an error naming the breed", err)
	}
}
