package render

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxBytes = 1 << 20

var (
	heading = regexp.MustCompile(`^(#{1,4})\s+(.+)$`)
	list    = regexp.MustCompile(`^(?:([-+*])|([0-9]+)[.)])\s+(.+)$`)
	divider = regexp.MustCompile(`^:?-{3,}:?$`)
)

// Markdown renders a deliberately small Markdown subset, never raw HTML.
// It escapes the entire input before parsing and emits only fixed whitelist tags.
// Input is capped at one MiB; incomplete UTF-8 at the cutoff is discarded.
func Markdown(src string) string {
	if len(src) > maxBytes {
		src = src[:maxBytes]
		for len(src) > 0 {
			r, size := utf8.DecodeLastRuneInString(src)
			if r != utf8.RuneError || size != 1 {
				break
			}
			src = src[:len(src)-1]
		}
	}
	src = strings.ToValidUTF8(src, "�")
	src = strings.ReplaceAll(src, "\r\n", "\n")
	return blocks(strings.Split(html.EscapeString(src), "\n"), 0)
}

func fence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") || strings.HasPrefix(s, "~~~") {
		i := 0
		for i < len(s) && s[i] == s[0] {
			i++
		}
		return s[:i]
	}
	return ""
}

func rule(s string) bool {
	s = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(s), " ", ""), "\t", "")
	return len(s) >= 3 && (strings.Trim(s, "-") == "" || strings.Trim(s, "*") == "" || strings.Trim(s, "_") == "")
}

func cells(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")
	var out []string
	var cell strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] == '|' {
			cell.WriteByte('|')
			i++
		} else if s[i] == '|' {
			out = append(out, strings.TrimSpace(cell.String()))
			cell.Reset()
		} else {
			cell.WriteByte(s[i])
		}
	}
	return append(out, strings.TrimSpace(cell.String()))
}

func tableStart(lines []string, i int) bool {
	if i+1 >= len(lines) || !strings.Contains(lines[i], "|") {
		return false
	}
	head, sep := cells(lines[i]), cells(lines[i+1])
	if len(head) != len(sep) {
		return false
	}
	for _, cell := range sep {
		if !divider.MatchString(cell) {
			return false
		}
	}
	return true
}

func special(lines []string, i int) bool {
	s := strings.TrimSpace(lines[i])
	return s == "" || fence(s) != "" || heading.MatchString(s) || rule(s) || list.MatchString(s) || strings.HasPrefix(s, "&gt;") || tableStart(lines, i)
}

func blocks(lines []string, depth int) string {
	var out strings.Builder
	for i := 0; i < len(lines); {
		s := strings.TrimSpace(lines[i])
		switch {
		case s == "":
			i++
		case fence(s) != "":
			marker := fence(s)
			i++
			out.WriteString("<pre><code>")
			for i < len(lines) {
				end := strings.TrimSpace(lines[i])
				if strings.HasPrefix(end, marker) && strings.Trim(end, marker[:1]) == "" {
					i++
					break
				}
				out.WriteString(lines[i])
				out.WriteByte('\n')
				i++
			}
			out.WriteString("</code></pre>\n")
		case heading.MatchString(s):
			m := heading.FindStringSubmatch(s)
			tag := "h" + string(rune('0'+len(m[1])))
			out.WriteString("<" + tag + ">" + inline(m[2], 0, true) + "</" + tag + ">\n")
			i++
		case rule(s):
			out.WriteString("<hr>\n")
			i++
		case strings.HasPrefix(s, "&gt;") && depth < 16:
			var quoted []string
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "&gt;") {
				q := strings.TrimPrefix(strings.TrimSpace(lines[i]), "&gt;")
				quoted = append(quoted, strings.TrimPrefix(q, " "))
				i++
			}
			out.WriteString("<blockquote>\n" + blocks(quoted, depth+1) + "</blockquote>\n")
		case tableStart(lines, i):
			head := cells(lines[i])
			out.WriteString("<table><thead><tr>")
			for _, cell := range head {
				out.WriteString("<th>" + inline(cell, 0, true) + "</th>")
			}
			out.WriteString("</tr></thead><tbody>")
			i += 2
			for i < len(lines) && strings.Contains(lines[i], "|") && strings.TrimSpace(lines[i]) != "" {
				row := cells(lines[i])
				out.WriteString("<tr>")
				for col := range head {
					value := ""
					if col < len(row) {
						value = row[col]
					}
					out.WriteString("<td>" + inline(value, 0, true) + "</td>")
				}
				out.WriteString("</tr>")
				i++
			}
			out.WriteString("</tbody></table>\n")
		case list.MatchString(s):
			ordered := list.FindStringSubmatch(s)[2] != ""
			tag := "ul"
			if ordered {
				tag = "ol"
			}
			out.WriteString("<" + tag + ">\n")
			for i < len(lines) {
				m := list.FindStringSubmatch(strings.TrimSpace(lines[i]))
				if m == nil || (m[2] != "") != ordered {
					break
				}
				out.WriteString("<li>" + inline(m[3], 0, true) + "</li>\n")
				i++
			}
			out.WriteString("</" + tag + ">\n")
		default:
			start := i
			i++
			for i < len(lines) && !special(lines, i) {
				i++
			}
			out.WriteString("<p>" + inline(strings.Join(lines[start:i], "\n"), 0, true) + "</p>\n")
		}
	}
	return out.String()
}

