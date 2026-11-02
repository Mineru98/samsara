// Package webui embeds the React dashboard build (web/, 02 §21.2). The
// directory holds only a placeholder until `pnpm -C web build` writes the
// bundle, so `go build` works with or without a UI build.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:static
var files embed.FS

// FS returns the built assets, or nil when this binary carries no UI build.
func FS() fs.FS {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
