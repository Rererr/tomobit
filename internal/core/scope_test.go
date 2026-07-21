package core

import (
	"reflect"
	"testing"
)

func TestNewScopeSortsDedupesAndDropsEmpty(t *testing.T) {
	got := NewScope("b=2", "a=1", "b=2", "", "a=1")
	want := Scope{"a=1", "b=2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNewScopeEmptyHasEmptyKey(t *testing.T) {
	if k := NewScope().Key(); k != "" {
		t.Errorf("empty scope key: got %q, want %q", k, "")
	}
}

func TestKeyJoinsWithPipe(t *testing.T) {
	if k := NewScope("a=1", "b=2").Key(); k != "a=1|b=2" {
		t.Errorf("got %q, want %q", k, "a=1|b=2")
	}
}

func TestParseScopeKeyRoundTrips(t *testing.T) {
	for _, key := range []string{"", "a=1", "a=1|b=2|c=3"} {
		if got := ParseScopeKey(key).Key(); got != key {
			t.Errorf("round-trip %q: got %q", key, got)
		}
	}
}

// TestParseScopeKeyIsInjectiveOverCanonTokens fixes the contract Connection
// matching depends on: for any tokens CanonToken can produce, Key/
// ParseScopeKey must be a lossless round-trip — same token count, same
// tokens, regardless of value content or the order attributes were
// collected in. A value containing '|' (the token separator) previously
// re-split into an extra token here, silently changing the token count and
// orphaning the Connection.
func TestParseScopeKeyIsInjectiveOverCanonTokens(t *testing.T) {
	tests := []struct {
		name       string
		pairs      [][2]string // key, value
		wantTokens int         // distinct tokens NewScope should keep, post dedupe
	}{
		{"single pipe in value", [][2]string{{"topic", "CI|CD"}}, 1},
		{"multiple pipes in one value", [][2]string{{"topic", "a|b|c"}}, 1},
		{"pipe alongside plain tokens", [][2]string{
			{"topic", "ci|cd"}, {"lang", "rust"}, {"cap", "b|onus"},
		}, 3},
		{"same tokens, reversed collection order", [][2]string{
			{"cap", "b|onus"}, {"lang", "rust"}, {"topic", "ci|cd"},
		}, 3},
		{"pipe and equals mixed in one value", [][2]string{{"topic", "a=b|c"}}, 1},
		// CanonValue maps '|' to '-' and lowercases, so two raw values that
		// differ only in case land on the same canonical token; NewScope must
		// collapse them rather than let the duplicate survive into Key.
		{"same pair collected twice under different casing collapses to one token", [][2]string{
			{"topic", "CI|CD"}, {"topic", "ci|cd"},
		}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks := make([]string, len(tt.pairs))
			for i, p := range tt.pairs {
				toks[i] = CanonToken(p[0], p[1])
			}
			scope := NewScope(toks...)
			if len(scope) != tt.wantTokens {
				t.Fatalf("got %d tokens %v, want %d", len(scope), scope, tt.wantTokens)
			}
			got := ParseScopeKey(scope.Key())
			if !reflect.DeepEqual(got, scope) {
				t.Errorf("round-trip broken: got %v, want %v", got, scope)
			}
		})
	}
}

func TestSubsetOf(t *testing.T) {
	tokens := []string{"a=1", "b=2"}
	tests := []struct {
		name  string
		scope Scope
		want  bool
	}{
		{"single present", NewScope("a=1"), true},
		{"all present", NewScope("a=1", "b=2"), true},
		{"absent token", NewScope("c=3"), false},
		{"partly absent", NewScope("a=1", "c=3"), false},
		{"empty scope", NewScope(), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.scope.SubsetOf(tokens); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlusAddsTokenAndStaysCanonical(t *testing.T) {
	if got := NewScope("b=2").Plus("a=1").Key(); got != "a=1|b=2" {
		t.Errorf("got %q, want %q", got, "a=1|b=2")
	}
}

func TestPlusDedupesExistingToken(t *testing.T) {
	if got := NewScope("a=1").Plus("a=1").Key(); got != "a=1" {
		t.Errorf("got %q, want %q", got, "a=1")
	}
}

func TestMinusReturnsTokensNotInOther(t *testing.T) {
	got := NewScope("a=1", "b=2", "c=3").Minus(NewScope("a=1", "c=3"))
	want := []string{"b=2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
