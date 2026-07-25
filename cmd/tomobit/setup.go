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
	"runtime"
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
	if err := askPerceiveBackend(in, out, &c, resolvePerceiveBackend(cfg)); err != nil {
		return err
	}
	if err := askFaceAutoLaunch(in, out, &c); err != nil {
		return err
	}
	if err := askFaceResident(in, out, &c); err != nil {
		return err
	}
	if err := askQuotaObserve(in, out, &c); err != nil {
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
	// A partial save must still uphold "every config this binary writes
	// carries perceive_backend" (ADR-0029 Decision 3) — resolved from the
	// on-disk config as it stood before this save, so the save's own
	// behavior is pinned unchanged: an already-wired machine still resolves
	// to ollama, a virgin Mac still resolves to mlx-lm.
	if c.PerceiveBackend == "" {
		c.PerceiveBackend = resolvePerceiveBackend(cfg)
	}
	if err := config.Save(c); err != nil {
		return err
	}
	cfg, cfgErr = c, nil
	wireClaude()
	return nil
}

// resolvePerceiveBackend resolves the backend from onDisk — the config as
// loaded at process start (package var cfg), never a question's in-progress
// working copy: ADR-0029 Decision 3's legacy-detection branch makes an empty
// perceive_backend's meaning depend on the WHOLE config, so resolving
// against a copy an earlier question in this same run already touched (e.g.
// claude profile) would make a virgin machine's config look already-wired
// and misresolve it to ollama. Taking the snapshot as a parameter (instead
// of reading the package var here) keeps the resolution testable without
// global state and safe if a daemonized future (ADR-0004) ever calls it off
// the main goroutine.
func resolvePerceiveBackend(onDisk config.Config) string {
	resolved, err := onDisk.ResolveBackend(runtime.GOOS)
	if err != nil {
		// An on-disk perceive_backend that is itself invalid still needs a
		// sane fallback: blank only the broken key and re-resolve, so every
		// legacy-detection branch (the ollama_* keys AND the any-other-field
		// check) still sees the machine's real wiring. Rebuilding a Config
		// from just the ollama_* fields here would misread a defaults-wired
		// machine as virgin — the exact regression Decision 3's revision was
		// measured to prevent.
		onDisk.PerceiveBackend = ""
		resolved, _ = onDisk.ResolveBackend(runtime.GOOS)
	}
	return resolved
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
	for _, cli := range []string{"claude", "codex", "ollama", "mlx_lm.server"} {
		if p, err := exec.LookPath(cli); err == nil {
			fmt.Fprintf(out, "  %-14s ✓ %s\n", cli, p)
		} else {
			fmt.Fprintf(out, "  %-14s ✗ 見つからない(PATH)\n", cli)
		}
	}
}

// askPerceiveBackend asks which local perception server this machine uses
// (ADR-0029 Decision 5): a backend choice first, then that backend's URL and
// model, then a backend-specific diagnosis. resolved is what ResolveBackend
// derives from the ON-DISK config (resolvePerceiveBackend), not from
// c — c may already carry an earlier question's edits from this same run
// (e.g. the claude profile), and resolving against that could make a virgin
// machine's config look already-wired, misresolving it to ollama.
//
// Enter WRITES perceive_backend explicitly here, unlike this setup's other
// "Enter = leave unwritten" questions (askFaceAutoLaunch etc.): ADR-0029
// Decision 3's legacy-detection branch makes an absent perceive_backend's
// meaning depend on the rest of the config, so leaving it unwritten would
// make a config this very run just saved indistinguishable from one that
// predates the key. An unrecognized answer pins the same displayed default
// for the same reason, instead of the usual "print a hint and move on".
func askPerceiveBackend(in *bufio.Reader, out io.Writer, c *config.Config, resolved string) error {
	// Anything but the two valid names falls back to resolved — not just "":
	// a corrupt on-disk value would otherwise be displayed as the current
	// choice and, on Enter, written straight back (measured on a scripted
	// setup run). Setup doubles as the diagnosis, so rerunning it must heal
	// a broken key, not preserve it.
	cur := c.PerceiveBackend
	if cur != "ollama" && cur != "mlx-lm" {
		cur = resolved
	}
	fmt.Fprintf(out, "\n知覚バックエンド [ollama/mlx-lm, 現在: %s / Enterで維持] > ", cur)
	line, err := readLine(in)
	if err != nil {
		return err
	}
	switch line {
	case "ollama", "mlx-lm":
		c.PerceiveBackend = line
	default:
		if line != "" {
			fmt.Fprintf(out, "  ollama か mlx-lm で — 現状(%s)を維持\n", cur)
		}
		c.PerceiveBackend = cur
	}

	if c.PerceiveBackend == "mlx-lm" {
		return askMLXLM(in, out, c)
	}
	return askOllama(in, out, c)
}

