package dbtemplates

import (
	"strings"
	"testing"
)

func TestListIncludesDataOnlyTemplate(t *testing.T) {
	keys := map[string]bool{}
	for _, tmpl := range List() {
		keys[tmpl.Key] = true
	}
	for _, want := range []string{"postgres", "redis", "mysql", "mongodb", "valkey"} {
		if !keys[want] {
			t.Errorf("List() missing %q; got %v", want, keys)
		}
	}
}

func TestRenderPostgres(t *testing.T) {
	compose, env, err := Render("postgres", "mydb", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compose, "postgres:17-alpine") {
		t.Errorf("compose missing image:\n%s", compose)
	}
	// hostPort=0 falls back to the template default (5432).
	if !strings.Contains(compose, "127.0.0.1:5432:5432") {
		t.Errorf("compose missing default port:\n%s", compose)
	}
	if env["POSTGRES_DB"] != "mydb" {
		t.Errorf("POSTGRES_DB = %q, want mydb", env["POSTGRES_DB"])
	}
	if env["POSTGRES_USER"] != "windlass" {
		t.Errorf("POSTGRES_USER = %q, want windlass", env["POSTGRES_USER"])
	}
	if len(env["POSTGRES_PASSWORD"]) == 0 {
		t.Error("POSTGRES_PASSWORD not generated")
	}
}

func TestRenderHostPortOverride(t *testing.T) {
	compose, _, err := Render("postgres", "mydb", "", 6000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compose, "127.0.0.1:6000:5432") {
		t.Errorf("compose did not honor host port override:\n%s", compose)
	}
}

// MySQL declares MYSQL_ROOT_PASSWORD and MYSQL_PASSWORD against the same "main"
// secret slot: both must receive one identical generated value.
func TestSharedSecretSlot(t *testing.T) {
	_, env, err := Render("mysql", "shop", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if env["MYSQL_ROOT_PASSWORD"] == "" {
		t.Fatal("root password not generated")
	}
	if env["MYSQL_ROOT_PASSWORD"] != env["MYSQL_PASSWORD"] {
		t.Errorf("shared slot produced different values: root=%q user=%q",
			env["MYSQL_ROOT_PASSWORD"], env["MYSQL_PASSWORD"])
	}
}

func TestRenderPerInstanceSecrets(t *testing.T) {
	_, a, err := Render("postgres", "one", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := Render("postgres", "two", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if a["POSTGRES_PASSWORD"] == b["POSTGRES_PASSWORD"] {
		t.Error("expected a fresh secret per instance")
	}
}

func TestRenderUnknown(t *testing.T) {
	if _, _, err := Render("oracle", "x", "", 0); err == nil {
		t.Error("expected error for unknown template")
	}
}

// App templates carry a Route and inject the domain into env; database
// templates carry neither.
func TestAppTemplateRoute(t *testing.T) {
	kuma, ok := Get("uptime-kuma")
	if !ok {
		t.Fatal("uptime-kuma not in catalog")
	}
	if kuma.Route == nil || kuma.Route.Service != "uptime-kuma" || kuma.Route.ContainerPort != 3001 {
		t.Errorf("unexpected route: %+v", kuma.Route)
	}
	if pg, _ := Get("postgres"); pg.Route != nil {
		t.Errorf("database template should have no route: %+v", pg.Route)
	}
}

func TestRenderInjectsDomainURL(t *testing.T) {
	_, env, err := Render("ghost", "blog", "blog.example.com", 0)
	if err != nil {
		t.Fatal(err)
	}
	if env["GHOST_URL"] != "https://blog.example.com" {
		t.Errorf("GHOST_URL = %q", env["GHOST_URL"])
	}
	// Ghost and its DB must share the same generated user password.
	if env["DB_PASSWORD"] == "" {
		t.Fatal("DB_PASSWORD not generated")
	}
	if env["DB_PASSWORD"] == env["DB_ROOT_PASSWORD"] {
		t.Error("user and root passwords must use distinct secret slots")
	}
}

func TestRenderMissingDomainFails(t *testing.T) {
	if _, _, err := Render("ghost", "blog", "", 0); err == nil {
		t.Error("expected error when a url-injecting template has no domain")
	}
}

// The Tier-1 batch: app templates carry a route, the search backing service
// does not, and all render.
func TestTier1TemplatesLoad(t *testing.T) {
	keys := map[string]bool{}
	for _, tmpl := range List() {
		keys[tmpl.Key] = true
	}
	for _, want := range []string{"pocketbase", "n8n", "meilisearch", "gitea", "minio"} {
		if !keys[want] {
			t.Errorf("List() missing %q", want)
		}
	}
	// MinIO routes its console; the S3 API is published on loopback in compose.
	if mo, _ := Get("minio"); mo.Route == nil || mo.Route.ContainerPort != 9001 {
		t.Errorf("minio route wrong: %+v", mo.Route)
	}
	// Meilisearch is a loopback backing service (no route); n8n is an app.
	if m, _ := Get("meilisearch"); m.Route != nil {
		t.Errorf("meilisearch should have no route: %+v", m.Route)
	}
	if n, _ := Get("n8n"); n.Route == nil || n.Route.ContainerPort != 5678 {
		t.Errorf("n8n route wrong: %+v", n.Route)
	}
}

func TestN8nInjectsDomainAndURL(t *testing.T) {
	_, env, err := Render("n8n", "flows", "n8n.example.com", 0)
	if err != nil {
		t.Fatal(err)
	}
	if env["N8N_HOST"] != "n8n.example.com" {
		t.Errorf("N8N_HOST = %q", env["N8N_HOST"])
	}
	if env["N8N_URL"] != "https://n8n.example.com" {
		t.Errorf("N8N_URL = %q", env["N8N_URL"])
	}
	if env["N8N_ENCRYPTION_KEY"] == "" {
		t.Error("N8N_ENCRYPTION_KEY not generated")
	}
}

func TestMeilisearchRendersLoopbackPort(t *testing.T) {
	compose, env, err := Render("meilisearch", "search", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compose, "127.0.0.1:7700:7700") {
		t.Errorf("meilisearch default port not rendered:\n%s", compose)
	}
	if env["MEILI_MASTER_KEY"] == "" {
		t.Error("MEILI_MASTER_KEY not generated")
	}
}
