// Package markdown renders a subset of GitHub-flavored Markdown to an ANSI
// terminal without any third-party dependencies. It intentionally stays small:
// the AI Shell project keeps its dependency surface minimal (stdlib + yaml.v3
// only), so a full CommonMark engine is out of scope. It covers the constructs
// models actually emit in chat answers: headings, bold/italic, inline code,
// fenced code blocks, links, unordered/ordered lists, block quotes, and
// horizontal rules.
package markdown

import (
	"fmt"
	"io"
	"strings"
)

// ANSI SGR sequences used for styling. Each style resets with reset so that
// partially styled lines don't bleed into following output.
const (
	reset     = "\033[0m"
	boldOn    = "\033[1m"
	boldOff   = "\033[22m"
	dim       = "\033[2m"
	italicOn  = "\033[3m"
	italicOff = "\033[23m"
	underline = "\033[4m"
	cyan      = "\033[36m"
	green     = "\033[32m"
	blue      = "\033[34m"
	yellow    = "\033[33m"
)

func styled(style, text string) string {
	if text == "" {
		return ""
	}
	return style + text + reset
}

// Render writes markdown to w as ANSI-styled terminal text. It returns after
// emitting a trailing blank line so following prompts sit below the answer.
func Render(w io.Writer, md string) {
	r := &renderer{w: w}
	r.block(strings.TrimRight(md, "\n"))
	io.WriteString(w, "\n")
}

type renderer struct {
	w io.Writer
}

// block renders a multi-line markdown document, grouping consecutive lines
// into blocks (paragraph, list, code fence, quote, heading, rule).
func (r *renderer) block(md string) {
	lines := strings.Split(md, "\n")
	i := 0
	for i < len(lines) {
		line := lines[i]

		switch {
		case isBlank(line):
			i++

		case isFenceStart(line):
			i = r.codeBlock(lines, i)

		case isHeading(line):
			r.line(boldOn + cyan + renderInline(headingText(line)) + reset + "\n")
			i++

		case isHR(line):
			r.line(styled(dim, "────────────────────────────────────────") + "\n")
			i++

		case isBlockquote(line):
			i = r.blockquote(lines, i)

		case isListItem(line):
			i = r.list(lines, i)

		default:
			// Gather a paragraph (consecutive non-blank, non-special lines).
			var para []string
			for i < len(lines) && !isBlank(lines[i]) && !isSpecial(lines[i]) {
				para = append(para, lines[i])
				i++
			}
			r.line(renderInline(strings.Join(para, " ")) + "\n")
		}
	}
}

func (r *renderer) line(s string) {
	io.WriteString(r.w, s)
}

// codeBlock consumes a fenced code block (``` ... ```) and prints it dimmed
// with a left rule.
func (r *renderer) codeBlock(lines []string, start int) int {
	i := start + 1
	var body []string
	for i < len(lines) && !isFenceEnd(lines[i]) {
		body = append(body, lines[i])
		i++
	}
	if i < len(lines) {
		i++ // consume closing fence
	}
	r.line(styled(dim, "┌──────────────────────────────\n"))
	for _, l := range body {
		io.WriteString(r.w, styled(dim, "│ ")+styled(green, l)+"\n")
	}
	r.line(styled(dim, "└──────────────────────────────\n"))
	return i
}

// blockquote consumes consecutive `>` lines.
func (r *renderer) blockquote(lines []string, start int) int {
	i := start
	var body []string
	for i < len(lines) && isBlockquote(lines[i]) {
		body = append(body, strings.TrimSpace(strings.TrimPrefix(lines[i], ">")))
		i++
	}
	for _, l := range body {
		r.line(styled(dim, "│ ")+styled(dim, renderInline(l))+"\n")
	}
	return i
}

// list consumes a run of list items (the run may mix ul/ol but we keep the
// marker each item declares) and renders them with hanging indents.
func (r *renderer) list(lines []string, start int) int {
	i := start
	order := 0
	for i < len(lines) && isListItem(lines[i]) {
		indent, marker, text := parseListItem(lines[i])
		if marker == "" {
			order++
			r.line(indent + fmt.Sprintf("%d. ", order) + renderInline(text) + "\n")
		} else {
			r.line(indent + styled(yellow, "• ")+renderInline(text)+"\n")
		}
		i++
	}
	return i
}