func askOllama(in *bufio.Reader, out io.Writer, c *config.Config) error {
	urlDef := c.OllamaURL
	if urlDef == "" {
		urlDef = defaultOllamaURL
	}
	fmt.Fprintf(out, "Ollama の URL [%s / Enterで維持] > ", urlDef)
	line, err := readLine(in)
	if err != nil {
		return err
	}
	if line != "" {
		c.OllamaURL = strings.TrimRight(line, "/")
	}

	modelDef := c.OllamaModel
	if modelDef == "" {
		modelDef = defaultOllamaModel
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
		url = defaultOllamaURL
	}
	if model == "" {
		model = defaultOllamaModel
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

func askMLXLM(in *bufio.Reader, out io.Writer, c *config.Config) error {
	urlDef := c.MLXURL
	if urlDef == "" {
		urlDef = defaultMLXURL
	}
	fmt.Fprintf(out, "MLX LM Server の URL [%s / Enterで維持] > ", urlDef)
	line, err := readLine(in)
	if err != nil {
		return err
	}
	if line != "" {
		c.MLXURL = strings.TrimRight(line, "/")
	}

	modelDef := c.MLXModel
	if modelDef == "" {
		modelDef = defaultMLXModel
	}
	fmt.Fprintf(out, "知覚モデル [%s / Enterで維持] > ", modelDef)
	if line, err = readLine(in); err != nil {
		return err
	}
	if line != "" {
		c.MLXModel = line
	}

	url, model := c.MLXURL, c.MLXModel
	if url == "" {
		url = defaultMLXURL
	}
	if model == "" {
		model = defaultMLXModel
	}
	// An unreached server is diagnosis, not an error to fix now (知覚は
	// Deferred); an unrecognized model at a reachable server is not an error
	// either — mlx-lm loads it from Hugging Face on demand at first use
	// (ADR-0029 Context).
	switch has, err := mlxHasModel(url, model); {
	case err != nil:
		fmt.Fprintf(out, "  mlx-lm ✗ 届かない(%v) — 知覚はDeferredなので後でよい\n", err)
	case has:
		fmt.Fprintf(out, "  mlx-lm ✓ %s がキャッシュ済み\n", model)
	default:
		fmt.Fprintf(out, "  mlx-lm ・ %s は未キャッシュ → 初回知覚時にHFからダウンロードされる\n", model)
	}
	return nil
}

// askFaceAutoLaunch asks whether the face window auto-spawns on interactive
// commands (ADR-0025 Decision 2). "on" stores nil, not &true: absent already
// means on (the default), so keeping it nil avoids pinning a value that only
// restates the default. Current state is shown so Enter keeps it, like every
// other setup question; an unrecognized answer keeps the current value rather
// than aborting a setup whose earlier answers are not saved yet.
func askFaceAutoLaunch(in *bufio.Reader, out io.Writer, c *config.Config) error {
	cur := "on(既定)"
	if c.FaceAutoLaunch != nil {
		if *c.FaceAutoLaunch {
			cur = "on"
		} else {
			cur = "off"
		}
	}
	fmt.Fprintf(out, "\n顔窓(tomobit-face)を対話起動時に自動で開く? [on/off, 現在: %s / Enterで維持] > ", cur)
	line, err := readLine(in)
	if err != nil {
		return err
	}
	switch strings.ToLower(line) {
	case "":
	case "on", "y", "yes":
		c.FaceAutoLaunch = nil
	case "off", "n", "no":
		no := false
		c.FaceAutoLaunch = &no
	default:
		fmt.Fprintf(out, "  on か off で — 現状(%s)を維持\n", cur)
	}
	return nil
}

// askFaceResident asks whether the face window stays after the conversation
// ends (ADR-0027 Decision 4). "off" stores nil, not &false: absent already
// means ephemeral (the new default), so keeping it nil avoids pinning a value
// that only restates the default — the mirror of askFaceAutoLaunch's "on ⇒ nil".
// An unrecognized answer keeps the current value rather than aborting a setup
// whose earlier answers are not saved yet.
func askFaceResident(in *bufio.Reader, out io.Writer, c *config.Config) error {
	cur := "off(既定・対話終了で自閉)"
	if c.FaceResident != nil {
		if *c.FaceResident {
			cur = "on"
		} else {
			cur = "off"
		}
	}
	fmt.Fprintf(out, "\n顔窓を対話終了後も常駐させる? [on/off, 現在: %s / Enterで維持] > ", cur)
	line, err := readLine(in)
	if err != nil {
		return err
	}
	switch strings.ToLower(line) {
	case "":
	case "on", "y", "yes":
		yes := true
		c.FaceResident = &yes
	case "off", "n", "no":
		c.FaceResident = nil
	default:
		fmt.Fprintf(out, "  on か off で — 現状(%s)を維持\n", cur)
	}
	return nil
}

// askQuotaObserve asks whether to turn the ADR-0044 route-A observers on
// (ADR-0049). The mirror of askFaceResident: "on" stores &true and "off"
// stores nil, because absent already means off. What makes this question
// different from the others is that the prompt must state what saying yes
// actually does — reading a Keychain item and calling an endpoint the vendor
// never documented. An opt-in nobody understood is not consent, so the cost is
// spelled out in the question rather than left in the ADR.
func askQuotaObserve(in *bufio.Reader, out io.Writer, c *config.Config) error {
	cur := "off(既定)"
	if c.QuotaObserveEnabled() {
		cur = "on"
	}
	fmt.Fprintf(out, "\nProviderの残量をstatusに表示する?\n"+
		"  onにすると、あなた自身のOAuthトークンをmacOS Keychainから読み、\n"+
		"  ベンダーの**非公式な**usage端点を参照します(いつ消えてもおかしくない)。\n"+
		"  取れなければ「不明」と出るだけで、推定はせず、判断にも混ぜません。\n"+
		"  [on/off, 現在: %s / Enterで維持] > ", cur)
	line, err := readLine(in)
	if err != nil {
		return err
	}
	switch strings.ToLower(line) {
	case "":
	case "on", "y", "yes":
		yes := true
		c.QuotaObserve = &yes
	case "off", "n", "no":
		c.QuotaObserve = nil
	default:
		fmt.Fprintf(out, "  on か off で — 現状(%s)を維持\n", cur)
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

// mlxHasModel asks GET /v1/models (the OpenAI-compatible listing of
// mlx-lm's HF-cached models) whether model is already cached. A short
// timeout: setup must never hang on a daemon that isn't there. false with a
// nil error means "reachable but not cached yet" — mlx-lm downloads
// on-demand at first use, so that is diagnosis, not failure.
func mlxHasModel(url, model string) (bool, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url + "/v1/models")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return false, err
	}
	for _, m := range list.Data {
		if m.ID == model {
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
