// tomobit chat — the conversational session (ADR-0022).
//
// One chat = one task = one Experience; turns are the breathing inside it.
// The organs `do` runs at its boundary (Feedback → 知覚 → 質問 → 鏡) run here
// too, at /new, /exit or Ctrl-D — the boundary moved from "one process" to
// "one task", which is what it always meant.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Rererr/tomobit/internal/decide"
	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/lineedit"
	"github.com/Rererr/tomobit/internal/mdlite"
	"github.com/Rererr/tomobit/internal/perceive"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/subtask"
	"github.com/Rererr/tomobit/internal/voice"
	"github.com/Rererr/tomobit/internal/workspace"
)

// chatPrompt hangs the marker one column in (gutter 1); everything else the
// chat prints sits at the two-column gutter, so the marker stands just left of
// the conversation instead of both touching the terminal edge.
const chatPrompt = " ❯ "

// gutter is the left margin the chat's answers, notes and wrapped input all
// share, so nothing but the prompt marker touches the terminal edge. Two
// columns matches the tool/footer indent this view already used.
const gutter = "  "

// inputBg is the faint background laid behind the user's typed line, so their
// turn reads as a block distinct from Tomo's answer. A desaturated blue in
// truecolor (48;2;R;G;B): the hue stays blue as tomobit's own colour (not a
// copy of any provider's), but the chroma is halved and the lightness matched
// to Claude Code's measured transcript band (55,55,55), whose softness comes
// from sitting near grey — a fuller blue at this size read as harsh. A
// terminal without truecolor maps it to its nearest palette entry.
const inputBg = "\x1b[48;2;40;49;64m"

// indent prefixes every non-empty line of s with the gutter. Empty lines stay
// empty — a gutter on a blank line is just trailing space. Multi-line safe, so
// a rendered markdown answer moves as one block.
func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if ln != "" {
			lines[i] = gutter + ln
		}
	}
	return strings.Join(lines, "\n")
}

// indentWriter prefixes every non-empty line written through it with the
// gutter, so a whole block of output — the boundary organs, the connections
// table — sits at the chat's margin without each print site (some in other
// packages) knowing about it. Empty lines stay empty. It carries the
// line-start state across Writes, so a line split over several calls is
// prefixed once, at its start, and the organs' own speaker-separation blanks
// re-establish that start after the terminal echoes a typed answer.
type indentWriter struct {
	w       io.Writer
	prefix  string
	pending bool // at a line start: the next non-newline byte takes the prefix
}

func newIndentWriter(w io.Writer, prefix string) *indentWriter {
	return &indentWriter{w: w, prefix: prefix, pending: true}
}

func (iw *indentWriter) Write(p []byte) (int, error) {
	for i := 0; i < len(p); i++ {
		c := p[i]
		if iw.pending && c != '\n' {
			if _, err := io.WriteString(iw.w, iw.prefix); err != nil {
				return i, err
			}
		}
		if _, err := iw.w.Write(p[i : i+1]); err != nil {
			return i, err
		}
		iw.pending = c == '\n'
	}
	return len(p), nil
}

// chat is one process's worth of conversation: the wiring is fixed for the
// whole run, the session fields below are reset at every task boundary.
type chat struct {
	s  *store.Store
	ed *lineedit.Editor
	// in is where the prompts between turns read from — the editor's own
	// buffered view of stdin. One reader per process: a prompt that opened
	// its own over os.Stdin could not see what this one has already pulled in.
	in  *bufio.Reader
	out io.Writer
	// stream is non-nil only under --view ndjson (ADR-0032 Decision 1): the
	// single writer of the NDJSON view stream. When set, out is a note-framing
	// writer over it and the turn view is an ndjsonView, so every byte of stdout
	// is one JSON view event. nil is the plain-text default — the pipe/script
	// path, byte-for-byte unchanged.
	stream *ndjsonStream

	providerName string
	capability   string
	permMode     string
	timeout      time.Duration
	size         string
	// workDir/addDirs は働く場所 (ADR-0047)。他の配線と同じくタスク境界でだけ
	// 替えられる（/cd・/add-dir → setWiring）。空の workDir は tomobit 自身の
	// cwd を継ぐ = 端末で `cd` してから起動した従来の姿。
	workDir   string
	addDirs   []string
	extractor perceive.Extractor
	// interactive is whether the terminal can draw — both stdin and stdout are
	// a TTY (ADR-0035 Decision 2). It gates the split parallelism offer
	// (splitAndFold), which shows a y/N prompt and a cost estimate meant for a
	// screen: a pipe or CI never sees it and stays sequential. It answers "can
	// this render", not "is anyone there" (humanPresent, below) — a view-stream
	// consumer is present with no terminal to draw into. A field, not a live
	// isTTY() call, so a test can drive the accept path deterministically
	// without a real terminal.
	interactive bool
	// humanPresent is whether someone is on the other end of stdin to ask — a
	// TTY, or a declared view stream (--view ndjson). It gates the boundary
	// organs at finishTask (Tomo's question ADR-0007, the mirror ADR-0015),
	// separately from interactive above (ADR-0035 Decision 2): a GUI piping
	// --view ndjson has a person reading, but no terminal to render y/N into.
	humanPresent bool

	// sid == "" means no task is open: the next prompt starts one.
	sid       string
	adapter   executor.Adapter
	human     bool
	threadID  string
	turns     int
	completed bool // a turn ran to completion — there is something to judge
	// perception is this task's Task Perception holder (ADR-0036 Decision
	// 2b), built fresh in startTask and cleared in closeTask along with the
	// rest of the task's fields — its lifetime is one task, same as sid.
	perception *taskPerception
}

