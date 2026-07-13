//go:build embedweb

// The production build embeds the compiled frontend. Build with:
//
//	npm run build && go build -tags embedweb ./cmd/windlass
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

func Dist() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}
