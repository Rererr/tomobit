// tomobit chat — the conversational session (ADR-0022).
//
// One chat = one task = one Experience; turns are the breathing inside it.
// The organs `do` runs at its boundary (採用確認 → 知覚 → 質問 → 鏡) run here
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
	"github.com/Rererr/tomobit/internal/perceive"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/voice"
)

const chatPrompt = "❯ "

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
	model := fs.String("model", ollamaModelDefault(), "ollama model for best-effort perception")
	url := fs.String("url", cfg.OllamaURL, "ollama base url (default http://localhost:11434)")
	fs.Parse(args)

	// Both fail before the store is even opened, like `do`: a chat that
	// cannot launch anything is not a chat, and finding out one prompt later
	// would already have cost the user their first typed task.
	if *providerName != "auto" && *providerName != "human" {
		if _, err := resolveProvider(*providerName); err != nil {
			return err
		}
	}
	ed := lineedit.New(os.Stdin, os.Stdout)
	if err := ensureClaudeProfile(ed.Reader(), *providerName); err != nil {
		return err
	}
	s, err := openStore(*db)
	if err != nil {
		return err
	}
	defer s.Close()

	c := &chat{
		s: s, ed: ed, in: ed.Reader(), out: os.Stdout,
		providerName: *providerName, capability: *capability,
		permMode: *permMode, timeout: *timeout, size: *size,
		extractor: &perceive.Ollama{URL: *url, Model: *model},
	}
	// The first screen is the companion view (ADR-0008), and its next line is
	// the prompt (ADR-0022 Decision 4): you meet Tomo and you can talk.
	if isTTY(os.Stdout) {
		if err := showStatus(s); err != nil {
			return err
		}
		fmt.Fprintln(c.out, dim("話しかけて。/help でコマンド、Ctrl-D で終了"))
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
				fmt.Fprintln(c.out, dim("もう一度 Ctrl-C で終了(/exit)"))
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
			fmt.Fprintln(c.out, dim("次のタスクへ"))
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
		fmt.Fprintf(c.out, "%s — 知らないコマンド。/help\n", name)
	}
	return false, nil
}

// setWiring changes one of the per-task choices. Mid-task it refuses: an
// experience carries one provider and one capability for the whole session
// (SCHEMA.md), so swapping either halfway would record a task that never
// happened. The ledger's shape is what says no here, not the implementation.
func (c *chat) setWiring(field *string, arg, label string, check func(string) error) {
	if arg == "" {
		fmt.Fprintln(c.out, dim(label+": "+*field))
		return
	}
	if c.sid != "" {
		fmt.Fprintf(c.out, "タスクの途中では %s を替えられない — /new で区切ってから\n", label)
		return
	}
	if check != nil {
		if err := check(arg); err != nil {
			fmt.Fprintln(c.out, err)
			return
		}
	}
	*field = arg
	fmt.Fprintln(c.out, dim(label+": "+arg))
}

// turn is one exchange: the first opens the task, the rest resume its thread.
func (c *chat) turn(prompt string) error {
	if c.human {
		fmt.Fprintln(c.out, dim("いまのタスクは君の手にある — 終わったら /new か /exit で区切る"))
		return nil
	}
	if c.sid == "" {
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
	return c.run(prompt)
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
		fmt.Fprintf(c.out, "\n「%s」\n", voice.RouteHuman())
		fmt.Fprintln(c.out, dim("終わったら /new か /exit で区切る"))
		c.completed = true
	}
	return nil
}

// run executes one turn against the provider, resuming the thread the
// earlier turns opened.
func (c *chat) run(prompt string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	v := newTurnView(c.out, c.adapter.Name())
	sink := func(ev executor.Event, ts int64) error {
		// The thread id arrives on provider.selected (both adapters) and
		// again on provider.finished; the newest wins, so a CLI that forks a
		// new id on resume would be followed rather than lost.
		if id, ok := ev.Payload["provider_session_id"].(string); ok && id != "" {
			c.threadID = id
		}
		v.show(ev)
		return c.s.AppendEvent(c.sid, ev.Type, ts, ev.Payload)
	}

	ex := &executor.Executor{Adapter: c.adapter, Stderr: os.Stderr, Warn: os.Stderr}
	if os.Getenv("TOMOBIT_DEBUG") != "" {
		ex.Debug = os.Stderr
	}
	v.begin()
	result, runErr := ex.Run(ctx, executor.Request{
		Prompt: prompt, ResumeID: c.threadID,
		PermissionMode: c.permMode, Timeout: c.timeout,
	}, sink)
	v.end(result)

	if ctx.Err() != nil {
		// Ctrl-C interrupted the turn, not the task: the thread id is already
		// captured (it comes from the init line, before any work), so the next
		// turn picks the conversation back up where it stopped.
		fmt.Fprintln(c.out, dim("中断 — 続けるか /new で区切る"))
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
	return nil
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
	fmt.Fprintln(c.out)
	return finishTask(c.s, sid, c.in, c.out, true, c.extractor)
}

func chatUsage(w io.Writer) {
	fmt.Fprint(w, `/new [prompt]     ここまでを区切って次のタスクへ(採用確認 → 知覚 → Tomo)
/provider <name>  次のタスクのProvider (claude-code|codex|human|auto)
/cap <name>       次のタスクのcapability (既定 implement)
/size <s|m|l>     次のタスクの判断の温度 (--provider auto のとき効く)
/status           相棒ビュー
/help             これ
/exit             終了 (Ctrl-D も同じ)

入力  ↑↓ 履歴 / Ctrl-A,E 行頭・行末 / Ctrl-W 単語削除 / Ctrl-U 全消し
      \ + Enter で改行(貼り付けの改行はそのまま入る)
実行中の Ctrl-C はそのターンの中断 — タスクは続く
`)
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
	fmt.Fprintln(v.out, s)
}

// show renders one canonical event. The user must read the assistant text to
// judge adoption; tool names are the proof that something is happening, and
// stay dim — they are not the answer.
func (v *turnView) show(ev executor.Event) {
	switch ev.Type {
	case executor.EventProviderOutput:
		if text, ok := ev.Payload["text"].(string); ok && text != "" {
			v.line(text)
			return
		}
		if tool, ok := ev.Payload["tool"].(string); ok && tool != "" {
			v.line(dim("  · " + tool))
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
	footer := fmt.Sprintf("  %.1fs", result.Duration.Seconds())
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
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor || !isTTY(os.Stdout) {
		return s
	}
	return "\x1b[2m" + s + "\x1b[0m"
}