func cmdChat(args []string) error {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	db := dbFlag(fs)
	capability := fs.String("cap", "implement", "capability of the task")
	timeout := fs.Duration("timeout", 0, "max run time per turn, 0 = no limit")
	permMode := fs.String("permission-mode", "", "permission mode passthrough (claude --permission-mode / codex --sandbox)")
	providerName := providerFlag(fs)
	size := fs.String("size", "", "task size for decision stakes: small|medium|large (--provider auto)")
	backend := fs.String("backend", "", "perception backend for best-effort perception: ollama|mlx-lm (default: resolved from config)")
	model := fs.String("model", "", "perception model for best-effort perception (default depends on --backend)")
	url := fs.String("url", "", "perception backend url for best-effort perception (default depends on --backend)")
	view := fs.String("view", "", "stdout view stream: ndjson (default: plain text)")
	// 働く場所は起動時にも宣言できる (ADR-0047 Decision 6): /cd・/add-dir と
	// 同じ値を、最初のタスクの前から持たせるための口。GUI のような入口は
	// 起動のたびにコマンドを打ち込まずに済み、会話面が配線の応答で汚れない。
	workDir := fs.String("cd", "", "where Tomo works for this chat (default: this process's cwd)")
	var addDirs idList
	fs.Var(&addDirs, "add-dir", "a place outside --cd Tomo may also work with (repeatable)")
	fs.Parse(args)

	// 存在しない場所は起動前に断る: 立てない場所を cwd にした exec の chdir
	// エラーは、どの配線が悪いか語らない（/cd の check と同じ判定）。
	if err := checkWorkingPlace(*workDir, "--cd"); err != nil {
		return err
	}
	for _, dir := range addDirs {
		if err := checkWorkingPlace(dir, "--add-dir"); err != nil {
			return err
		}
	}

	// The view choice validates before anything launches, like the provider: a
	// bad value or a TTY target is a mistake to catch at the door, not one turn in.
	if err := validateViewFlag(*view, isTTY(os.Stdout)); err != nil {
		return err
	}
	// Both fail before the store is even opened, like `do`: a chat that
	// cannot launch anything is not a chat, and finding out one prompt later
	// would already have cost the user their first typed task.
	if *providerName != "auto" && *providerName != "human" {
		if _, err := resolveProvider(*providerName); err != nil {
			return err
		}
	}
	// Take presence before spawning the face (ADR-0027 Decision 2): the window
	// is born with at least one live conversation, so it never observes 0 during
	// its own startup race. Held for the whole REPL — even between turns — so an
	// idle chat keeps Tomo on screen; released on exit.
	releasePresence := registerPresence(os.Stderr)
	defer releasePresence()
	maybeLaunchFace(*db)
	ed := lineedit.New(os.Stdin, os.Stdout)
	// Wrapped and multi-line input indent to the same gutter as the answers, so
	// a task typed across several lines aligns under the first instead of
	// falling back to the terminal edge.
	ed.WrapIndent = len(gutter)
	// A faint background behind the typed line marks the user's turn apart from
	// Tomo's answer (Claude Code style). Colour, so it rides the same styled()
	// gate as dim — NO_COLOR and pipes never see it.
	if styled() {
		ed.TextStyle = inputBg
	}

	// Under --view ndjson, stdout becomes the NDJSON view stream (ADR-0032
	// Decision 1). The framing writer wraps every plain-text write (organ speech,
	// sayln, showStatus) into a note event, and the flush hook under the shared
	// reader releases a partial question line as an await note the moment a read
	// blocks (flush-on-read). The editor keeps history — the same db-adjacent
	// file — and reads through the same hooked reader, so a prompt between turns
	// shares it. init opens the stream.
	var stream *ndjsonStream
	out := io.Writer(os.Stdout)
	if *view == "ndjson" {
		stream = newNDJSONStream(os.Stdout)
		ed.SetReader(flushReader{r: os.Stdin, flush: stream.flushAwait})
		out = noteWriter{s: stream}
		stream.emit(map[string]any{"type": "init", "v": viewVersion})
	}

	// ensureClaudeProfile still writes its prompt to os.Stdout directly, but that
	// write cannot corrupt the view stream: it only fires in the interactive
	// branch (ADR-0021 Decision 4), and view mode forces a non-TTY stdout →
	// non-interactive → the hard-error branch, which writes nothing to stdout.
	if err := ensureClaudeProfile(ed.Reader(), *providerName); err != nil {
		return err
	}
	s, err := openStore(*db)
	if err != nil {
		return err
	}
	defer s.Close()

	// History lives next to the db (ADR-0024 Decision 1), so `--db` isolates it
	// the same way it isolates the ledger — tests never touch the real one. A
	// read failure warns once and the chat opens anyway.
	if err := ed.SetHistoryFile(*db+".history", os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}

	extractor, err := newExtractor(*backend, *url, *model)
	if err != nil {
		return err
	}
	c := &chat{
		s: s, ed: ed, in: ed.Reader(), out: out, stream: stream,
		providerName: *providerName, capability: *capability,
		permMode: *permMode, timeout: *timeout, size: *size,
		workDir: *workDir, addDirs: addDirs,
		extractor:   extractor,
		interactive: isTTY(os.Stdin) && isTTY(os.Stdout),
		// The declared view stream is the signal (ADR-0035 Decision 1) — not
		// TOMOBIT_FACE, not config: *view is this call's own argv, so a script
		// exporting TOMOBIT_FACE=1 never burns the question budget by accident.
		humanPresent: isTTY(os.Stdin) || *view == "ndjson",
	}
	ed.Completer = c.complete
	// The first screen is the companion view (ADR-0008), and its next line is
	// the prompt (ADR-0022 Decision 4): you meet Tomo and you can talk. A pipe —
	// view stream or plain — skips the greeting, as it always has.
	if isTTY(os.Stdout) {
		// nil collector: the between-turn view stays offline — a per-turn quota
		// fetch is not what a chat is for (ADR-0044).
		if err := showStatus(c.out, s, nil); err != nil {
			return err
		}
		c.sayln(dim("話しかけて。/help でコマンド、Ctrl-D で終了"))
		fmt.Fprintln(c.out)
	}
	// A note left half-written when the chat ends (no read followed to flush it)
	// is emitted rather than dropped at stdout's close (ADR-0032 Decision 1).
	if stream != nil {
		defer stream.flushClose()
	}
	return c.loop(strings.TrimSpace(strings.Join(fs.Args(), " ")))
}

// loop reads turns until the input ends. seed, when non-empty, is the first
// turn (`tomobit chat "..."`), so a one-liner can still open a conversation.
func (c *chat) loop(seed string) error {
	interrupts := 0
	for {
		line := seed
		seed = ""
		if line == "" {
			// The view stream marks the input wait with a ready event instead of
			// the ` ❯ ` marker, and prints no prompt (ADR-0032 Decision 1). A seed
			// turn skips this — it is not standing at the prompt.
			prompt := chatPrompt
			if c.stream != nil {
				c.stream.emit(map[string]any{"type": "ready"})
				prompt = ""
			}
			var err error
			line, err = c.ed.ReadLine(prompt)
			switch {
			case errors.Is(err, lineedit.ErrInterrupt):
				// Ctrl-C at an empty prompt is ambiguous — leaving, or
				// clearing a line? Ask for a second one rather than
				// dropping the task's closing question on a stray keystroke.
				interrupts++
				if interrupts >= 2 {
					return c.closeTask()
				}
				c.sayln(dim("もう一度 Ctrl-C で終了(/exit)"))
				continue
			case errors.Is(err, io.EOF):
				return c.closeTask()
			case err != nil:
				return err
			}
		}
		interrupts = 0
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Commands go into the history too: a mistyped `/provder codex` is
		// exactly the fumble this editor exists to make cheap to fix.
		c.ed.AddHistory(line)
		if strings.HasPrefix(line, "/") {
			done, err := c.command(line)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			continue
		}
		if err := c.turn(line); err != nil {
			return err
		}
	}
}

