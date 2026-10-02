// Package web embeds the built UI (run `bun run build` in web/ first).
package web

import (
	"embed"
	"io/fs"
)

//go:embed dist
var dist embed.FS

// Dist is the built UI rooted at dist/.
func Dist() fs.FS {
	sub, _ := fs.Sub(dist, "dist")
	return sub
}
