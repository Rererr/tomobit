// tomobit-face — Tomo's face window: a display-only second renderer over the
// same SQLite truth (ADR-0020). It reads; it never writes. Work stays in the
// terminal, the companion lives on the desktop.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/Rererr/tomobit/internal/config"
	"github.com/Rererr/tomobit/internal/facewin"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("tomobit-face", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "database file (read-only)")
	breedName := fs.String("breed", "shiba", "dog breed: shiba / retriever / pom")
	scale := fs.Int("scale", 4, "integer pixel scale (sprite is 32px square)")
	plain := fs.Bool("plain", false, "ordinary decorated window (no transparency / always-on-top)")
	fontPath := fs.String("font", "", "font file for the bubble (.ttf/.otf/.ttc; default: system Japanese font)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), `tomobit-face — Tomoのマスコット窓（表示専用・DBは読み取りのみ）

ドラッグで移動 / Esc・Qで終了。回答は端末（tomobit）側で。`)
		fs.PrintDefaults()
	}
	fs.Parse(args)

	breed, ok := facewin.ParseBreed(*breedName)
	if !ok {
		return fmt.Errorf("unknown --breed %q (shiba / retriever / pom)", *breedName)
	}
	if *scale < 1 {
		return fmt.Errorf("--scale must be a positive integer (integer nearest-neighbor only)")
	}

	font, err := facewin.LoadFontSource(*fontPath)
	if err != nil {
		return err
	}

	g := facewin.NewGame(&facewin.Poller{Path: *db}, breed, *scale, *plain, font)
	w, h := g.Size()

	ebiten.SetWindowTitle("Tomo")
	ebiten.SetWindowSize(w, h)
	if !*plain {
		ebiten.SetWindowDecorated(false)
		ebiten.SetWindowFloating(true)
	}
	// Start in the bottom-right corner — a mascot's natural perch. Draggable
	// from there.
	if mw, mh := ebiten.Monitor().Size(); mw > w && mh > h {
		ebiten.SetWindowPosition(mw-w-24, mh-h-48)
	}

	return ebiten.RunGameWithOptions(g, &ebiten.RunGameOptions{
		ScreenTransparent: !*plain,
	})
}

// defaultDB mirrors cmd/tomobit's --db default: $TOMOBIT_DB, then the
// config file, then ~/.tomobit/tomobit.db — the two renderers must look at
// the same truth.
func defaultDB() string {
	if v := os.Getenv("TOMOBIT_DB"); v != "" {
		return v
	}
	if c, err := config.Load(); err == nil && c.DB != "" {
		return c.DB
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "tomobit.db"
	}
	return filepath.Join(home, ".tomobit", "tomobit.db")
}
