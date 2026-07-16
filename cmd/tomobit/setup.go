// tomobit setup — the interactive onboarding (ADR-0021). It asks what this
// machine looks like, writes the wiring to ~/.tomobit/config.json (outside
// the experience DB — wiring is not experience), and ends on the companion
// view: the first screen after setup is Tomo, not a manual. Idempotent —
// rerunning it is the diagnosis.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Rererr/tomobit/internal/config"
)

func cmdSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	fs.Parse(args)

	in := bufio.NewReader(os.Stdin)
	out := os.Stdout

	path, err := config.Path()
	if err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	fmt.Fprintf(out, "tomobit setup — この機械の配線を決める(%s)\n\n", path)

	// Start from the current config so rerunning edits instead of resetting;
	// a broken file starts blank (run() already warned about it).
	c := cfg
	if cfgErr != nil {
		c = config.Config{}
	}

	if err := askProfile(in, out, &c); err != nil {
		return err
	}
	if err := askClaudeArgs(in, out, &c); err != nil {
		return err
	}
	reportCLIs(out)
	if err := askOllama(in, out, &c); err != nil {
		return err
	}

	if err := config.Save(c); err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	cfg, cfgErr = c, nil
	wireClaude()
	fmt.Fprintf(out, "\nwrote %s\n\n", path)

	// The first screen is the companion view (ADR-0008): on a brand-new DB
	// the empty network itself speaks —「はじめまして」is a View, not an event.
	return cmdStatus(nil)
}

// askClaudeProfile is the in-run variant (ADR-0021): `do` found no profile
// choice on a terminal, so the missing choice becomes the question. Saves
// only that answer, then rewires the current process and continues the run.
func askClaudeProfile(in *bufio.Reader, out io.Writer) error {
	c := cfg
	if cfgErr != nil {
		c = config.Config{}
	}
	if err := askProfile(in, out, &c); err != nil {
		return err
	}
	if err := config.Save(c); err != nil {
		return err
	}
	cfg, cfgErr = c, nil
	wireClaude()
	return nil
}

// askProfile asks which CLAUDE_CONFIG_DIR profile claude-code runs under.
// Candidates are every ~/.claude* directory on this machine; 0 is the
// explicit "inherit the parent environment" (stored as "", still a choice).
func askProfile(in *bufio.Reader, out io.Writer, c *config.Config) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cands := claudeProfileCandidates(home)

	fmt.Fprintln(out, "claude-code をどのプロファイルで走らせる? (子プロセスの CLAUDE_CONFIG_DIR)")
	for i, p := range cands {
		fmt.Fprintf(out, "  %d) %s\n", i+1, p)
	}
	fmt.Fprintln(out, "  0) 親環境をそのまま継承する(明示)")
	fmt.Fprintln(out, "  またはパスを直接入力")
	cur := "未設定"
	if c.ClaudeConfigDir != nil {
		if cur = *c.ClaudeConfigDir; cur == "" {
			cur = "継承"
		}
	}
	fmt.Fprintf(out, "[現在: %s / Enterで維持] > ", cur)

	line, err := readLine(in)
	if err != nil {
		return err
	}
	switch {
	case line == "":
		if c.ClaudeConfigDir == nil {
			return fmt.Errorf("setup: プロファイルは明示が必要(継承したい場合も 0 を選ぶ)")
		}
	case line == "0":
		empty := ""
		c.ClaudeConfigDir = &empty
	default:
		if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(cands) {
			c.ClaudeConfigDir = &cands[n-1]
		} else {
			c.ClaudeConfigDir = &line
		}
	}
	return nil
}

func askClaudeArgs(in *bufio.Reader, out io.Writer, c *config.Config) error {
	fmt.Fprintln(out, "\nclaude-code の毎回付けるフラグ(空白区切り、none でなし)")
	fmt.Fprintln(out, "  例: --exclude-dynamic-system-prompt-sections")
	fmt.Fprintf(out, "[現在: %s / Enterで維持] > ", displayArgs(c.ClaudeArgs))
	line, err := readLine(in)
	if err != nil {
		return err
	}
	switch line {
	case "":
	case "none":
		c.ClaudeArgs = nil
	default:
		c.ClaudeArgs = strings.Fields(line)
	}
	return nil
}

// reportCLIs is diagnosis only: which provider CLIs this machine has. No
// config to write — presence is a fact of the machine, not a choice.
func reportCLIs(out io.Writer) {
	fmt.Fprintln(out)
	for _, cli := range []string{"claude", "codex", "ollama"} {
		if p, err := exec.LookPath(cli); err == nil {
			fmt.Fprintf(out, "  %-6s ✓ %s\n", cli, p)
		} else {
			fmt.Fprintf(out, "  %-6s ✗ 見つからない(PATH)\n", cli)
		}
	}
}

func askOllama(in *bufio.Reader, out io.Writer, c *config.Config) error {
	urlDef := c.OllamaURL
	if urlDef == "" {
		urlDef = "http://localhost:11434"
	}
	fmt.Fprintf(out, "\nOllama の URL [%s / Enterで維持] > ", urlDef)
	line, err := readLine(in)
	if err != nil {
		return err
	}
	if line != "" {
		c.OllamaURL = strings.TrimRight(line, "/")
	}

	modelDef := c.OllamaModel
	if modelDef == "" {
		modelDef = "qwen3:8b"
	}
	fmt.Fprintf(out, "知覚モデル [%s / Enterで維持] > ", modelDef)
	if line, err = readLine(in); err != nil {
		return err
	}
	if line != "" {
		c.OllamaModel = line
	}

	url, model := c.OllamaURL, c.OllamaModel
	if url == "" {
		url = "http://localhost:11434"
	}
	if model == "" {
		model = "qwen3:8b"
	}
	switch has, err := ollamaHasModel(url, model); {
	case err != nil:
		fmt.Fprintf(out, "  ollama ✗ 届かない(%v) — 知覚はDeferredなので後でよい\n", err)
	case has:
		fmt.Fprintf(out, "  ollama ✓ %s が居る\n", model)
	default:
		fmt.Fprintf(out, "  ollama ✗ %s が居ない → `ollama pull %s`\n", model, model)
	}
	return nil
}

// claudeProfileCandidates lists every ~/.claude* directory — the plain
// ~/.claude counts too: that IS the default profile.
func claudeProfileCandidates(home string) []string {
	ents, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && strings.HasPrefix(e.Name(), ".claude") {
			out = append(out, home+"/"+e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// ollamaHasModel asks /api/tags whether the model is pulled. A short timeout:
// setup must never hang on a daemon that isn't there.
func ollamaHasModel(url, model string) (bool, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url + "/api/tags")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return false, err
	}
	for _, m := range tags.Models {
		if m.Name == model || strings.TrimSuffix(m.Name, ":latest") == model {
			return true, nil
		}
	}
	return false, nil
}

func readLine(in *bufio.Reader) (string, error) {
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("setup: 入力が閉じられた")
	}
	return strings.TrimSpace(line), nil
}

func displayArgs(args []string) string {
	if len(args) == 0 {
		return "なし"
	}
	return strings.Join(args, " ")
}
