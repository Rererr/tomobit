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
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/lineedit"
	"github.com/Rererr/tomobit/internal/mdlite"
	"github.com/Rererr/tomobit/internal/perceive"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/subtask"
	"github.com/Rererr/tomobit/internal/voice"
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
// turn reads as a block distinct from Tomo's answer. A low-saturation blue in
// truecolor (48;2;R;G;B), tuned for a dark terminal; drop B for fainter, raise
// it for more blue. A terminal without truecolor maps it to its nearest
// palette entry.
const inputBg = "\x1b[48;2;30;44;74m"

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

	providerName string
	capability   string
	permMode     string
	timeout      time.Duration
	size         string
	extractor    perceive.Extractor
	// interactive is whether a human is watching (both stdin and stdout are a
	// terminal). It gates the split parallelism offer (splitAndFold): a pipe or CI
	// never sees it and stays sequential. A field, not a live isTTY() call, so a
	// test can drive the accept path deterministically without a real terminal.
	interactive bool

	// sid == "" means no task is open: the next prompt starts one.
	sid       string
	adapter   executor.Adapter
	human     bool
	threadID  string
	turns     int
	completed bool // a turn ran to completion — there is something to judge
}

func cmdChat(args []string) error {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	db := dbFlag(fs)
	capability := fs.String("cap", "implement", "capability of the task")
	timeout := fs.Duration("timeout", 0, "max run time per turn, 0 = no limit")
	permMode := fs.String("permission-mode", "", "permission mode passthrough (claude --permission-mode / codex --sandbox)")
	providerName := fs.String("provider", "claude-code", "adapter to run: claude-code|codex|human|auto")
	size := fs.String("size", "", "task size for decision stakes: small|medium|large (--provider auto)")
	backend := fs.String("backend", "", "perception backend for best-effort perception: ollama|mlx-lm (default: resolved from config)")
	model := fs.String("model", "", "perception model for best-effort perception (default depends on --backend)")
	url := fs.String("url", "", "perception backend url for best-effort perception (default depends on --backend)")
	fs.Parse(args)

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
		s: s, ed: ed, in: ed.Reader(), out: os.Stdout,
		providerName: *providerName, capability: *capability,
		permMode: *permMode, timeout: *timeout, size: *size,
		extractor:   extractor,
		interactive: isTTY(os.Stdin) && isTTY(os.Stdout),
	}
	ed.Completer = c.complete
	// The first screen is the companion view (ADR-0008), and its next line is
	// the prompt (ADR-0022 Decision 4): you meet Tomo and you can talk.
	if isTTY(os.Stdout) {
		if err := showStatus(s); err != nil {
			return err
		}
		c.sayln(dim("話しかけて。/help でコマンド、Ctrl-D で終了"))
		fmt.Fprintln(c.out)
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
			var err error
			line, err = c.ed.ReadLine(chatPrompt)
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
	case "/cap":
		c.setWiring(&c.capability, arg, "cap", nil)
	case "/size":
		// Same nature as /cap: a per-task value the ledger reads once. It
		// only bites with --provider auto, where it is the stakes of the
		// decision (ADR-0012's n).
		c.setWiring(&c.size, arg, "size", nil)
	case "/status":
		return false, showStatus(c.s)
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
var completableCommands = []string{"/new", "/provider", "/cap", "/size", "/status", "/help", "/exit"}

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
	sid, adapter, human, err := openTask(c.s, c.providerName, c.capability, c.size, prompt)
	if err != nil {
		return err
	}
	c.sid, c.adapter, c.human = sid, adapter, human
	c.turns, c.completed, c.threadID = 1, false, ""

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
		runPrompt = subtask.Instruction(prompt)
	}

	v := newTurnView(c.out, c.adapter.Name())
	sink := func(ev executor.Event, ts int64) error {
		// The thread id arrives on provider.selected (both adapters) and
		// again on provider.finished; the newest wins, so a CLI that forks a
		// new id on resume would be followed rather than lost.
		if id, ok := ev.Payload["provider_session_id"].(string); ok && id != "" {
			c.threadID = id
		}
		v.show(ev)
		if split && ev.Type == executor.EventProviderOutput {
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
	result, runErr := ex.Run(ctx, executor.Request{
		Prompt: runPrompt, ResumeID: c.threadID,
		PermissionMode: c.permMode, Timeout: c.timeout,
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
	}

	// A clean opening turn's output may be a split proposal (ADR-0028 Decision
	// 5). A broken run (already returned above on ctx.Err(), or non-zero exit /
	// runErr here) is never trusted as one — its output is not a decision.
	if split && runErr == nil && result.ExitCode == 0 {
		groups, parseErr := subtask.Parse(strings.Join(texts, "\n"))
		if parseErr != nil {
			fmt.Fprintln(os.Stderr, "split: proposal ignored —", parseErr)
		} else if groups != nil {
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
		c.providerName, c.capability, c.size, c.permMode, c.timeout, c.in, c.out, c.interactive)
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

	if !completed {
		return c.s.AppendEvent(sid, "task.cancelled", time.Now().UnixMilli(), nil)
	}
	// The boundary organs (Feedback → 知覚 → Tomo) sit at the same gutter as the
	// conversation. They print through a mix of this writer and, in other
	// packages, plain lines; an indenting writer guttes them all at once. TTY
	// only — a pipe reads the organs raw, at column 0.
	out := io.Writer(c.out)
	if isTTY(os.Stdout) {
		out = newIndentWriter(c.out, gutter)
	}
	c.gap()
	return finishTask(c.s, sid, c.in, out, true, c.extractor)
}

func chatUsage(w io.Writer) {
	usage := `/new [prompt]     ここまでを区切って次のタスクへ(Feedback → 知覚 → Tomo)
/provider <name>  次のタスクのProvider (claude-code|codex|human|auto)
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

	mu    sync.Mutex
	shown bool // a spinner frame is currently on the line

	started time.Time
	cost    float64
	stopCh  chan struct{}
	doneCh  chan struct{}
}

func newTurnView(out io.Writer, name string) *turnView {
	return &turnView{out: out, name: name, tty: isTTY(os.Stdout)}
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
// the view shows (ADR-0030 Decision 3), whichever bites first. A short colour
// demo is near neither, so the caps only fire on a runaway, keeping one result
// from pushing the turn's answer off the screen. The rune cap bounds a single
// enormous line (the executor once drained 5MB of child stdout); the line cap
// bounds height for short-line output — a diff, a test log — where the rune cap
// would let thousands of rows through. Per-result: a turn with many tool calls
// still shows each, capped. Both are implementation knobs, tuned on real output.
const (
	toolResultMaxRunes = 4000
	toolResultMaxLines = 40
)

// show renders one canonical event. The user must read the assistant text to
// judge adoption; tool names are the proof that something is happening, and
// stay dim — they are not the answer.
func (v *turnView) show(ev executor.Event) {
	switch ev.Type {
	case executor.EventProviderOutput:
		if text, ok := ev.Payload["text"].(string); ok && text != "" {
			// Markdown-lite is display only (ADR-0024 Decision 5): the ledger
			// records the raw text, and a pipe gets it untouched — same gate
			// as dim, for the same reason.
			if styled() {
				text = mdlite.Render(text)
			}
			v.line(text)
			return
		}
		if res, ok := ev.Payload[executor.PayloadToolResult].(string); ok && res != "" {
			// A tool's own output — a Bash colour demo, a diff — carries its
			// own ANSI, so it skips mdlite (prose rendering would mangle it) and
			// keeps only SGR (ADR-0030). Colour is the whole point, so it shows
			// only when styled(): under a pipe or NO_COLOR nothing is drawn here
			// (the tool's own tool_use event already showed its name), and the
			// ledger never held the output either way.
			if styled() {
				out, truncated := mdlite.ToolOutput(res, toolResultMaxRunes, toolResultMaxLines)
				if truncated {
					out += "\n" + dim("…（ツール出力は先頭のみ）")
				}
				v.line(out)
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