// command runs a slash command. It returns done=true to leave the chat; a
// returned error is fatal (the ledger is unwritable), while anything the user
// merely got wrong is answered on the spot and the conversation continues.
func (c *chat) command(line string) (done bool, err error) {
	name, arg, _ := strings.Cut(line, " ")
	arg = strings.TrimSpace(arg)
	switch name {
	case "/exit", "/quit":
		return true, c.closeTask()
	case "/new":
		if err := c.closeTask(); err != nil {
			return false, err
		}
		if arg == "" {
			c.sayln(dim("次のタスクへ"))
			return false, nil
		}
		return false, c.turn(arg)
	case "/provider":
		c.setWiring(&c.providerName, arg, "provider", func(v string) error {
			if v != "auto" && v != "human" {
				if _, err := resolveProvider(v); err != nil {
					return err
				}
			}
			return ensureClaudeProfile(c.in, v)
		})
	case "/cd":
		// 働く場所 (ADR-0047 Decision 4)。存在確認はここで済ませる: 実在しない
		// パスを cwd にした exec の chdir エラーは、どの配線が悪いか語らない。
		c.setWiring(&c.workDir, arg, "cd", func(v string) error {
			return checkWorkingPlace(v, "cd")
		})
	case "/add-dir":
		c.addDir(arg)
	case "/cap":
		c.setWiring(&c.capability, arg, "cap", nil)
	case "/size":
		// Same nature as /cap: a per-task value the ledger reads once. It
		// only bites with --provider auto, where it is the stakes of the
		// decision (ADR-0012's n).
		c.setWiring(&c.size, arg, "size", nil)
	case "/status":
		return false, showStatus(c.out, c.s, nil)
	case "/help":
		chatUsage(c.out)
	default:
		c.sayln(fmt.Sprintf("%s — 知らないコマンド。/help", name))
	}
	return false, nil
}

// completableCommands are what Tab completes for the leading token. /quit is
// an alias of /exit and stays out — completing to one spelling is enough, and
// two candidates for the same action would only block the unique-commit path.
var completableCommands = []string{"/new", "/provider", "/cd", "/add-dir", "/cap", "/size", "/status", "/help", "/exit"}

// complete is the editor's Completer (ADR-0024 Decision 4): the leading token
// completes to a command, and the second token of /provider or /size to that
// command's argument vocabulary — the words openTask/decide.Draws actually
// read. Anywhere else it declines, because free-text tasks are the provider's
// to parse, not the chat's to guess.
func (c *chat) complete(text string, pos int) ([]string, int) {
	r := []rune(text)
	if pos < 0 || pos > len(r) {
		return nil, -1
	}
	start := pos
	for start > 0 && r[start-1] != ' ' {
		start--
	}
	tokenIdx := 0
	for i := 0; i < start; i++ {
		if r[i] != ' ' && (i == 0 || r[i-1] == ' ') {
			tokenIdx++
		}
	}
	token := string(r[start:pos])
	switch tokenIdx {
	case 0:
		if !strings.HasPrefix(token, "/") {
			return nil, -1
		}
		return matchPrefix(completableCommands, token), start
	case 1:
		var vocab []string
		switch firstWord(r) {
		case "/provider":
			vocab = []string{"claude-code", "codex", "human", "auto"}
		case "/size":
			vocab = []string{"small", "medium", "large"}
		default:
			return nil, -1
		}
		return matchPrefix(vocab, token), start
	default:
		return nil, -1
	}
}

func firstWord(r []rune) string {
	i := 0
	for i < len(r) && r[i] == ' ' {
		i++
	}
	j := i
	for j < len(r) && r[j] != ' ' {
		j++
	}
	return string(r[i:j])
}

func matchPrefix(vocab []string, prefix string) []string {
	var out []string
	for _, v := range vocab {
		if strings.HasPrefix(v, prefix) {
			out = append(out, v)
		}
	}
	return out
}

// checkWorkingPlace rejects a place Tomo cannot be pointed at (ADR-0047).
// One check for both doors — the launch flags and the slash commands — so a
// path that is refused at startup is refused mid-chat for the same reason,
// in the same words. Empty is fine: it means "unset", not "nowhere".
func checkWorkingPlace(dir, label string) error {
	if dir == "" {
		return nil
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%s: そこには行けない: %s (%v)", label, dir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s: そこはディレクトリではない: %s", label, dir)
	}
	return nil
}

// addDir maintains the places outside the working dir that Tomo may also work
// with (ADR-0047 Decision 4). Three words only: bare lists, "clear" empties,
// anything else is a path to add. Mid-task it refuses like the other wiring —
// same reason (setWiring), and the refusal is worded the same way.
func (c *chat) addDir(arg string) {
	if arg == "" {
		if len(c.addDirs) == 0 {
			c.sayln(dim("add-dir: なし"))
			return
		}
		c.sayln(dim("add-dir: " + strings.Join(c.addDirs, " ")))
		return
	}
	if c.sid != "" {
		c.sayln("タスクの途中では add-dir を替えられない — /new で区切ってから")
		return
	}
	if arg == "clear" {
		c.addDirs = nil
		c.sayln(dim("add-dir: なし"))
		return
	}
	if err := checkWorkingPlace(arg, "add-dir"); err != nil {
		c.sayln(err.Error())
		return
	}
	for _, d := range c.addDirs {
		if d == arg {
			// 二度言われても一度だけ持つ: 同じ場所が argv に2回並ぶ意味はない。
			c.sayln(dim("add-dir: " + strings.Join(c.addDirs, " ")))
			return
		}
	}
	c.addDirs = append(c.addDirs, arg)
	c.sayln(dim("add-dir: " + strings.Join(c.addDirs, " ")))
}

// setWiring changes one of the per-task choices. Mid-task it refuses: an
// experience carries one provider and one capability for the whole session
// (SCHEMA.md), so swapping either halfway would record a task that never
// happened. The ledger's shape is what says no here, not the implementation.
func (c *chat) setWiring(field *string, arg, label string, check func(string) error) {
	if arg == "" {
		c.sayln(dim(label + ": " + *field))
		return
	}
	if c.sid != "" {
		c.sayln(fmt.Sprintf("タスクの途中では %s を替えられない — /new で区切ってから", label))
		return
	}
	if check != nil {
		if err := check(arg); err != nil {
			c.sayln(err.Error())
			return
		}
	}
	*field = arg
	c.sayln(dim(label + ": " + arg))
}

// turn is one exchange: the first opens the task, the rest resume its thread.
// opening (the turn that starts the task — the first, or the first after /new)
// is where the split protocol rides, since intent decomposition only means
// anything at a task's birth (ADR-0028 Decision 1); a continuation turn carries
// no protocol.
func (c *chat) turn(prompt string) error {
	if c.human {
		c.sayln(dim("いまのタスクは君の手にある — 終わったら /new か /exit で区切る"))
		return nil
	}
	opening := c.sid == ""
	if opening {
		if err := c.startTask(prompt); err != nil {
			return err
		}
		if c.human {
			return nil
		}
	} else {
		c.turns++
		if err := c.s.AppendEvent(c.sid, "task.turn", time.Now().UnixMilli(),
			map[string]any{"intent": prompt, "n": c.turns}); err != nil {
			return err
		}
	}
	return c.run(prompt, opening)
}

