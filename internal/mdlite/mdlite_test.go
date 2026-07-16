package mdlite

import "testing"

const (
	reset      = "\x1b[0m"
	bold       = "\x1b[1m"
	dim        = "\x1b[2m"
	inlineCode = "\x1b[36m"
)

func TestRender(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain prose is unchanged",
			in:   "just a sentence, nothing to mark up.",
			want: "just a sentence, nothing to mark up.",
		},
		{
			name: "bold marker becomes ANSI bold and stars are removed",
			in:   "the **important** part",
			want: "the " + bold + "important" + reset + " part",
		},
		{
			name: "inline code becomes ANSI and backticks are removed",
			in:   "run `go test` now",
			want: "run " + inlineCode + "go test" + reset + " now",
		},
		{
			name: "heading text is bolded and the hashes are dropped",
			in:   "## Section title",
			want: bold + "Section title" + reset,
		},
		{
			name: "six-hash heading is still a heading",
			in:   "###### deep",
			want: bold + "deep" + reset,
		},
		{
			name: "seven hashes is not a heading",
			in:   "####### too deep",
			want: "####### too deep",
		},
		{
			name: "hash without a following space is not a heading",
			in:   "#nospace",
			want: "#nospace",
		},
		{
			name: "dash bullet marker becomes a bullet glyph",
			in:   "- first item",
			want: "• first item",
		},
		{
			name: "star bullet marker becomes a bullet glyph",
			in:   "* first item",
			want: "• first item",
		},
		{
			name: "indented bullet keeps its indent",
			in:   "    - nested item",
			want: "    • nested item",
		},
		{
			name: "bullet content is still formatted inline",
			in:   "- use `flag` here",
			want: "• use " + inlineCode + "flag" + reset + " here",
		},
		{
			name: "unclosed bold marker is left literal",
			in:   "a **dangling start",
			want: "a **dangling start",
		},
		{
			name: "unclosed inline code is left literal",
			in:   "a `dangling backtick",
			want: "a `dangling backtick",
		},
		{
			name: "code fence line dims and its body is verbatim",
			in:   "```go\nx := **notbold**\n```",
			want: dim + "```go" + reset + "\nx := **notbold**\n" + dim + "```" + reset,
		},
		{
			name: "unclosed fence keeps every following line verbatim",
			in:   "```\n**still** not bold\nmore raw",
			want: dim + "```" + reset + "\n**still** not bold\nmore raw",
		},
		{
			name: "first marker opened wins the span",
			in:   "**bold with `tick` inside**",
			want: bold + "bold with `tick` inside" + reset,
		},
		{
			name: "full-width japanese bold is not corrupted",
			in:   "これは**大事**な点",
			want: "これは" + bold + "大事" + reset + "な点",
		},
		{
			name: "full-width japanese heading is bolded",
			in:   "# 見出し",
			want: bold + "見出し" + reset,
		},
		{
			name: "trailing newline is preserved",
			in:   "line\n",
			want: "line\n",
		},
		{
			name: "long command line is not truncated by the renderer",
			in:   "plain long line kept as is even when it is quite a bit longer than sixty",
			want: "plain long line kept as is even when it is quite a bit longer than sixty",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Render(c.in); got != c.want {
				t.Errorf("Render(%q)\n got: %q\nwant: %q", c.in, got, c.want)
			}
		})
	}
}

// TestRenderFenceStateIsScopedToTheCall pins that fence tracking does not leak
// across calls: a message that opens a fence must not silence the bold in the
// next, separately rendered message.
func TestRenderFenceStateIsScopedToTheCall(t *testing.T) {
	if got := Render("```\nopen fence never closed"); got == "" {
		t.Fatal("first render produced nothing")
	}
	got := Render("**bold** again")
	want := bold + "bold" + reset + " again"
	if got != want {
		t.Errorf("second call should start with a fresh fence state\n got: %q\nwant: %q", got, want)
	}
}
