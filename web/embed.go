// Package webdist embeds the built SPA (web/dist). A committed placeholder
// web/dist/.keep keeps the embed compiling before the first `npm run build`.
//
// Owner: SCAFFOLD-0b.
package webdist

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the SPA files rooted at dist/.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
