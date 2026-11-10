package workspace

import (
	"bytes"

	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/extension"
	"github.com/yuin/goldmark/v2/parser"
	"github.com/yuin/goldmark/v2/renderer/html"
)

// Lab documents are parsed and rendered with GitHub Flavored Markdown.
// goldmark's default renderer is the safe one: raw HTML in the source is
// replaced by a comment, and links or images with javascript:, vbscript:,
// file: or data: URLs lose their destination. html.WithUnsafe must never be
// added here; the page inserts the result as HTML.
var (
	markdownParser   = parser.New(parser.WithExtensions(extension.NewGFMParser()))
	markdownRenderer = html.New(html.WithExtensions(extension.NewGFMHTMLRenderer()))
)

func renderMarkdown(src string) (string, error) {
	source := []byte(src)
	return render(source, markdownParser.Parse(source))
}

// renderQuestion renders the task text without its leading top-level heading
// ("# Exam Question: <lab>"): the task panel already says what it is, so the
// heading only repeats the lab name.
func renderQuestion(src string) (string, error) {
	source := []byte(src)
	doc := markdownParser.Parse(source)
	if h, ok := doc.FirstChild().(*ast.Heading); ok && h.Level == 1 {
		doc.RemoveChild(h)
	}
	return render(source, doc)
}

func render(source []byte, doc ast.Node) (string, error) {
	var buf bytes.Buffer
	if err := markdownRenderer.Render(&buf, source, doc); err != nil {
		return "", err
	}
	return buf.String(), nil
}
