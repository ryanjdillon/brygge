//go:build noembed

package backend

import (
	"io/fs"
	"testing/fstest"
)

// FrontendFS returns an empty filesystem when built without embedded frontend.
func FrontendFS() (fs.FS, error) {
	return fstest.MapFS{}, nil
}