// startTask opens the ledger session: the first prompt is the task's intent,
// and the provider is chosen once, for the whole conversation.
func (c *chat) startTask(prompt string) error {
	// One holder per task (ADR-0036 Decision 2b): openTask below may be its
	// first asker (--provider auto), or nothing may ever ask (a pinned
	// provider) — either way it lives until closeTask.
	c.perception = newTaskPerception(prompt, taskExtractFuncFor(c.s, c.extractor))
	sid, adapter, human, err := openTask(c.s, c.out, c.providerName, c.capability, c.size, prompt, c.perception)
	if err != nil {
		return err
	}
	c.sid, c.adapter, c.human = sid, adapter, human
	c.turns, c.completed, c.threadID = 1, false, ""
	// The view stream names the open task by its ledger session id (ADR-0032
	// Decision 1), so a GUI can tie later events to the same task.finished.
	if c.stream != nil {
		c.stream.emit(map[string]any{"type": "task.started", "sid": c.sid})
	}

	if c.human {
		// The Human Executor (ADR-0018 Decision 2) inside a chat: the routing
		// is recorded like any provider's, and the closing question is where
		// the human's work lands on the same ledger. There is nothing to
		// wait for — the boundary the user declares is the wait.
		if err := c.s.AppendEvent(c.sid, "provider.selected", time.Now().UnixMilli(),
			map[string]any{"provider": "human"}); err != nil {
			return err
		}
		c.gap()
		c.sayln(fmt.Sprintf("「%s」", voice.RouteHuman()))
		c.sayln(dim("終わったら /new か /exit で区切る"))
		c.gap()
		c.completed = true
	}
	return nil
}

// run executes one turn against the provider, resuming the thread the earlier
// turns opened. opening carries the split protocol (ADR-0028 Decision 1) and is
// the only turn whose output is read for a proposal: the fold-back's own feed
// turn (splitAndFold) reuses this method with opening=false, so the protocol
// text that stays in the thread is never read back as a fresh proposal — depth
// stays 1 (ADR-0023 Decision 4), structurally, not by luck.
func (c *chat) run(prompt string, opening bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// The protocol rides the task's opening turn only, under the same discipline
	// as do (splitProtocolEligible): the kill switch must be on, and a human turn
	// (excluded earlier in turn) never gets it. A chat has no plan step, so
	// planName is always "".
	split := opening && splitProtocolEligible(splitProtocolEnabled(), c.human, "")
	runPrompt := prompt
	var texts []string
	if split {
		runPrompt = subtask.Instruction(runPrompt)
	}
	// The isolation protocol rides the same opening turn and for the same
	// reason (ADR-0050 Decision 5): a workspace is decided when the task is
	// born. A continuing turn that wanted a different place would be a new
	// task, which is what /new is for. Unlike the split protocol there is no
	// recursion to stop — isolation does not beget isolation — so the "read
	// only the opening turn" discipline here is about meaning, not depth.
	isolate := opening && isolateProtocolEnabled() && !c.human
	var isoParent string
	if isolate {
		p, child, err := isolationDir(c.sid)
		if err != nil {
			fmt.Fprintln(os.Stderr, "workspace: 隔離先を用意できないので隔離せずに走る:", err)
			isolate = false
		} else {
			isoParent = p
			runPrompt = workspace.Instruction(runPrompt, child)
		}
	}
	collect := split || isolate

	v := c.newView(c.adapter.Name())
	sink := func(ev executor.Event, ts int64) error {
		// The thread id arrives on provider.selected (both adapters) and
		// again on provider.finished; the newest wins, so a CLI that forks a
		// new id on resume would be followed rather than lost.
		if id, ok := ev.Payload["provider_session_id"].(string); ok && id != "" {
			c.threadID = id
		}
		v.show(ev)
		if collect && ev.Type == executor.EventProviderOutput {
			if text, ok := ev.Payload["text"].(string); ok && text != "" {
				texts = append(texts, text)
			}
		}
		// The ledger gets the payload without its view-only keys (tool detail,
		// tool output — ADR-0024 Decision 6, ADR-0030): they are for the human
		// watching, and recording them would spend the perception digest budget
		// on what R3 already excludes. recordEvent also drops an event left
		// empty by the strip, so a tool_result adds no zero-information row.
		return recordEvent(c.s, c.sid, ev, ts)
	}

	ex := &executor.Executor{Adapter: c.adapter, Stderr: os.Stderr, Warn: os.Stderr}
	if os.Getenv("TOMOBIT_DEBUG") != "" {
		ex.Debug = os.Stderr
	}
	// A blank line before the answer sets it off from the line the user just
	// typed; another after it (below) sets it off from the next prompt.
	c.gap()
	v.begin()
	addDirs := c.addDirs
	if isoParent != "" {
		// The scope must name a directory that exists, so AddDirs gets the
		// parent while the prompt names the leaf (ADR-0050 Decision 4). Appended
		// to a copy: c.addDirs is the user's /add-dir declaration and outlives
		// this turn, and the isolation dir is per-session wiring, not theirs.
		addDirs = append(append([]string(nil), c.addDirs...), isoParent)
	}
	result, runErr := ex.Run(ctx, executor.Request{
		Prompt: runPrompt, ResumeID: c.threadID,
		PermissionMode: c.permMode, Timeout: c.timeout,
		// 働く場所 (ADR-0047): /cd と /add-dir がタスク境界で置いた値。
		// 空なら従来どおり tomobit 自身の cwd を継ぐ。
		WorkDir: c.workDir, AddDirs: addDirs,
	}, sink)
	v.end(result)

	if ctx.Err() != nil {
		// Ctrl-C interrupted the turn, not the task: the thread id is already
		// captured (it comes from the init line, before any work), so the next
		// turn picks the conversation back up where it stopped.
		c.sayln(dim("中断 — 続けるか /new で区切る"))
		c.gap()
		return nil
	}
	if payload, need := providerErrorPayload(runErr, result); need {
		if err := c.s.AppendEvent(c.sid, "provider.error", time.Now().UnixMilli(), payload); err != nil {
			return err
		}
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "error:", runErr)
	}
	if result.Started {
		c.completed = true
		// Where the work went, recorded before the boundary reads it: the
		// first-layer observation (ADR-0052) follows this path. Read even from
		// a failed run — a declaration reports where results are, and a run
		// that broke halfway still put them somewhere (ADR-0050 Decision 2).
		if isolate {
			recordWorkspace(c.s, c.sid, texts)
		}
	}

	// A clean opening turn's output may be a split proposal (ADR-0028 Decision
	// 5). A broken run (already returned above on ctx.Err(), or non-zero exit /
	// runErr here) is never trusted as one — its output is not a decision.
	if split && runErr == nil && result.ExitCode == 0 {
		if groups := readSplitProposal(texts); groups != nil {
			// The fold-back re-enters run and prints its own trailing gap, so the
			// split path skips the one below to avoid a double blank.
			return c.splitAndFold(ctx, groups, prompt)
		}
	}
	c.gap()
	return nil
}

