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
