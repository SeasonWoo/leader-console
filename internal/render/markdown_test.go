package render

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMarkdownDocument(t *testing.T) {
	src := "# Title\n\n## Two\n### Three\n#### Four\n\nparagraph **bold** and *italic* and __strong__ and _em_ and `x < y`.\nnext line\n\n- one\n- two\n\n1. first\n2. second\n\n> quoted **text**\n>\n> next\n\n---\n\n[site](https://example.com/a?q=1&v=2)"
	want := "<h1>Title</h1>\n<h2>Two</h2>\n<h3>Three</h3>\n<h4>Four</h4>\n" +
		"<p>paragraph <strong>bold</strong> and <em>italic</em> and <strong>strong</strong> and <em>em</em> and <code>x &lt; y</code>.\nnext line</p>\n" +
		"<ul>\n<li>one</li>\n<li>two</li>\n</ul>\n<ol>\n<li>first</li>\n<li>second</li>\n</ol>\n" +
		"<blockquote>\n<p>quoted <strong>text</strong></p>\n<p>next</p>\n</blockquote>\n<hr>\n" +
		`<p><a href="https://example.com/a?q=1&amp;v=2" rel="noopener noreferrer" target="_blank">site</a></p>` + "\n"
	if got := Markdown(src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCodeIsOpaque(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"```go\n# title\n**bold** [x](https://example.com)\n<script>\n```", "<pre><code># title\n**bold** [x](https://example.com)\n&lt;script&gt;\n</code></pre>\n"},
		{"~~~\n`a`\n~~~", "<pre><code>`a`\n</code></pre>\n"},
		{"````\n```\n# raw\n`````", "<pre><code>```\n# raw\n</code></pre>\n"},
		{"```\nunclosed", "<pre><code>unclosed\n</code></pre>\n"},
		{"``a ` b **bold**``", "<p><code>a ` b **bold**</code></p>\n"},
		{"`[x](https://example.com) **b**`", "<p><code>[x](https://example.com) **b**</code></p>\n"},
		{"`unfinished", "<p>`unfinished</p>\n"},
		{"\\*literal\\* and \\[text\\]", "<p>*literal* and [text]</p>\n"},
	} {
		if got := Markdown(tc.in); got != tc.want {
			t.Errorf("Markdown(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestTable(t *testing.T) {
	src := "| Name | Result |\n| :--- | ---: |\n| **a** | `<b>` |\n| b\\|c | [report](http://example.com) |\n| missing |\n| one | two | ignored |\n\nAfter"
	want := "<table><thead><tr><th>Name</th><th>Result</th></tr></thead><tbody>" +
		"<tr><td><strong>a</strong></td><td><code>&lt;b&gt;</code></td></tr>" +
		`<tr><td>b|c</td><td><a href="http://example.com" rel="noopener noreferrer" target="_blank">report</a></td></tr>` +
		"<tr><td>missing</td><td></td></tr><tr><td>one</td><td>two</td></tr></tbody></table>\n<p>After</p>\n"
	if got := Markdown(src); got != want {
		t.Fatalf("table = %s", got)
	}
	for _, src := range []string{"a | b\n--- | invalid", "a | b\n---", "| only |", "no table"} {
		if got := Markdown(src); strings.Contains(got, "<table>") {
			t.Fatalf("invalid table accepted: %q => %s", src, got)
		}
	}
}

var (
	generatedTag = regexp.MustCompile(`<[^>]*>`)
	allowedTag   = regexp.MustCompile(`^(?:</?(?:h[1-4]|p|ul|ol|li|pre|code|table|thead|tbody|tr|th|td|strong|em|a|blockquote)>|<hr>|<a href="[^"]*" rel="noopener noreferrer" target="_blank">)$`)
)

func assertSafe(t *testing.T, output string) {
	t.Helper()
	for _, tag := range generatedTag.FindAllString(output, -1) {
		if !allowedTag.MatchString(tag) {
			t.Fatalf("unexpected tag %q in %s", tag, output)
		}
		if strings.HasPrefix(tag, "<a ") && !strings.HasPrefix(tag, `<a href="http://`) && !strings.HasPrefix(tag, `<a href="https://`) {
			t.Fatalf("unsafe URL: %s", tag)
		}
	}
}

func TestXSS(t *testing.T) {
	for _, src := range []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`<svg/onload=alert(1)>`,
		`[click](javascript:alert(1))`,
		`[click](data:text/html,<script>alert(1)</script>)`,
		`[click](//evil.example.com)`,
		`[click](https://example.com/"onmouseover="alert)`,
		`[click](https://example.com/" onmouseover="alert)`,
		`[click](https://example.com/&#34;onclick=&#34;alert)`,
		`[click](javascript&#58;alert(1))`,
		"[click](java\nscript:alert(1))",
		"[click](https://example.com/\x00evil)",
		`[<img onerror="boom">](https://example.com)`,
		"`` `</code><script>alert(1)</script>` ``",
		`[**[nested](https://evil.com)**](https://example.com)`,
		`![image](https://example.com/image.png)`,
	} {
		got := Markdown(src)
		assertSafe(t, got)
		if strings.Contains(got, "<script") || strings.Contains(got, "<img") || strings.Contains(got, "<svg") {
			t.Fatalf("raw HTML: %q => %s", src, got)
		}
	}
	for _, src := range []string{`[x](javascript:alert(1))`, `[x](data:text/plain,payload)`, `[x](/relative)`, `[x](https://)`, `[x](https://example.com/%zz)`} {
		if got := Markdown(src); strings.Contains(got, "<a ") {
			t.Fatalf("unsafe destination accepted: %s", got)
		}
	}
	got := Markdown(`[x](https://example.com/"onclick="alert)`)
	if !strings.Contains(got, "&#34;") {
		t.Fatalf("attribute quotes were not escaped: %s", got)
	}
}

func TestBoundaries(t *testing.T) {
	for _, src := range []string{
		"", "\n\n", "hello\r\nworld", "***", "___", "* * *", "1) a\n- b", "*unfinished", "**", "[unfinished", "[x](https://example.com/unclosed", "[x](https://example.com/a(b))",
		strings.Repeat("> ", 20) + "deep", strings.Repeat("**a ", 30) + strings.Repeat("b** ", 30),
		strings.Repeat("[", maxBytes), strings.Repeat("[", maxBytes-30) + "](https://example.com/(",
		"invalid\xffUTF8", "[x](http://[::1]:7788)", "text\n### title", "`a `` b `",
	} {
		out := Markdown(src)
		if !utf8.ValidString(out) {
			t.Fatalf("invalid UTF8: %q", out)
		}
		assertSafe(t, out)
	}
	if got := Markdown(strings.Repeat("a", maxBytes) + "SECRET_SUFFIX"); strings.Contains(got, "SECRET_SUFFIX") || len(got) != maxBytes+8 {
		t.Fatalf("input was not capped: len=%d", len(got))
	}
	src := strings.Repeat("a", maxBytes-1) + "中文"
	if got := Markdown(src); !utf8.ValidString(got) || strings.Contains(got, "中") {
		t.Fatal("cutoff did not discard incomplete rune")
	}
}

func FuzzMarkdownSafe(f *testing.F) {
	for _, seed := range []string{"<script>x</script>", `[x](javascript:alert(1))`, "# title\n\n```\nraw\n```", "[x](https://example.com/\"onclick=\"x)", "|a|b|\n|---|---|\n|1|2|", "**`[x](https://example.com)`**"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) { assertSafe(t, Markdown(src)) })
}
