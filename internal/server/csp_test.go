package server

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// TestInlineScriptHashes covers the CSP hash derivation for index.html. The
// SPA applies the saved theme from an inline script before first paint;
// script-src 'self' silently blocked it on every page load until the hash
// was added, so the anti-flash logic never ran.
func TestInlineScriptHashes(t *testing.T) {
	body := "\n  var t = localStorage.getItem(\"windlass-theme\");\n"
	index := `<!doctype html><html><head>` +
		`<script>` + body + `</script>` +
		`<script type="module" crossorigin src="/assets/index-abc.js"></script>` +
		`</head><body></body></html>`

	got := inlineScriptHashes(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(index)},
	})
	sum := sha256.Sum256([]byte(body))
	want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"

	// Exactly one hash: the inline script. The external one is covered by
	// 'self' and has no hashable body.
	if len(got) != 1 || got[0] != want {
		t.Errorf("hashes = %v, want [%s]", got, want)
	}
}

func TestInlineScriptHashesNoIndex(t *testing.T) {
	if got := inlineScriptHashes(fstest.MapFS{}); got != nil {
		t.Errorf("hashes = %v, want nil when index.html is missing", got)
	}
}

// TestCSPCoversServedIndex is the regression that matters: whatever inline
// scripts the real served index.html contains must be permitted by the CSP
// the server actually sends, so the two cannot drift apart.
func TestCSPCoversServedIndex(t *testing.T) {
	e := newTestEnv(t)

	rec := e.do(t, "GET", "/", nil)
	csp := rec.Header().Get("Content-Security-Policy")
	html := rec.Body.String()

	for _, m := range inlineScriptRE.FindAllStringSubmatch(html, -1) {
		open := m[0][:len(m[0])-len(m[1])-len("</script>")]
		if strings.Contains(open, "src=") || strings.TrimSpace(m[1]) == "" {
			continue
		}
		sum := sha256.Sum256([]byte(m[1]))
		want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		if !strings.Contains(csp, want) {
			t.Errorf("inline script in index.html is not allowed by CSP\nscript: %q\nwant source: %s\ncsp: %s",
				m[1], want, csp)
		}
	}

	// The policy must still be restrictive: no blanket inline execution.
	if strings.Contains(csp, "'unsafe-inline'") && regexp.MustCompile(`script-src[^;]*'unsafe-inline'`).MatchString(csp) {
		t.Errorf("script-src allows unsafe-inline: %s", csp)
	}
}