func safeURL(s string) bool {
	s = html.UnescapeString(s)
	if strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != ""
}

// inline consumes escaped text. Delimited code is opaque; generated HTML is
// never fed back through this parser. Recursion is bounded for hostile nesting.
func inline(s string, depth int, links bool) string {
	if depth >= 16 {
		return s
	}
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) && strings.ContainsRune("\\`*_[]()!|", rune(s[i+1])) {
			out.WriteByte(s[i+1])
			i += 2
			continue
		}
		if s[i] == '`' {
			n := 1
			for i+n < len(s) && s[i+n] == '`' {
				n++
			}
			marker := s[i : i+n]
			if end := codeEnd(s, i+n, n); end >= 0 {
				out.WriteString("<code>" + s[i+n:end] + "</code>")
				i = end + n
				continue
			}
			out.WriteString(marker)
			i += n
			continue
		}
		if links && s[i] == '[' {
			if end := strings.Index(s[i+1:], "]("); end >= 0 {
				end += i + 1
				if close := linkEnd(s, end+2); close >= 0 {
					dest := s[end+2 : close]
					if safeURL(dest) {
						out.WriteString(`<a href="` + html.EscapeString(html.UnescapeString(dest)) + `" rel="noopener noreferrer" target="_blank">` + inline(s[i+1:end], depth+1, false) + "</a>")
					} else {
						out.WriteString(s[i : close+1])
					}
					i = close + 1
					continue
				}
			}
			// No complete link remains. Avoid rescanning the same suffix for
			// every '[' in a large malformed input.
			links = false
		}
		if s[i] == '*' || s[i] == '_' {
			marker, tag := s[i:i+1], "em"
			if i+1 < len(s) && s[i+1] == s[i] {
				marker, tag = s[i:i+2], "strong"
			}
			if end := strings.Index(s[i+len(marker):], marker); end > 0 {
				end += i + len(marker)
				out.WriteString("<" + tag + ">" + inline(s[i+len(marker):end], depth+1, links) + "</" + tag + ">")
				i = end + len(marker)
				continue
			}
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

func codeEnd(s string, start, width int) int {
	for i := start; i < len(s); i++ {
		if s[i] != '`' {
			continue
		}
		end := i
		for end < len(s) && s[end] == '`' {
			end++
		}
		if end-i == width {
			return i
		}
		i = end - 1
	}
	return -1
}

func linkEnd(s string, start int) int {
	depth := 0
	for i := start; i < len(s); i++ {
		if s[i] == '(' {
			depth++
		}
		if s[i] == ')' {
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}
