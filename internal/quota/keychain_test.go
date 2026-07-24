package quota

import (
	"runtime"
	"strings"
	"testing"
)

// TestReadClaudeKeychainOffDarwinIsAClearError guards the platform boundary:
// off macOS the reader must fail with a message that names the reason and
// points at the file fallback, not surface a confusing exec-not-found — the
// wiring relies on this to choose the file source instead.
func TestReadClaudeKeychainOffDarwinIsAClearError(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("this guards the non-darwin path; on darwin the real security tool runs")
	}
	_, err := ReadClaudeKeychain("Claude Code-credentials")
	if err == nil {
		t.Fatal("ReadClaudeKeychain off darwin should error, got nil")
	}
	if !strings.Contains(err.Error(), "macOS-only") {
		t.Errorf("error = %q, want it to name the macOS-only reason", err)
	}
}

// TestClaudeKeychainServiceNameNeverEchoesTheToken is a belt-and-braces check
// on the fetch error path this reader feeds: the service-name derivation, the
// only user-supplied string that reaches an error, is a pure function of the
// config dir — no token can be in it. (The token non-leakage of the reader
// itself is structural: stdout, where -w prints the password, is never placed
// in an error; see ReadClaudeKeychain.)
func TestClaudeKeychainServiceNameNeverEchoesTheToken(t *testing.T) {
	got := ClaudeKeychainServiceName("/Users/example/.claude-personal")
	if got != "Claude Code-credentials-5034c31c" {
		t.Errorf("service = %q, want the measured derivation Claude Code-credentials-5034c31c", got)
	}
	if ClaudeKeychainServiceName("") != "Claude Code-credentials" {
		t.Errorf("empty configDir must map to the bare default service name")
	}
}
