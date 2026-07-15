package voice

import (
	"testing"

	"github.com/Rererr/tomobit/internal/core"
)

func TestScopeDisplayShowsValuesOnly(t *testing.T) {
	got := ScopeDisplay(core.NewScope("lang=go", "topic=api"))
	if want := "go・api"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestScopeDisplayPassesThroughTokensWithoutEquals(t *testing.T) {
	got := ScopeDisplay(core.Scope{"standalone"})
	if want := "standalone"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFirstMeetingIncludesQuotes(t *testing.T) {
	got := FirstMeeting()
	if want := "「はじめまして。まだなにも知らないんだ」"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
