// tomobit-face — Tomo's face window: a display-only second renderer over the
// same SQLite truth (ADR-0020). It reads; it never writes. Work stays in the
// terminal, the companion lives on the desktop.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/Rererr/tomobit/internal/config"
	"github.com/Rererr/tomobit/internal/facelock"
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
	debug := fs.Bool("debug", false, "keep raw stderr (macOS system noise included — needed to see panics)")
	resident := fs.Bool("resident", false, "0セッションでも常駐（既定は最後の対話終了後に自閉）")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), `tomobit-face — Tomoのマスコット窓（表示専用・DBは読み取りのみ）

ドラッグで移動 / Esc・Qで終了。回答は端末（tomobit）側で。`)
		fs.PrintDefaults()
	}
	fs.Parse(args)

	// One mascot per machine (ADR-0025 Decision 2): if another face already
	// holds the lock, exit quietly — a manual second launch is a no-op, not an
	// error to report. The lock stays held for this whole process (the OS frees
	// it on exit or crash), so it is never released here.
	lock, held := holdFaceLock(os.Stderr)
	if held {
		return nil
	}
	if lock != nil {
		// Keep the reference alive for the process's life: dropping it would let
		// the finalizer close the fd and silently drop the flock. The deferred
		// Release both pins it and frees it explicitly when the window closes.
		defer lock.Release()
	}

	// Before anything can print: our own writers follow the os.Stderr var,
	// so the font fallback warning and exit errors survive the rerouting.
	if !*debug {
		if err := silenceSystemStderr(); err != nil {
			fmt.Fprintln(os.Stderr, "stderr rerouting failed (continuing loud):", err)
		}
	}

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

	g := facewin.NewGame(&facewin.Poller{Path: *db}, breed, *scale, *plain, font, *resident)
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

// holdFaceLock takes the machine-wide face lock (ADR-0025 Decision 2). The
// returned Lock must stay referenced for the process's life — its fd holds the
// flock. held=true means another face already owns the lock, so this one
// should exit quietly. A non-held failure (a broken ~/.tomobit) is best-effort:
// warn and run anyway, since the lock is a courtesy guard, not a gate on the
// window ever opening.
func holdFaceLock(warn io.Writer) (lock *facelock.Lock, held bool) {
	path, err := facelock.DefaultPath()
	if err != nil {
		fmt.Fprintln(warn, "warning: face lock:", err)
		return nil, false
	}
	lock, err = facelock.Acquire(path)
	if err != nil {
		if errors.Is(err, facelock.ErrHeld) {
			return nil, true
		}
		fmt.Fprintln(warn, "warning: face lock:", err)
		return nil, false
	}
	return lock, false
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