// splitAndFold runs the opening turn's accepted proposal and folds the results
// back into the parent thread (ADR-0028 Decision 5). The subtasks run through
// the shared executeSplit (the same machinery do uses); then, instead of do's
// finishTask, a single deterministic feed turn resumes the parent thread so the
// next user turn talks to a Provider that already knows what the subtasks
// produced — the conversation continues over the integrated result, not over the
// split JSON it stalled on.
func (c *chat) splitAndFold(ctx context.Context, groups [][]string, parentIntent string) error {
	subs, cancelled, err := executeSplit(ctx, c.s, c.sid, groups, parentIntent,
		c.providerName, c.capability, c.size, c.permMode, c.timeout, c.in, c.out, c.interactive,
		c.newSubtaskView(), c.perception)
	if err != nil {
		return err
	}
	if cancelled {
		// SIGINT already recorded task.cancelled on the children and the parent
		// (c.sid): the task is over, so reset the session and let the next turn
		// open a fresh one rather than resume a cancelled thread — the same reset
		// closeTask does at a boundary.
		c.sid, c.threadID, c.turns = "", "", 0
		c.adapter, c.human, c.completed = nil, false, false
		c.sayln(dim("中断 — 分割を止めた。/new で次のタスクへ"))
		c.gap()
		return nil
	}

	prompt, err := c.feedPrompt(parentIntent, subs)
	if err != nil {
		return err
	}
	// The feed turn resumes the parent thread (c.threadID, unchanged by the
	// subtasks — they run in their own sessions) and records its integration
	// report as provider.output on c.sid. It goes straight through run with
	// opening=false: no task.turn (this is not the user's ask), no protocol, and
	// its output is not read for a further proposal.
	return c.run(prompt, false)
}

// feedTailChars caps each subtask's output tail carried into the fold-back
// prompt (ADR-0028 実装時ノブ). A subtask can emit a whole transcript, but the
// parent only needs the ending — its conclusion — to integrate it, so the tail
// is truncated deterministically rather than summarized by an LLM (Decision 5
// rejects a summary: it inserts a judgment and costs a run). The cap is
// per-subtask; with subtask.Max=5 the aggregate stays on the order of
// internal/perceive's maxSessionChars=12000 and deliberately below it, so the
// fold-back turn is a digest the parent thread can hold, not a dump.
const feedTailChars = 2000

// feedPrompt builds the deterministic harness that folds the subtask results
// back into the parent thread (ADR-0028 Decision 5). It lists every proposed
// subtask with its instruction and output tail; a subtask a fail-stop never
// started is named as 未着手 rather than omitted — the parent thread must not be
// left believing the whole split ran. Harness text, like subtask.Prompt and
// stepPrompt: it lives in the prompt only, never the ledger.
func (c *chat) feedPrompt(parentIntent string, subs []string) (string, error) {
	children, err := c.s.ChildSessions(c.sid)
	if err != nil {
		return "", fmt.Errorf("split fold-back: reading subtask sessions: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[tomobit] タスク「%s」を%d個のサブタスクに分割して実行した。各サブタスクの指示と結果は次の通り。\n",
		parentIntent, len(subs))
	for i, sub := range subs {
		fmt.Fprintf(&b, "\n## サブタスク %d/%d: %s\n", i+1, len(subs), sub)
		if i >= len(children) {
			b.WriteString("（未着手 — 前のサブタスクが失敗したため実行されなかった）\n")
			continue
		}
		evs, err := c.s.EventsBySession(children[i])
		if err != nil {
			return "", fmt.Errorf("split fold-back: reading subtask %d: %w", i+1, err)
		}
		text, failed := subtaskResult(evs)
		if failed {
			b.WriteString("（失敗）\n")
		}
		switch {
		case text != "":
			b.WriteString(tailRunes(text, feedTailChars))
			b.WriteString("\n")
		case !failed:
			b.WriteString("（出力なし）\n")
		}
	}
	b.WriteString("\nこれらの結果を統合して、ユーザーへの報告としてまとめよ。失敗・未着手のサブタスクがあれば、それも省かず正直に述べよ。")
	return b.String(), nil
}

// subtaskResult reads one subtask session's provider output (concatenated in
// stream order) and whether it recorded a provider.error — the objective failure
// signal a subtask carries instead of a subjective Feedback (ADR-0028 Decision 5).
func subtaskResult(evs []*store.Event) (text string, failed bool) {
	var parts []string
	for _, e := range evs {
		switch e.Type {
		case executor.EventProviderOutput:
			if t, ok := e.Payload["text"].(string); ok && t != "" {
				parts = append(parts, t)
			}
		case executor.EventProviderError:
			failed = true
		}
	}
	return strings.Join(parts, "\n"), failed
}

// tailRunes returns the last max runes of s, marking the elided head so the
// reader knows the opening was cut. The fold-back keeps each subtask's ending —
// where its conclusion lands — not its opening (ADR-0028 Decision 5: 最終出力の
// 末尾). Rune-based so a multi-byte character straddling the cut is never split.
func tailRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return "…[前略]\n" + string(r[len(r)-max:])
}

// closeTask ends the open task and runs the boundary organs. A task where
// nothing ever ran to completion is recorded as cancelled: there is nothing
// to judge, and a cancelled outcome carries no signal (ADR-0003), which is
// the honest reading of a conversation that produced no work.
func (c *chat) closeTask() error {
	if c.sid == "" {
		return nil
	}
	sid, completed := c.sid, c.completed
	c.sid, c.threadID, c.turns = "", "", 0
	c.adapter, c.human, c.completed = nil, false, false
	// ADR-0036 Decision 2b: the holder's lifetime is one task — /new (this
	// function) discards it, so the next task gets a fresh one in startTask
	// rather than reading a stale extraction against a new intent.
	c.perception = nil

	if !completed {
		if err := c.s.AppendEvent(sid, "task.cancelled", time.Now().UnixMilli(), nil); err != nil {
			return err
		}
		if c.stream != nil {
			c.stream.emit(map[string]any{"type": "task.cancelled", "sid": sid})
		}
		return nil
	}
	// The boundary organs (Feedback → 知覚 → Tomo) sit at the same gutter as the
	// conversation. They print through a mix of this writer and, in other
	// packages, plain lines; an indenting writer guttes them all at once. TTY
	// only — a pipe (view stream or plain) reads the organs raw, at column 0; in
	// view mode c.out already frames each line into a note event.
	out := io.Writer(c.out)
	if isTTY(os.Stdout) {
		out = newIndentWriter(c.out, gutter)
	}
	c.gap()
	if err := finishTask(c.s, sid, c.in, out, true, c.humanPresent, c.extractor, c.workDir); err != nil {
		return err
	}
	// task.finished closes the stream's task after the boundary organs have run
	// (ADR-0032 Decision 1: 境界の器官が済んだ後).
	if c.stream != nil {
		c.stream.emit(map[string]any{"type": "task.finished", "sid": sid})
	}
	return nil
}

