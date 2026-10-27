package staging

import (
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
)

func TestDefaultHostname(t *testing.T) {
	cases := map[string]string{
		"example.com":        "staging.example.com",
		"example.co.uk":      "staging.example.co.uk",
		"app.example.com":    "staging-app.example.com",
		"shop.example.co.uk": "staging-shop.example.co.uk",
		"a.b.example.com":    "staging-a.b.example.com",
		"localhost":          "staging.localhost",
		"example.uk":         "staging.example.uk",
	}
	for in, want := range cases {
		if got := DefaultHostname(in); got != want {
			t.Errorf("DefaultHostname(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[uint64]string{0: "0 B", 1023: "1023 B", 1 << 20: "1.0 MiB", 3 << 30: "3.0 GiB"}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestPromoteBlocked(t *testing.T) {
	staged := &DeploymentRef{Number: 2, Status: "succeeded", id: 12}
	cases := []struct {
		name string
		st   Status
		want string
	}{
		{"nothing to promote", Status{}, "staging has no succeeded deployment yet"},
		{"ready", Status{Promotable: staged, LiveCurrent: &DeploymentRef{Number: 1, id: 5}}, ""},
		{"staging deploying", Status{Promotable: staged, Latest: &DeploymentRef{Number: 3, Status: "building"}},
			"staging #3 is still deploying"},
		{"already promoted without Git", Status{Promotable: staged, LiveCurrent: &DeploymentRef{Number: 4, id: 20, promotedFrom: 12}},
			"live already runs staging #2"},
		{"live deployed the same commit", Status{
			Promotable:  &DeploymentRef{Number: 2, id: 12, Commit: "abc"},
			LiveCurrent: &DeploymentRef{Number: 4, id: 20, Commit: "abc"},
		}, "live already runs this commit"},
	}
	for _, c := range cases {
		if got := promoteBlocked(c.st); got != c.want {
			t.Errorf("%s: promoteBlocked = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestValuesOutsideEnv(t *testing.T) {
	cfg := agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		// From .env through ${STRIPE_SECRET_KEY}: the flagged list covers it.
		"web": {Environment: map[string]string{"STRIPE_SECRET_KEY": "sk_live_x", "PORT": "80"}},
		// From an env_file: same key as .env with another value, and one
		// .env does not have.
		"worker": {Environment: map[string]string{"STRIPE_SECRET_KEY": "sk_live_other", "SMTP_HOST": "mail.example.com"}},
	}}
	got := valuesOutsideEnv(cfg, map[string]string{"STRIPE_SECRET_KEY": "sk_live_x"})
	if len(got) != 1 || got[0].Service != "worker" || !strings.Contains(got[0].Message, "SMTP_HOST, STRIPE_SECRET_KEY") ||
		strings.Contains(got[0].Message, "sk_live") || strings.Contains(got[0].Message, "mail.example.com") {
		t.Fatalf("issues = %+v", got)
	}
}
