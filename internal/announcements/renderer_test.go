package announcements

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMarkdownProfileAllowedBlocksAndSingleRenderer(t *testing.T) {
	source := "## Heading\n\nParagraph with **strong**, *em*, `code`, and [link](https://example.com/a?q=1&x=2).\nnext line\n\n- one\n- two\n\n1. first\n2. second\n\n> quote\n> line\n\n```go\n<script>alert(1)</script>\n```"
	rendered, err := (markdownRenderer{}).render(source)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, expected := range []string{
		"<h2>Heading</h2>", "<strong>strong</strong>", "<em>em</em>", "<code>code</code>",
		`href="https://example.com/a?q=1&amp;x=2"`, "<br>", "<ul>", "<ol>", "<blockquote>",
		"&lt;script&gt;alert(1)&lt;/script&gt;",
	} {
		if !strings.Contains(rendered.html, expected) {
			t.Errorf("rendered HTML missing %q:\n%s", expected, rendered.html)
		}
	}
	if strings.Contains(rendered.html, "<script>") || strings.Contains(rendered.plain, "**") ||
		strings.Contains(rendered.plain, "href") {
		t.Fatalf("unsafe/non-plain projection: html=%q plain=%q", rendered.html, rendered.plain)
	}
	if RenderProfileVersion != "announcement-markdown/v1" {
		t.Fatalf("renderer profile = %q", RenderProfileVersion)
	}
}

func TestMarkdownProfileRejectsHostileAndUnsupportedCorpus(t *testing.T) {
	hostile := []string{
		`<script>alert(1)</script>`, `<img src=x onerror=alert(1)>`, `<iframe src="https://evil.example"></iframe>`,
		`<form action="https://evil.example"><input></form>`, `<style>body{display:none}</style>`,
		`<svg><a xlink:href="javascript:alert(1)">x</a></svg>`, `<math href="data:text/html,x">x</math>`,
		`![remote](https://evil.example/pixel.png)`, `[x](javascript:alert(1))`, `[x](data:text/html,x)`,
		`![remote][pixel]
[pixel]: https://evil.example/pixel.png`, `[x](JaVaScRiPt:alert(1))`, `[x](vbscript:msgbox(1))`,
		`[x](blob:https://example.com/id)`, `[x](file:///etc/passwd)`, `[x](mailto:user@example.com)`,
		`[x](https://user:pass@example.com/)`, `[x](//example.com/)`,
		`<https://example.com>`, "```\nunclosed", "# h1", "##### h5", "    indented code", "---",
		"bad\x00byte", "bad\rline", "bad\u000bline", "[x](https://example.com/a b)",
	}
	for _, source := range hostile {
		t.Run(strings.ReplaceAll(source, "/", "_"), func(t *testing.T) {
			if _, err := (markdownRenderer{}).render(source); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("hostile markdown accepted: %q, err=%v", source, err)
			}
		})
	}
}

func TestMarkdownHTTPLinkAttributesRemainEscaped(t *testing.T) {
	rendered, err := (markdownRenderer{}).render(`[safe](https://example.com/%22onmouseover=%22alert%281%29)`)
	if err != nil {
		t.Fatalf("render escaped URL: %v", err)
	}
	if strings.Contains(rendered.html, `" onmouseover=`) || !strings.Contains(rendered.html, `href="https://example.com/%22onmouseover=%22alert%281%29"`) {
		t.Fatalf("unsafe or altered link attribute: %s", rendered.html)
	}
}

func TestMarkdownBackslashEscapePreservesUnicodeScalar(t *testing.T) {
	rendered, err := (markdownRenderer{}).render(`escaped: \界 and \🐟`)
	if err != nil {
		t.Fatalf("render unicode escape: %v", err)
	}
	if rendered.html != "<p>escaped: 界 and 🐟</p>\n" || rendered.plain != "escaped: 界 and 🐟" ||
		!utf8.ValidString(rendered.html) || !utf8.ValidString(rendered.plain) {
		t.Fatalf("unicode escape was corrupted: html=%q plain=%q", rendered.html, rendered.plain)
	}
}

func TestMarkdownExcerptIsBoundedSafePlainText(t *testing.T) {
	body := "## " + strings.Repeat("界", 260) + "\n\n[visible](https://example.com)"
	rendered, err := (markdownRenderer{}).render(body)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got := utf8.RuneCountInString(rendered.plain); got != maxExcerptRunes {
		t.Fatalf("excerpt runes=%d want=%d", got, maxExcerptRunes)
	}
	if strings.ContainsAny(rendered.plain, "<>") || strings.Contains(rendered.plain, "https://") {
		t.Fatalf("excerpt is not safe plain text: %q", rendered.plain)
	}
}
