package workspace

import (
	"bytes"

	"github.com/yuin/goldmark/v2"
	"github.com/yuin/goldmark/v2/extension"
)

// markdown renders lab documents. goldmark's default renderer is the safe
// one: raw HTML in the source is replaced by a comment, and links or images
// with javascript:, vbscript:, file: or data: URLs lose their destination.
// html.WithUnsafe must never be added here; the page inserts the result as
// HTML.
var markdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

func renderMarkdown(src string) (string, error) {
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}
