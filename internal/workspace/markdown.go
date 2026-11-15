package workspace

import (
	"bytes"
	stdhtml "html"
	"regexp"
	"strings"

	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/extension"
	"github.com/yuin/goldmark/v2/parser"
	"github.com/yuin/goldmark/v2/renderer/html"
	"github.com/yuin/goldmark/v2/text"
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

// renderMarkdownImages renders src like renderMarkdown, with the images
// resolve knows pointed at the URL it returns: Markdown images, and an
// <img> tag written as HTML, which becomes an ordinary image (its source
// and alt text only). Everything else is rendered exactly as renderMarkdown
// does: an image resolve does not know keeps its source, and any other raw
// HTML is still dropped. Code blocks and code spans are never touched.
func renderMarkdownImages(src string, resolve func(dest string) (string, bool)) (string, error) {
	if resolve == nil {
		return renderMarkdown(src)
	}
	source := []byte(src)
	doc := markdownParser.Parse(source)
	type swap struct{ old, new ast.Node }
	var swaps []swap
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Image:
			if u, ok := resolve(n.Destination.Value(source)); ok {
				n.Destination = text.NewSingleLineValueFromString(u, nil)
			}
			return ast.WalkSkipChildren, nil
		case *ast.RawHTML:
			if img := htmlImage(n.Value.Str(source), resolve); img != nil {
				swaps = append(swaps, swap{n, img})
			}
		case *ast.HTMLBlock:
			if img := htmlImage(strings.TrimSpace(n.Value.Str(source)), resolve); img != nil {
				p := ast.NewParagraph()
				p.AppendChild(img)
				swaps = append(swaps, swap{n, p})
			}
		}
		return ast.WalkContinue, nil
	})
	for _, s := range swaps {
		if parent := s.old.Parent(); parent != nil {
			parent.ReplaceChild(s.old, s.new)
		}
	}
	return render(source, doc)
}

var (
	htmlImageTag  = regexp.MustCompile(`(?is)^<img(\s[^<>]*?)/?>$`)
	htmlImageAttr = regexp.MustCompile(`(?is)\s(src|alt)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>` + "`" + `]+))`)
)

// htmlImage is the image node for tag when it is a single <img> whose src
// resolve knows; nil otherwise. Only the resolved URL and the alt text are
// taken from the tag.
func htmlImage(tag string, resolve func(dest string) (string, bool)) *ast.Image {
	m := htmlImageTag.FindStringSubmatch(tag)
	if m == nil {
		return nil
	}
	attrs := map[string]string{}
	for _, a := range htmlImageAttr.FindAllStringSubmatch(m[1], -1) {
		name := strings.ToLower(a[1])
		if _, seen := attrs[name]; !seen {
			attrs[name] = stdhtml.UnescapeString(a[2] + a[3] + a[4])
		}
	}
	u, ok := resolve(strings.TrimSpace(attrs["src"]))
	if !ok {
		return nil
	}
	img := ast.NewImage(text.NewSingleLineValueFromString(u, nil))
	if alt := strings.Join(strings.Fields(attrs["alt"]), " "); alt != "" {
		img.AppendChild(ast.NewText(text.NewSingleLineValueFromString(alt, nil)))
	}
	return img
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
