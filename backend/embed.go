//go:build !noembed

package backend

import (
	"embed"
	"io/fs"
)

//go:embed internal/web/dist
var frontendFiles embed.FS

// FrontendFS returns the embedded frontend filesystem rooted at internal/web/dist.
func FrontendFS() (fs.FS, error) {
	return fs.Sub(frontendFiles, "internal/web/dist")
}
