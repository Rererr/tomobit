package core

import (
	"sort"
	"strings"
)

// Scope is a sorted, unique set of canonical "key=value" tokens.
// Its Key is the '|'-joined form used as the connections primary key
// (SCHEMA.md D5/D8).
type Scope []string

// NewScope canonicalizes: sort + dedupe.
func NewScope(tokens ...string) Scope {
	s := make([]string, 0, len(tokens))
	seen := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		s = append(s, t)
	}
	sort.Strings(s)
	return s
}

func (s Scope) Key() string { return strings.Join(s, "|") }

func ParseScopeKey(key string) Scope {
	if key == "" {
		return Scope{}
	}
	return NewScope(strings.Split(key, "|")...)
}

// SubsetOf reports whether every token of s is present in tokens.
func (s Scope) SubsetOf(tokens []string) bool {
	set := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		set[t] = true
	}
	for _, t := range s {
		if !set[t] {
			return false
		}
	}
	return true
}

// Plus returns a new scope with one more token.
func (s Scope) Plus(token string) Scope {
	return NewScope(append(append([]string{}, s...), token)...)
}

// Minus returns the tokens of s that are not in other.
func (s Scope) Minus(other Scope) []string {
	set := make(map[string]bool, len(other))
	for _, t := range other {
		set[t] = true
	}
	var out []string
	for _, t := range s {
		if !set[t] {
			out = append(out, t)
		}
	}
	return out
}