func chatUsage(w io.Writer) {
	usage := `/new [prompt]     ここまでを区切って次のタスクへ(Feedback → 知覚 → Tomo)
/provider <name>  次のタスクのProvider (claude-code|codex|human|auto)
/cd <dir>         次のタスクで働く場所 (既定 いま tomobit を起動した場所)
/add-dir <dir>    その外で扱わせる場所を足す (clear で全部外す・引数なしで一覧)
/cap <name>       次のタスクのcapability (既定 implement)
/size <s|m|l>     次のタスクの判断の温度 (--provider auto のとき効く)
/status           相棒ビュー
/help             これ
/exit             終了 (Ctrl-D も同じ)

入力  ↑↓ 履歴 / Ctrl-R 履歴検索 / Tab 補完 / Ctrl-A,E 行頭・行末
      Ctrl-W 単語削除 / Ctrl-K 行末まで削除 / Ctrl-U 全消し / Ctrl-Y 戻す
      Ctrl-Z 中断 / Shift+Enter か \ + Enter で改行(貼り付けの改行はそのまま入る)
実行中の Ctrl-C はそのターンの中断 — タスクは続く
`
	if isTTY(os.Stdout) {
		usage = indent(usage)
	}
	fmt.Fprint(w, usage)
}

// The provider CLIs emit these themselves, so the chat cannot assume the
// cursor is where it left it — it turns the cursor back on unconditionally
// when a turn ends.
const (
	cursorHide = "\x1b[?25l"
	cursorShow = "\x1b[?25h"
)

// spinnerFrames animate while a provider is silent. Motion, not color: this
// still runs under NO_COLOR, because "it is alive" is not decoration.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// turnView renders one turn for a human watching it happen. The provider's
// stream and the spinner share one line of terminal, so every write goes
// through the mutex: the spinner goroutine must never paint over an
// arriving line, and vice versa.
type turnView struct {
	out  io.Writer
	name string
	tty  bool
	// styled gates markdown rendering and tool-output display the same way
	// package-level styled() does, but is captured once at construction
	// instead of re-read live: go test's stdout is never a terminal, so
	// styled() itself always takes the colourless branch and a test could
	// never reach the tool_result path it needs to pin (ADR-0031
	// Consequences). Injecting the bool makes that path reachable from a
	// bytes.Buffer.
	styled bool

	mu    sync.Mutex
	shown bool // a spinner frame is currently on the line

	started time.Time
	cost    float64
	stopCh  chan struct{}
	doneCh  chan struct{}

	// toolBudget is the turn's remaining tool_result display budget in lines
	// (ADR-0031 Decision 1): full at the start of a turn, spent as results
	// are shown, never replenished — so it resets naturally because a
	// turnView is itself built fresh per turn. elided marks that the
	// one-line omission notice already fired, so the budget's silence after
	// that stays silence rather than repeating itself.
	//
	// Neither field rides mu, deliberately: show() runs only on executor.Run's
	// single stdout-reading goroutine (the spinner goroutine touches shown
	// alone, under mu), so a lock here would claim a concurrency that does
	// not exist and invite show() to be called from more than one.
	toolBudget int
	elided     bool
}

func newTurnView(out io.Writer, name string) *turnView {
	return &turnView{
		out: out, name: name, tty: isTTY(os.Stdout),
		styled: styled(), toolBudget: turnToolResultMaxLines,
	}
}

func (v *turnView) begin() {
	v.started = time.Now()
	if !v.tty {
		return
	}
	// The cursor has nowhere useful to sit while a provider works, and a
	// block cursor parked on the spinner reads as "waiting for you to type".
	io.WriteString(v.out, cursorHide)
	v.stopCh, v.doneCh = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(v.doneCh)
		t := time.NewTicker(120 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			select {
			case <-v.stopCh:
				v.mu.Lock()
				v.clear()
				v.mu.Unlock()
				return
			case <-t.C:
				v.mu.Lock()
				v.clear()
				fmt.Fprint(v.out, dim(fmt.Sprintf("%s %s %ds",
					spinnerFrames[i%len(spinnerFrames)], v.name,
					int(time.Since(v.started).Seconds()))))
				v.shown = true
				v.mu.Unlock()
			}
		}
	}()
}

// clear erases the spinner frame. Caller holds the mutex.
func (v *turnView) clear() {
	if v.shown {
		io.WriteString(v.out, "\r\x1b[K")
		v.shown = false
	}
}

func (v *turnView) line(s string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.clear()
	// The gutter is layout, so it rides the same TTY gate as the spinner and
	// dim: a pipe reads the answer raw, at column 0.
	if v.tty {
		s = indent(s)
	}
	fmt.Fprintln(v.out, s)
}

// toolResultMaxRunes and toolResultMaxLines cap how much of one tool's output
// the view shows (ADR-0030 Decision 3), whichever bites first — a guard
// against one runaway result. It is not a guard against many small ones: a
// turn that calls a tool N times still shows every result, each capped, so
// the total climbs to N×toolResultMaxLines and can still push the turn's own
// answer off the screen. That is exactly what happened in practice (a
// teardown task with a dozen-plus small command outputs), so
// turnToolResultMaxLines (ADR-0031 Decision 1) caps the turn as a whole —
// spent as results are shown, never replenished within the turn.
//
// The values are calibrated on a real stream, not guessed (ADR-0031 Decision
// 2): an efficient three-tool-call task measured 46 visible lines across its
// results, so a 48-line turn budget — about one terminal screen — lets that
// pass almost whole while a flood of a dozen-plus calls is capped well short
// of it. The per-result caps came down with it: 40 lines alone gave one
// result half a screen, and the measured median result (19 lines, a
// `wc`-style listing) reads fine cut at 16; a colour-sample motivating case
// is nowhere near either cap.
const (
	toolResultMaxRunes = 2000
	toolResultMaxLines = 16
)

// turnToolResultMaxLines is the turn-wide budget above.
const turnToolResultMaxLines = 48

// show renders one canonical event. The user must read the assistant text to
// judge adoption; tool names are the proof that something is happening, and
// stay dim — they are not the answer.
func (v *turnView) show(ev executor.Event) {
	switch ev.Type {
	case executor.EventProviderOutput:
		if text, ok := ev.Payload["text"].(string); ok && text != "" {
			// Markdown-lite is display only (ADR-0024 Decision 5): the ledger
			// records the raw text, and a pipe gets it untouched — same gate
			// as dim, for the same reason. Text is the answer the user judges
			// for adoption, so it is exempt from the tool budget below
			// (ADR-0031 Decision 1) — never touch toolBudget here.
			if v.styled {
				text = mdlite.Render(text)
			}
			v.line(text)
			return
		}
		if res, ok := ev.Payload[executor.PayloadToolResult].(string); ok && res != "" {
			// A tool's own output — a Bash colour demo, a diff — carries its
			// own ANSI, so it skips mdlite (prose rendering would mangle it) and
			// keeps only SGR (ADR-0030). Colour is the whole point, so it shows
			// only when styled: under a pipe or NO_COLOR nothing is drawn here
			// (the tool's own tool_use event already showed its name), and the
			// ledger never held the output either way.
			if v.styled {
				switch {
				case v.toolBudget <= 0:
					// The turn's budget is spent (ADR-0031 Decision 1). Say so
					// once, honestly — not silently — then stay quiet: a
					// second result met with an empty budget is not news.
					if !v.elided {
						v.line(dim("…（以降のツール出力は省略）"))
						v.elided = true
					}
				default:
					// A result met with less than the per-result cap is cut to
					// what remains, so one result cannot spend more than the
					// turn has left — toolBudget > 0 here, so lines is always
					// at least 1 and mdlite.ToolOutput never sees the
					// "unlimited" 0.
					lines := toolResultMaxLines
					if v.toolBudget < lines {
						lines = v.toolBudget
					}
					out, truncated := mdlite.ToolOutput(res, toolResultMaxRunes, lines)
					if truncated {
						out += "\n" + dim("…（ツール出力は先頭のみ）")
					}
					// Charged after the marker joins: the marker is a display
					// line like any other, and a free marker would leak one
					// line past the budget per cut. Only the final, straddling
					// result can overdraw — by its one marker line, with the
					// budget ≤ 0 after — so the turn's tool output is bounded
					// by turnToolResultMaxLines + 2 including the elision
					// notice (the bound ADR-0031's Consequences records).
					v.toolBudget -= strings.Count(out, "\n") + 1
					v.line(out)
				}
			}
			return
		}
		if tool, ok := ev.Payload["tool"].(string); ok && tool != "" {
			s := "· " + tool
			if d, ok := ev.Payload[executor.PayloadDetail].(string); ok && d != "" {
				s += " " + d
			}
			v.line(dim(s))
		}
	case executor.EventProviderFinished:
		if c, ok := ev.Payload["cost_usd"].(float64); ok {
			v.cost = c
		}
	case executor.EventProviderError:
		if msg, ok := ev.Payload["message"].(string); ok && msg != "" {
			v.line("! " + msg)
		}
	}
}

