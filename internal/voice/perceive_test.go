package voice

import (
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/face"
)

func TestGrowthForward(t *testing.T) {
	text, ok := Growth(face.StageChick, face.StageChild)
	if !ok {
		t.Fatal("a stage change should speak")
	}
	if want := "……なんだか、少し育った気がする。いまはこどもだよ"; text != want {
		t.Errorf("got %q, want %q", text, want)
	}
}

func TestGrowthBackward(t *testing.T) {
	text, ok := Growth(face.StageChild, face.StageChick)
	if !ok {
		t.Fatal("a stage change should speak, including regression")
	}
	if want := "……なんだか、少し縮んだ気がする。いまはひよこだよ"; text != want {
		t.Errorf("got %q, want %q", text, want)
	}
}

func TestGrowthNoChangeIsSilent(t *testing.T) {
	if _, ok := Growth(face.StageChild, face.StageChild); ok {
		t.Error("an unchanged stage must not speak")
	}
}

func TestInsight(t *testing.T) {
	child := &core.Connection{
		Kind: core.ConnCapability, ScopeKey: "lang=rust|topic=api", Target: "claude",
		ParentKey: "lang=rust",
	}
	text, ok := Insight([]*core.Connection{child})
	if !ok {
		t.Fatal("a new split should speak")
	}
	if want := "apiのときは勝手が違うんだって、わかってきたよ"; text != want {
		t.Errorf("got %q, want %q", text, want)
	}
}

func TestInsightNoneIsSilent(t *testing.T) {
	if _, ok := Insight(nil); ok {
		t.Error("no split must not speak")
	}
}

func TestMurmurSingleExperience(t *testing.T) {
	exp := &core.Experience{Kind: core.KindExecution, Context: map[string]string{"lang": "rust"}}
	text, ok := Murmur([]*core.Experience{exp})
	if !ok {
		t.Fatal("one experience should speak")
	}
	if want := "rustの仕事、経験にしたよ"; text != want {
		t.Errorf("got %q, want %q", text, want)
	}
}

func TestMurmurMultipleExperiences(t *testing.T) {
	exps := []*core.Experience{{}, {}, {}}
	text, ok := Murmur(exps)
	if !ok {
		t.Fatal("multiple experiences should speak")
	}
	if want := "3件の仕事、経験にしたよ"; text != want {
		t.Errorf("got %q, want %q", text, want)
	}
}

func TestMurmurNoneIsSilent(t *testing.T) {
	if _, ok := Murmur(nil); ok {
		t.Error("nothing perceived must not speak")
	}
}

func TestPerceivePriority(t *testing.T) {
	child := &core.Connection{
		Kind: core.ConnCapability, ScopeKey: "lang=rust|topic=api", Target: "claude",
		ParentKey: "lang=rust",
	}
	exp := &core.Experience{Context: map[string]string{"lang": "rust"}}

	t.Run("growth outranks insight and murmur", func(t *testing.T) {
		text, ok := Perceive(face.StageChick, face.StageChild, []*core.Connection{child}, []*core.Experience{exp})
		if !ok || !strings.Contains(text, "育った") {
			t.Errorf("growth should win, got %q ok=%v", text, ok)
		}
	})
	t.Run("insight outranks murmur", func(t *testing.T) {
		text, ok := Perceive(face.StageChick, face.StageChick, []*core.Connection{child}, []*core.Experience{exp})
		if !ok || !strings.Contains(text, "勝手が違う") {
			t.Errorf("insight should win, got %q ok=%v", text, ok)
		}
	})
	t.Run("murmur is the fallback", func(t *testing.T) {
		text, ok := Perceive(face.StageChick, face.StageChick, nil, []*core.Experience{exp})
		if !ok || !strings.Contains(text, "経験にしたよ") {
			t.Errorf("murmur should win, got %q ok=%v", text, ok)
		}
	})
	t.Run("no signal is silent", func(t *testing.T) {
		if _, ok := Perceive(face.StageChick, face.StageChick, nil, nil); ok {
			t.Error("no growth, insight, or murmur must not speak")
		}
	})
}

func TestNewSplitsSortsDeterministically(t *testing.T) {
	before := []*core.Connection{
		{Kind: core.ConnCapability, ScopeKey: "lang=rust", Target: "claude"},
	}
	after := []*core.Connection{
		{Kind: core.ConnCapability, ScopeKey: "lang=rust", Target: "claude"},
		{Kind: core.ConnCapability, ScopeKey: "lang=rust|topic=web", Target: "claude", ParentKey: "lang=rust"},
		{Kind: core.ConnCapability, ScopeKey: "lang=rust|topic=api", Target: "claude", ParentKey: "lang=rust"},
	}
	splits := NewSplits(before, after)
	if len(splits) != 2 {
		t.Fatalf("want 2 new splits, got %d", len(splits))
	}
	if splits[0].ScopeKey != "lang=rust|topic=api" {
		t.Errorf("splits should sort by scope key, got %q first", splits[0].ScopeKey)
	}
}

func TestNewSplitsIgnoresUnchangedConnections(t *testing.T) {
	before := []*core.Connection{
		{Kind: core.ConnCapability, ScopeKey: "lang=rust", Target: "claude"},
	}
	if splits := NewSplits(before, before); len(splits) != 0 {
		t.Errorf("an unchanged snapshot must yield no splits, got %v", splits)
	}
}
