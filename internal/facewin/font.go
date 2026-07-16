package facewin

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strings"

	text "github.com/hajimehoshi/ebiten/v2/text/v2"
	"golang.org/x/image/font/gofont/goregular"
)

// defaultFontPaths are tried in order when --font is not given. System fonts
// keep the binary small while the font-embedding Open Question (ADR-0020)
// stays open; the goregular fallback below guarantees the bubble always
// renders something, ASCII-only at worst.
var defaultFontPaths = map[string][]string{
	"darwin": {
		"/System/Library/Fonts/ヒラギノ角ゴシック W3.ttc",
		"/System/Library/Fonts/ヒラギノ角ゴシック W4.ttc",
		"/System/Library/Fonts/Hiragino Sans GB.ttc",
	},
	"linux": {
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
	},
	"windows": {
		`C:\Windows\Fonts\meiryo.ttc`,
		`C:\Windows\Fonts\msgothic.ttc`,
	},
}

// LoadFontSource resolves the bubble's typeface: the explicit path if given,
// then the platform candidates, then the embedded Go Regular (ASCII-only —
// a warning is printed since Tomo speaks Japanese).
func LoadFontSource(path string) (*text.GoTextFaceSource, error) {
	if path != "" {
		src, err := fontSourceFromFile(path)
		if err != nil {
			return nil, fmt.Errorf("facewin: --font %s: %w", path, err)
		}
		return src, nil
	}
	for _, p := range defaultFontPaths[runtime.GOOS] {
		if src, err := fontSourceFromFile(p); err == nil {
			return src, nil
		}
	}
	fmt.Fprintln(os.Stderr, "facewin: no Japanese system font found — bubble text falls back to ASCII (use --font <path>)")
	return text.NewGoTextFaceSource(bytes.NewReader(goregular.TTF))
}

func fontSourceFromFile(path string) (*text.GoTextFaceSource, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(strings.ToLower(path), ".ttc") {
		srcs, err := text.NewGoTextFaceSourcesFromCollection(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		if len(srcs) == 0 {
			return nil, fmt.Errorf("empty font collection")
		}
		return srcs[0], nil
	}
	return text.NewGoTextFaceSource(bytes.NewReader(b))
}