func (v *turnView) end(result executor.Result) {
	if v.stopCh != nil {
		close(v.stopCh)
		<-v.doneCh
		io.WriteString(v.out, cursorShow)
	}
	if !v.tty || !result.Started {
		return
	}
	footer := fmt.Sprintf("%.1fs", result.Duration.Seconds())
	if v.cost > 0 {
		footer += fmt.Sprintf(" · $%.4f", v.cost)
	}
	v.line(dim(footer))
}

// dim marks a line as the machine's own bookkeeping rather than Tomo's voice
// or the provider's answer.
//
// Colour goes only to a terminal, the same rule the avatar follows (ADR-0008
// Decision 4): piped output is read by a script or a test, and escape codes
// in it are corruption, not decoration. NO_COLOR is "present", not "non-empty"
// (https://no-color.org/), and both are read per call — a test's stdout is not
// a terminal, and neither is the pipe the chat's own scripted path runs in.
func dim(s string) string {
	if !styled() {
		return s
	}
	return "\x1b[2m" + s + "\x1b[0m"
}

func styled() bool {
	_, noColor := os.LookupEnv("NO_COLOR")
	return !noColor && isTTY(os.Stdout)
}

// gap prints a blank line between the parts of a turn — the user's line and
// the answer, the answer and the next prompt — but only when a human is
// watching. A pipe gets the turns back to back, raw, the same rule the gutter
// follows: layout is for the terminal, not the script reading it.
func (c *chat) gap() {
	if isTTY(os.Stdout) {
		fmt.Fprintln(c.out)
	}
}

// sayln prints one of the chat's own notes at the gutter (TTY only), so its
// asides sit at the same left margin as the conversation rather than flush
// against the terminal edge. Off a terminal the line passes through raw.
func (c *chat) sayln(s string) {
	if isTTY(os.Stdout) {
		s = indent(s)
	}
	fmt.Fprintln(c.out, s)
}

// viewVersion is the NDJSON view stream's contract version (ADR-0032 Decision
// 1): a consumer ignores unknown types, so adding one is compatible; changing
// or dropping a meaning bumps this.
const viewVersion = 1

// validateViewFlag checks --view and its target (ADR-0032 Decision 1). "" is
// the plain-text default; "ndjson" the machine stream, which refuses a TTY —
// view mode is built on all the terminal gates (gutter, gap, markdown-lite)
// being shut, and a terminal would put every one back in question (`| jq` is
// the debugging path). Pure, so the rejection pins without a real terminal.
func validateViewFlag(view string, stdoutTTY bool) error {
	switch view {
	case "":
		return nil
	case "ndjson":
		if stdoutTTY {
			return fmt.Errorf("--view ndjson は端末には出せない — パイプして使う（例: | jq）")
		}
		return nil
	default:
		return fmt.Errorf("unknown --view %q (ndjson)", view)
	}
}

// view renders one turn for a reader. turnView draws it for a human at a
// terminal; ndjsonView emits it as machine events for a GUI (ADR-0032 Decision
// 1). run() drives whichever the session opened with through the same three
// calls, so the turn loop stays unaware which reader is downstream.
type view interface {
	begin()
	show(ev executor.Event)
	end(result executor.Result)
}

// newView builds the turn's view for the session's mode: the NDJSON stream when
// one is wired, the terminal turnView otherwise. The turn number rides along so
// a fold-back feed turn (run with opening=false) repeats its parent's n, which
// c.turns already holds unchanged (ADR-0032 Decision 1).
func (c *chat) newView(name string) view {
	if c.stream != nil {
		return &ndjsonView{s: c.stream, name: name, n: c.turns}
	}
	return newTurnView(c.out, name)
}

// newSubtaskView is the per-subtask view factory executeSplit takes (ADR-0032
// Decision 1 × ADR-0028): under the NDJSON stream, a split's subtasks must
// reach the GUI in the same typed vocabulary an ordinary turn does, not
// providerSink's raw echo — a subtask nests under the opening turn no less
// than the fold-back feed turn does, so it repeats the same n (c.turns, which
// splitAndFold only ever reaches from the opening turn). Off the stream (do
// has no view at all; a plain/TTY chat keeps its existing raw echo for split,
// unchanged by this ADR) it returns nil, and executeSplit falls back to
// providerSink exactly as it always has.
func (c *chat) newSubtaskView() func(string) view {
	if c.stream == nil {
		return nil
	}
	n := c.turns
	return func(name string) view {
		return &ndjsonView{s: c.stream, name: name, n: n}
	}
}

// ndjsonStream is the single writer of the NDJSON view stream (ADR-0032
// Decision 1). Every line of stdout — the typed view events and the plain-text
// notes the organs still print — goes through emit, so the two never interleave
// mid-line. It carries no lock, deliberately: view mode forces a non-TTY stdout
// (validated at startup), which forces a non-interactive session, which keeps
// split sequential — so turnView's spinner and split's parallel goroutines, the
// only second writers this file has, never run here. show() runs on Run's one
// synchronous stdout goroutine; the note writer and flush hook run on the main
// goroutine between turns, and Run never reads our stdin, so the two never
// overlap. The reasoning turnView records for its unlocked toolBudget, again.
type ndjsonStream struct {
	enc     *json.Encoder
	pending []byte // a note line written without its terminating newline yet
}

func newNDJSONStream(w io.Writer) *ndjsonStream {
	enc := json.NewEncoder(w)
	// The view carries raw markdown and code, not HTML — keep <, > and & literal
	// so a consumer's string match sees the text the provider actually wrote.
	enc.SetEscapeHTML(false)
	return &ndjsonStream{enc: enc}
}

