package main

import (
	"regexp"
	"strings"

	"astrona/internal/labops"
)

// stripFrontMatter removes a YAML front-matter block — a "---" line, YAML,
// a "---" line — from the very start of a doc, and returns its title, if
// it has one. A "---" anywhere else is a horizontal rule and stays, as does
// a leading block that isn't YAML. Input must be sanitized first.
func stripFrontMatter(src string) (title, body string) {
	meta, body := docFrontMatter(src)
	if t, ok := meta["title"].(string); ok {
		title = labops.SanitizeLine(strings.TrimSpace(t))
	}
	return title, body
}

func docFrontMatter(src string) (map[string]any, string) { return labops.DocFrontMatter(src) }

var (
	mdHeading    = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	mdInlineCode = regexp.MustCompile("`([^`]+)`")
	mdBold       = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdLink       = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	mdBullet     = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
)

// renderMarkdown is a deliberately small terminal renderer for lab docs —
// headings, fenced code, quotes, bullets, inline code, bold and links —
// rather than a full Markdown engine with its dependency tree. With color
// off it returns the (sanitized) source unchanged, which is already
// readable. Input must be sanitized first.
func renderMarkdown(src string, color bool) string {
	if !color {
		return src
	}
	style := func(code, s string) string { return code + s + ansiReset }

	var out strings.Builder
	emit := func(parts ...string) {
		for _, p := range parts {
			out.WriteString(p)
		}
		out.WriteByte('\n')
	}
	inCode := false
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			emit("    ", style(ansiCyan, line))
			continue
		}

		switch {
		case mdHeading.MatchString(line):
			m := mdHeading.FindStringSubmatch(line)
			text := inline(m[2], style)
			if len(m[1]) == 1 {
				emit(style(ansiBold+"\x1b[4m", text))
			} else {
				emit(style(ansiBold, text))
			}
		case strings.HasPrefix(trimmed, ">"):
			emit(style("\x1b[2m", "│ "+inline(strings.TrimSpace(strings.TrimPrefix(trimmed, ">")), style)))
		case trimmed == "---" || trimmed == "***":
			emit(style("\x1b[2m", strings.Repeat("─", 40)))
		case mdBullet.MatchString(line):
			m := mdBullet.FindStringSubmatch(line)
			emit(m[1], "• ", inline(m[2], style))
		default:
			emit(inline(line, style))
		}
	}
	return strings.TrimRight(out.String(), "\n") + "\n"
}

// inline styles links first: once code/bold styling has inserted escape
// sequences, their "[" would confuse the link pattern.
func inline(s string, style func(code, s string) string) string {
	s = mdLink.ReplaceAllString(s, "$1 \x1b[2m($2)"+ansiReset)
	s = mdInlineCode.ReplaceAllStringFunc(s, func(m string) string {
		return style(ansiCyan, strings.Trim(m, "`"))
	})
	s = mdBold.ReplaceAllStringFunc(s, func(m string) string {
		return style(ansiBold, strings.Trim(m, "*"))
	})
	return s
}
