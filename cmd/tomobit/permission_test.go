package main

import (
	"bufio"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/store"
)

// denyingAdapter refuses a tool on its first run and works on the second, which
// is the exact shape ADR-0053 Decision 2 has to handle: the permission request
// ends the turn, so the only way forward is to grant and run again.
type denyingAdapter struct {
	denyTool string
	runs     int
	allowed  [][]string
}

func (a *denyingAdapter) Name() string { return "fake" }

func (a *denyingAdapter) Command(req executor.Request) (string, []string, []string) {
	a.runs++
	a.allowed = append(a.allowed, req.AllowedTools)
	// 2回目以降（＝許可された後）は素直に答える。
	if a.runs > 1 {
		return "printf", []string{"SEL\nDONE\n"}, nil
	}
	return "printf", []string{"SEL\nDENY\n"}, nil
}

func (a *denyingAdapter) Translate(line []byte) ([]executor.Event, error) {
	switch s := strings.TrimSpace(string(line)); s {
	case "":
		return nil, nil
	case "SEL":
		return []executor.Event{{Type: executor.EventProviderSelected, Payload: map[string]any{
			"provider": "fake", "provider_session_id": "th-1",
		}}}, nil
	case "DENY":
		return []executor.Event{{
			Type:    executor.EventPermissionRequired,
			Payload: map[string]any{"tool": a.denyTool, "detail": "/tmp/x.go"},
		}}, nil
	default:
		return []executor.Event{{Type: executor.EventProviderOutput,
			Payload: map[string]any{"text": s}}}, nil
	}
}

func newPermissionChat(t *testing.T, s *store.Store, a *denyingAdapter, in string) *chat {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	providers["fake"] = a
	t.Cleanup(func() { delete(providers, "fake") })
	no := false
	saved := cfg
	t.Cleanup(func() { cfg = saved })
	cfg.IsolateProtocol = &no // 隔離の指示はこのテストの関心ではない
	cfg.SplitProtocol = &no
	return &chat{
		s: s, out: &strings.Builder{}, providerName: "fake", capability: "implement",
		extractor: &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}},
		in:        bufio.NewReader(strings.NewReader(in)),
	}
}

// 許可すると、その道具を足して同じターンをやり直す (ADR-0053 Decision 2)。
// 実測のとおり許可要求はターンを終わらせるので、これが唯一の前進の形である。
func TestPermissionGrantedRerunsTheTurnWithTheToolAllowed(t *testing.T) {
	s := openTestStore(t)
	a := &denyingAdapter{denyTool: "Edit"}
	c := newPermissionChat(t, s, a, "1\n\n\n")

	if err := c.turn("ファイルを直して"); err != nil {
		t.Fatal(err)
	}

	if a.runs != 2 {
		t.Fatalf("許可の後にやり直すはずが %d 回しか走っていない", a.runs)
	}
	if len(a.allowed[0]) != 0 {
		t.Errorf("1回目は何も許可されていない: %v", a.allowed[0])
	}
	if len(a.allowed[1]) != 1 || a.allowed[1][0] != "Edit" {
		t.Errorf("2回目は許可された道具だけを持つ: %v", a.allowed[1])
	}
}

// 断ってもターンは終わるだけで、作業も会話も失われない — ADR-0028 の並走ゲートが
// 「n でも作業は失われない」と決めたのと同じ姿勢。
func TestPermissionRefusedEndsTheTurnWithoutRerunning(t *testing.T) {
	s := openTestStore(t)
	a := &denyingAdapter{denyTool: "Edit"}
	c := newPermissionChat(t, s, a, "\n\n\n") // Enter = 許可しない

	if err := c.turn("ファイルを直して"); err != nil {
		t.Fatal(err)
	}
	if a.runs != 1 {
		t.Errorf("断ったのにやり直した: %d 回", a.runs)
	}
	if len(c.allowedTools) != 0 {
		t.Errorf("断ったのに許可が残った: %v", c.allowedTools)
	}
}

