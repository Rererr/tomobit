package face

import (
	"strconv"
	"strings"
)

// Mode selects the color capability of the target terminal.
type Mode int

const (
	// Color256 renders with ANSI 256-color SGR sequences.
	Color256 Mode = iota
	// Mono renders shape only, no SGR — for NO_COLOR terminals.
	Mono
)

const (
	transparent = '.'
	sgrReset    = "\x1b[0m"
)

// Render draws one sprite frame as 6 lines of half-blocks (ADR-0008
// Decision 4): each output row packs two logical pixel rows, foreground for
// the top pixel and background for the bottom. Every line ends with an SGR
// reset (Color256) and a newline.
func Render(stage, frame int, mode Mode) string {
	grid := sprites[stage][frame]
	var b strings.Builder
	for row := 0; row < spriteHeight; row += 2 {
		top := grid[row]
		bottom := grid[row+1]
		for col := 0; col < spriteWidth; col++ {
			renderPixelPair(&b, top[col], bottom[col], mode)
		}
		if mode == Color256 {
			b.WriteString(sgrReset)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func renderPixelPair(b *strings.Builder, top, bottom byte, mode Mode) {
	topOn := top != transparent
	bottomOn := bottom != transparent

	if mode == Mono {
		switch {
		case topOn && bottomOn:
			b.WriteRune('█')
		case topOn:
			b.WriteRune('▀')
		case bottomOn:
			b.WriteRune('▄')
		default:
			b.WriteRune(' ')
		}
		return
	}

	switch {
	case topOn && bottomOn:
		b.WriteString("\x1b[38;5;")
		b.WriteString(strconv.Itoa(palette256[top]))
		b.WriteString(";48;5;")
		b.WriteString(strconv.Itoa(palette256[bottom]))
		b.WriteByte('m')
		b.WriteRune('▀')
	case topOn:
		b.WriteString("\x1b[38;5;")
		b.WriteString(strconv.Itoa(palette256[top]))
		b.WriteByte('m')
		b.WriteRune('▀')
	case bottomOn:
		b.WriteString("\x1b[38;5;")
		b.WriteString(strconv.Itoa(palette256[bottom]))
		b.WriteByte('m')
		b.WriteRune('▄')
	default:
		b.WriteRune(' ')
	}
}
