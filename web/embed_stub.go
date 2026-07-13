//go:build !embedweb

// Without the embedweb tag (the default for `go build` and `go test`), the
// binary serves a placeholder page instead of the compiled frontend, so the
// Go toolchain never requires Node.
package web

import (
	"io/fs"
	"testing/fstest"
)

const placeholder = `<!doctype html>
<html><head><title>Windlass (dev build)</title></head>
<body style="font-family:system-ui;background:#09090b;color:#e4e4e7;display:flex;align-items:center;justify-content:center;height:100vh;margin:0">
<div style="text-align:center">
<h1>Windlass</h1>
<p>This binary was built without the embedded frontend.<br>
Run <code>npm run build</code> in web/ and rebuild with <code>-tags embedweb</code>,<br>
or use the Vite dev server (<code>make dev</code>).</p>
</div></body></html>`

func Dist() (fs.FS, error) {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(placeholder)},
	}, nil
}
