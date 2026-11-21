package api

import "testing"

func TestIsManifestFile(t *testing.T) {
	for _, rel := range []string{".windlass.json", "./.windlass.json", `.\.windlass.json`, `x/../.windlass.json`, ".WINDLASS.json", `\.windlass.json`} {
		if !isManifestFile(rel) {
			t.Errorf("%q not treated as the manifest", rel)
		}
	}
	for _, rel := range []string{"app/.windlass.json", "windlass.json", ".windlass.json.bak"} {
		if isManifestFile(rel) {
			t.Errorf("%q treated as the manifest", rel)
		}
	}
}