// emit writes one view event as a line of JSON. A write error (a closed pipe —
// the GUI went away) is dropped, as turnView drops its own writes: the stream
// is a view, and there is nothing left to tell once its reader is gone.
func (n *ndjsonStream) emit(ev map[string]any) { _ = n.enc.Encode(ev) }

// flushAwait releases a buffered partial note when a read reaches stdin's
// bottom (ADR-0032 Decision 1: flush-on-read), tagging it await so an
// interactive consumer knows this line — the Feedback question — is the one
// blocking for input. await is a best-effort signal, not a guarantee: when
// stdin is written in one batch (a script that flushes the whole conversation
// up front), the shared bufio's read-ahead already holds the answer, so the
// read never reaches bottom here — the note then arrives unmarked, carried out
// later by the next full line or flushClose. That is the contract's shape, not
// a breach: whoever batch-wrote the input is not waiting on the signal. A GUI
// that feeds one line at a time always reaches bottom and always gets await.
func (n *ndjsonStream) flushAwait() {
	if len(n.pending) == 0 {
		return
	}
	n.emit(map[string]any{"type": "note", "text": string(n.pending), "await": true})
	n.pending = n.pending[:0]
}

// flushClose emits any leftover partial note as the chat ends, so a note
// written without a trailing newline is not lost when stdout closes.
func (n *ndjsonStream) flushClose() {
	if len(n.pending) == 0 {
		return
	}
	n.emit(map[string]any{"type": "note", "text": string(n.pending)})
	n.pending = n.pending[:0]
}

// noteWriter frames the chat's plain-text stdout writes — organ speech, sayln,
// showStatus, chatUsage — into note events on the shared stream, so stdout stays
// entirely NDJSON in view mode (ADR-0032 Decision 1). Writes split on '\n': each
// complete line becomes a note; a trailing partial line waits in the stream's
// pending buffer until a newline completes it, the chat ends (flushClose), or a
// read blocks (flushAwait) — the flush-on-read the Feedback question rides.
//
// An empty line emits no note: speaker-separation blanks are the terminal's
// typography, the same gutter/gap discipline that never crosses the pipe
// (ADR-0032 Decision 1: layout is for the terminal), so the view is spared a
// stream of contentless notes.
type noteWriter struct{ s *ndjsonStream }

func (w noteWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' {
			if len(w.s.pending) > 0 {
				w.s.emit(map[string]any{"type": "note", "text": string(w.s.pending)})
				w.s.pending = w.s.pending[:0]
			}
		} else {
			w.s.pending = append(w.s.pending, b)
		}
	}
	return len(p), nil
}

// viewDecided implements decidedViewer (ADR-0040 Decision 1): the same
// decide.Decision autoDecide just wrote to tomo.decided, as a typed event
// rather than framed prose — a GUI reads candidates as data, not a line to
// parse. Candidate keys match tomo.decided's own ("scope", not "scope_key")
// so the ledger and the view never give the same audit two names. Seed rides
// as a string, matching tomo.decided's own encoding: a UnixNano exceeds
// JSON's exact float64 integer range.
//
// Invariant: decided can reach the stream before this task's own
// task.started — autoDecide runs, and records tomo.decided, before openTask/
// openSubtask emit anything else — so a reader must correlate by sid, never
// by "the most recently opened task".
func (w noteWriter) viewDecided(sid string, dec decide.Decision) {
	cands := make([]map[string]any, len(dec.Candidates))
	for i, c := range dec.Candidates {
		cands[i] = map[string]any{
			"provider": c.Provider, "scope": c.ScopeKey,
			"quantile": c.Quantile, "passed": c.Passed, "wins": c.Wins,
		}
	}
	w.s.emit(map[string]any{
		"type": "decided", "sid": sid, "provider": dec.Provider,
		"n": dec.N, "q": dec.Q, "fallback": dec.Fallback,
		"seed": strconv.FormatInt(dec.Seed, 10), "candidates": cands,
	})
}

// flushReader calls flush just before each read that reaches the underlying
// reader — os.Stdin, beneath the shared bufio (ADR-0032 Decision 1). bufio pulls
// from here only when its buffer is drained, so this is the one place the "a
// read reached bottom" moment is observable: above the bufio a buffered read
// returns without ever touching this. It is the block-detection point, not a
// guarantee a block happened — a batch-written stdin can satisfy the read from
// read-ahead and never reach here (see flushAwait).
type flushReader struct {
	r     io.Reader
	flush func()
}

func (f flushReader) Read(p []byte) (int, error) {
	f.flush()
	return f.r.Read(p)
}

// ndjsonView renders one turn as NDJSON view events (ADR-0032 Decision 1), the
// machine counterpart to turnView. It shares the stream with the note writer so
// its typed events never interleave mid-line with the organs' notes. It has no
// spinner, gutter, mdlite or tool budget — those are terminal physics
// (ADR-0030/0031); the NDJSON consumer owns its own presentation, so tool_result
// flows through raw and uncapped, the budget staying on turnView's side.
type ndjsonView struct {
	s       *ndjsonStream
	name    string
	n       int
	started time.Time
	cost    float64
}

func (v *ndjsonView) begin() {
	v.started = time.Now()
	v.s.emit(map[string]any{"type": "turn.started", "n": v.n, "provider": v.name})
}

func (v *ndjsonView) show(ev executor.Event) {
	switch ev.Type {
	case executor.EventProviderSelected:
		// The auto answer-check: which provider — and model, when reported — the
		// turn actually ran on.
		out := map[string]any{"type": "provider", "name": v.name}
		if p, ok := ev.Payload["provider"].(string); ok && p != "" {
			out["name"] = p
		}
		if m, ok := ev.Payload["model"].(string); ok && m != "" {
			out["model"] = m
		}
		v.s.emit(out)
	case executor.EventProviderOutput:
		if t, ok := ev.Payload["text"].(string); ok && t != "" {
			v.s.emit(map[string]any{"type": "text", "text": t})
			return
		}
		if r, ok := ev.Payload[executor.PayloadToolResult].(string); ok && r != "" {
			v.s.emit(map[string]any{"type": "tool_result", "text": r})
			return
		}
		if tool, ok := ev.Payload["tool"].(string); ok && tool != "" {
			out := map[string]any{"type": "tool", "name": tool}
			if d, ok := ev.Payload[executor.PayloadDetail].(string); ok && d != "" {
				out["detail"] = d
			}
			v.s.emit(out)
		}
	case executor.EventProviderFinished:
		if c, ok := ev.Payload["cost_usd"].(float64); ok {
			v.cost = c
		}
	case executor.EventProviderError:
		if msg, ok := ev.Payload["message"].(string); ok && msg != "" {
			v.s.emit(map[string]any{"type": "error", "message": msg})
		}
	}
}

func (v *ndjsonView) end(result executor.Result) {
	out := map[string]any{
		"type":        "turn.finished",
		"n":           v.n,
		"started":     result.Started,
		"duration_ms": result.Duration.Milliseconds(),
	}
	if v.cost > 0 {
		out["cost_usd"] = v.cost
	}
	v.s.emit(out)
}
