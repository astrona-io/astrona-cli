// Package web embeds the browser workspace's static frontend.
//
// The built frontend (Vite output) lands in dist/ during `just build` and in
// the release workflow; it is not committed. The repository commits only
// dist/index.html, a placeholder page, so `go build` and `go test` work
// without Node. Release builds must fail when IsPlaceholder reports true.
package web

import (
	"bytes"
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// placeholderMarker is present only in the committed placeholder page.
var placeholderMarker = []byte(`<meta name="astrona-workspace-placeholder"`)

// FS returns the frontend rooted at dist/, with index.html at its top.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is a fixed, valid embed path
	}
	return sub
}

// IsPlaceholder reports whether the embedded frontend is the committed
// placeholder rather than built assets.
func IsPlaceholder() bool {
	index, err := fs.ReadFile(FS(), "index.html")
	return err != nil || bytes.Contains(index, placeholderMarker)
}