// 沈黙は同意ではない (本体 ADR-0049)。答えの無いパイプは拒否として扱う。
func TestPermissionWithNobodyAnsweringIsARefusal(t *testing.T) {
	s := openTestStore(t)
	a := &denyingAdapter{denyTool: "Bash"}
	c := newPermissionChat(t, s, a, "") // EOF

	if err := c.turn("なにかして"); err != nil {
		t.Fatal(err)
	}
	if a.runs != 1 {
		t.Errorf("誰も答えていないのに許可された: %d 回", a.runs)
	}
}

// 許可は台帳に書かない (ADR-0053 Decision 4): 「何を許したか」はどう走らせるかの
// 話で、何が起きたかではない。provider.error でもない — 実行は正常に終わっており、
// 進まなかったのは作業の方である。
func TestPermissionNeverReachesTheLedger(t *testing.T) {
	s := openTestStore(t)
	a := &denyingAdapter{denyTool: "Edit"}
	c := newPermissionChat(t, s, a, "\n\n\n")

	if err := c.turn("ファイルを直して"); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"permission.required", "provider.error"} {
		if n := countEventsOfTypeInSession(t, s, c.sid, typ); n != 0 {
			t.Errorf("%s が台帳に載った: %d件", typ, n)
		}
	}
}

// 許した道具はタスクの区切りまで (Decision 3)。覚えたままだと、次に人が見ていない
// 時にも効いてしまう。
func TestGrantsDoNotSurviveTheTaskBoundary(t *testing.T) {
	s := openTestStore(t)
	a := &denyingAdapter{denyTool: "Edit"}
	c := newPermissionChat(t, s, a, "1\n\n\n\n\n")

	if err := c.turn("ファイルを直して"); err != nil {
		t.Fatal(err)
	}
	if len(c.allowedTools) == 0 {
		t.Fatalf("許可が記録されていない")
	}
	if err := c.closeTask(); err != nil {
		t.Fatal(err)
	}
	if len(c.allowedTools) != 0 || c.permissionRounds != 0 {
		t.Errorf("区切りを越えて許可が残った: %v (rounds=%d)", c.allowedTools, c.permissionRounds)
	}
}

// 1つの依頼が人を呼び止めてよい回数には上限がある。無いと「もう1つ道具が要る」の
// 発見ごとに、人と費用を往復させられる。
func TestPermissionRoundsAreBounded(t *testing.T) {
	s := openTestStore(t)
	// 毎回ちがう道具を拒否し続けるアダプタ
	a := &alwaysDenyingAdapter{}
	t.Setenv("HOME", t.TempDir())
	providers["fake"] = a
	t.Cleanup(func() { delete(providers, "fake") })
	no := false
	saved := cfg
	t.Cleanup(func() { cfg = saved })
	cfg.IsolateProtocol, cfg.SplitProtocol = &no, &no

	c := &chat{
		s: s, out: &strings.Builder{}, providerName: "fake", capability: "implement",
		extractor: &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}},
		in:        bufio.NewReader(strings.NewReader(strings.Repeat("1\n", 20))),
	}
	if err := c.turn("なにかして"); err != nil {
		t.Fatal(err)
	}
	if a.runs > maxPermissionRounds+1 {
		t.Errorf("上限を超えて走った: %d 回 (上限 %d)", a.runs, maxPermissionRounds)
	}
}

type alwaysDenyingAdapter struct{ runs int }

func (a *alwaysDenyingAdapter) Name() string { return "fake" }

func (a *alwaysDenyingAdapter) Command(executor.Request) (string, []string, []string) {
	a.runs++
	return "printf", []string{"SEL\nDENY\n"}, nil
}

func (a *alwaysDenyingAdapter) Translate(line []byte) ([]executor.Event, error) {
	switch s := strings.TrimSpace(string(line)); s {
	case "":
		return nil, nil
	case "SEL":
		return []executor.Event{{Type: executor.EventProviderSelected, Payload: map[string]any{
			"provider": "fake", "provider_session_id": "th-1",
		}}}, nil
	default:
		// 毎回ちがう道具を求める
		return []executor.Event{{
			Type:    executor.EventPermissionRequired,
			Payload: map[string]any{"tool": "Tool" + strings.Repeat("x", a.runs)},
		}}, nil
	}
}
