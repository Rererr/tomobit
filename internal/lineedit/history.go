package lineedit

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// SetHistoryFile loads path into the ring and remembers it for later appends,
// with warn as the sink for append failures (ADR-0024 Decision 1). The load
// failure is returned, not fatal: history is UI state, not the ledger, and has
// no right to keep a chat from opening — the caller warns once and carries on.
//
// A file longer than maxHistory is compacted on load, so appending across many
// sessions cannot grow it without bound. A missing file is not an error: the
// first process to run simply has no past to recall.
func (e *Editor) SetHistoryFile(path string, warn io.Writer) error {
	e.histPath, e.histWarn = path, warn

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("lineedit: reading history %s: %w", path, err)
	}

	var lines []string
	for _, ln := range strings.Split(string(data), "\n") {
		if ln == "" {
			continue
		}
		s := decodeHistoryLine(ln)
		if strings.TrimSpace(s) == "" {
			continue
		}
		if n := len(lines); n > 0 && lines[n-1] == s {
			continue
		}
		lines = append(lines, s)
	}
	e.histLines = len(lines)
	if len(lines) > maxHistory {
		lines = lines[len(lines)-maxHistory:]
		// The ring keeps what was read even when the compaction cannot be
		// written back: losing a loaded past over a failed disk optimisation
		// would be worse than the growth it prevents (the append path already
		// keeps the ring alive on a write failure, for the same reason).
		e.history = lines
		if err := e.rewriteHistory(lines); err != nil {
			return err
		}
		e.histLines = len(lines)
		return nil
	}
	e.history = lines
	return nil
}

func (e *Editor) appendHistory(s string) {
	if e.histPath == "" {
		return
	}
	// Compact in-flight once the file holds twice the ring: load-time
	// compaction alone leaves a process that never restarts appending
	// without bound. The rewrite carries the ring, which is already capped.
	if e.histLines >= 2*maxHistory {
		if err := e.rewriteHistory(e.history); err != nil {
			e.warnHistory(err)
		} else {
			e.histLines = len(e.history)
			return
		}
	}
	f, err := os.OpenFile(e.histPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_, err = io.WriteString(f, encodeHistoryLine(s)+"\n")
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		e.warnHistory(err)
	} else {
		e.histLines++
	}
}

func (e *Editor) rewriteHistory(lines []string) error {
	var sb strings.Builder
	for _, s := range lines {
		sb.WriteString(encodeHistoryLine(s))
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(e.histPath, []byte(sb.String()), 0o600); err != nil {
		return fmt.Errorf("lineedit: compacting history %s: %w", e.histPath, err)
	}
	return nil
}

// warnHistory reports a write failure once. A read-only home must not go
// silent (that would be swallowing the error), but neither should it print on
// every keystroke — one note, then the ring keeps working in memory alone.
func (e *Editor) warnHistory(err error) {
	if e.histWarned || e.histWarn == nil {
		return
	}
	e.histWarned = true
	fmt.Fprintln(e.histWarn, "lineedit: history not saved:", err)
}

// encodeHistoryLine makes one entry safe for one physical line: a real
// backslash doubles and a newline becomes "\n", so a multi-line pasted task
// round-trips instead of splitting into separate history entries. Order
// matters — backslashes are escaped first, or the "\n" this adds would be
// re-escaped by the backslash pass.
func encodeHistoryLine(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// decodeHistoryLine reverses encodeHistoryLine, scanning left to right so an
// escaped backslash ("\\") is consumed as one and cannot combine with the next
// character into a spurious escape.
func decodeHistoryLine(s string) string {
	var sb strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\\' && i+1 < len(rs) {
			i++
			switch rs[i] {
			case 'n':
				sb.WriteByte('\n')
			default:
				sb.WriteRune(rs[i])
			}
			continue
		}
		sb.WriteRune(rs[i])
	}
	return sb.String()
}
