package quota

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// keychainReadTimeout bounds the security shell-out. An unlocked login
// keychain answers instantly, but a locked one — or one whose access control
// prompts — could otherwise hang a status view; a bounded read degrades to
// 不明（理由）like every other quota failure (ADR-0044 Decision 5).
const keychainReadTimeout = 5 * time.Second

// ReadClaudeKeychain reads one generic-password item's payload from the macOS
// login keychain via `security find-generic-password -s <service> -w`. It is
// the real reader ADR-0044 Decision 1 deliberately left unwired in this
// package: the injectable ClaudeKeychainToken.Read seam, so no test in the
// package ever touches a real Keychain.
//
// The returned bytes are the credentials JSON (claudeAiOauth.accessToken lives
// inside); the caller parses and discards them — nothing here caches or logs
// the payload. The token never rides an error either: on failure only
// security's own stderr (which carries the diagnostic, not the -w password)
// and the service name surface. macOS-only by construction — `security` is the
// platform's tool, so off darwin this returns a clear error and the wiring
// falls back to the on-disk credentials file.
func ReadClaudeKeychain(service string) ([]byte, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("keychain read is macOS-only (GOOS=%s); use the credentials file instead", runtime.GOOS)
	}
	ctx, cancel := context.WithTimeout(context.Background(), keychainReadTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "security", "find-generic-password", "-s", service, "-w")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// stderr, not stdout: `security -w` writes the password to stdout on
		// success and the error to stderr on failure, so this line can never
		// echo a token. A missing item prints "could not be found".
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("security find-generic-password -s %q: %s", service, msg)
		}
		return nil, fmt.Errorf("security find-generic-password -s %q: %w", service, err)
	}
	// -w prints the password with a trailing newline; the JSON parser tolerates
	// it, but trimming keeps the payload byte-identical to the file source's.
	return bytes.TrimRight(stdout.Bytes(), "\r\n"), nil
}
