package markdown

import "strings"

// LooksLikeMarkdown reports whether md is worth rendering as Markdown rather
// than printing verbatim. The client uses this to decide between the raw stream
// (kept for plain prose, and preserved when stdout is piped) and the styled
// terminal rendering. It triggers on at least one block-level construct
// (heading, list, code fence, block quote, horizontal rule) or two or more
// inline constructs (bold, inline code, link), which keeps stray asterisks in
// ordinary prose from being misinterpreted.
func LooksLikeMarkdown(md string) bool {
	strong := 0
	inline := 0
	lines := strings.Split(md, "\n")
	for _, line := range lines {
		switch {
		case isHeading(line), isFenceStart(line), isBlockquote(line), isListItem(line), isHR(line):
			strong++
		}
		if strings.Contains(line, "**") {
			inline++
		}
		if strings.Contains(line, "`") {
			inline++
		}
		if strings.Contains(line, "](") {
			inline++
		}
	}
	return strong >= 1 || inline >= 2
}