// renderInline applies inline markdown styling (bold, italic, code, links) to a
// single line of text. It scans left-to-right and resolves the most specific
// construct at each position.
func renderInline(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		// Inline code span: `code`
		if s[i] == '`' {
			if end := strings.IndexByte(s[i+1:], '`'); end >= 0 {
				b.WriteString(styled(green, s[i+1:i+1+end]))
				i = i + 1 + end + 1
				continue
			}
		}
		// Bold: **text**
		if i+1 < len(s) && s[i] == '*' && s[i+1] == '*' {
			if end := strings.Index(s[i+2:], "**"); end >= 0 {
				b.WriteString(boldOn + renderInline(s[i+2:i+2+end]) + boldOff)
				i = i + 2 + end + 2
				continue
			}
		}
		// Italic: *text*
		if s[i] == '*' {
			if end := strings.IndexByte(s[i+1:], '*'); end >= 0 {
				b.WriteString(italicOn + s[i+1:i+1+end] + italicOff)
				i = i + 1 + end + 1
				continue
			}
		}
		// Link: [text](url)
		if s[i] == '[' {
			close := strings.IndexByte(s[i:], ']')
			if close > 0 {
				text := s[i+1 : i+close]
				j := i + close + 1
				if j < len(s) && s[j] == '(' {
					end := strings.IndexByte(s[j:], ')')
					if end >= 0 {
						b.WriteString(styled(blue+underline, text))
						i = j + end + 1
						continue
					}
				}
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func isBlank(s string) bool {
	return strings.TrimSpace(s) == ""
}

func isFenceStart(s string) bool {
	t := strings.TrimSpace(s)
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

func isFenceEnd(s string) bool {
	return strings.TrimSpace(s) == "```" || strings.TrimSpace(s) == "~~~"
}

func isHeading(s string) bool {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "#") {
		return false
	}
	// Require a space after the hashes so "#tag" doesn't count.
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	return n >= 1 && n <= 6 && (n == len(t) || t[n] == ' ')
}

func headingText(s string) string {
	t := strings.TrimSpace(s)
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	return strings.TrimSpace(t[n:])
}

func isHR(s string) bool {
	t := strings.TrimSpace(s)
	if t == "---" || t == "***" || t == "___" {
		return true
	}
	// Three or more of the same dash, possibly spaced.
	if len(t) >= 3 && (strings.Count(t, "-") >= 3) && strings.ReplaceAll(t, "-", "") == "" {
		return true
	}
	if len(t) >= 3 && (strings.Count(t, "*") >= 3) && strings.ReplaceAll(t, "*", "") == "" {
		return true
	}
	return false
}

func isBlockquote(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), ">")
}

func isListItem(s string) bool {
	_, _, _ = parseListItem(s)
	// parseListItem returns ("", "", "") when not a list item.
	t := strings.TrimSpace(s)
	if len(t) == 0 {
		return false
	}
	if t[0] == '-' || t[0] == '*' || t[0] == '+' {
		return len(t) >= 2 && t[1] == ' '
	}
	if idx := strings.IndexByte(t, '.'); idx > 0 {
		num := t[:idx]
		for _, c := range num {
			if c < '0' || c > '9' {
				return false
			}
		}
		return len(num) >= 1 && idx+1 < len(t) && t[idx+1] == ' '
	}
	return false
}

// parseListItem returns the leading indentation, the ordered-list number
// string ("" for unordered), and the item text. When s is not a list item the
// returned strings are all empty.
func parseListItem(s string) (indent, marker, text string) {
	t := s
	indent = ""
	// Capture leading spaces as indentation.
	if sp := strings.IndexFunc(t, func(r rune) bool { return r != ' ' }); sp > 0 {
		indent = strings.Repeat("  ", sp/2)
		t = t[sp:]
	}
	if len(t) == 0 {
		return "", "", ""
	}
	if t[0] == '-' || t[0] == '*' || t[0] == '+' {
		if len(t) < 2 || t[1] != ' ' {
			return "", "", ""
		}
		return indent, "•", strings.TrimSpace(t[2:])
	}
	if idx := strings.IndexByte(t, '.'); idx > 0 {
		num := t[:idx]
		for _, c := range num {
			if c < '0' || c > '9' {
				return "", "", ""
			}
		}
		if idx+1 >= len(t) || t[idx+1] != ' ' {
			return "", "", ""
		}
		return indent, num, strings.TrimSpace(t[idx+2:])
	}
	return "", "", ""
}

// isSpecial reports whether a line begins a non-paragraph block so the
// paragraph collector stops before it.
func isSpecial(s string) bool {
	return isFenceStart(s) || isHeading(s) || isHR(s) || isBlockquote(s) || isListItem(s)
}
