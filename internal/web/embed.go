// Package web owns the Vite build output. Run npm ci && npm run build before
// compiling Go; generated files are deliberately not committed.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

func Files() fs.FS {
	root, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	return root
}
