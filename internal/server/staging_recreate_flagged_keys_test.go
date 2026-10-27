package server

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestRecreateStatusFlagsStagingKeysWithoutValues(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	old := prepareInterruptedStaging(t, e, cookie)
	ctx := context.Background()
	if err := e.api.Projects.SetEnv(ctx, "app", map[string]string{"SMTP_PASSWORD": "live-only-value"}); err != nil {
		t.Fatal(err)
	}
	if err := e.api.Projects.SetEnv(ctx, old.Name, map[string]string{
		"VALUE": "keep-staging", "SMTP_HOST": "staging-mail.invalid", "STRIPE_SECRET_KEY": "staging-test-key",
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app", old.Name} {
		rec := e.do(t, http.MethodGet, "/api/v1/projects/"+name+"/staging", nil, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("status for %s = %d: %s", name, rec.Code, rec.Body.String())
		}
		st := decode[struct {
			CanRecreate bool     `json:"can_recreate"`
			Flagged     []string `json:"recreate_flagged_keys"`
		}](t, rec.Body.String())
		if !st.CanRecreate || !reflect.DeepEqual(st.Flagged, []string{"SMTP_HOST", "STRIPE_SECRET_KEY"}) {
			t.Errorf("status for %s flags = %v, recreatable=%v", name, st.Flagged, st.CanRecreate)
		}
		for _, value := range []string{"live-only-value", "keep-staging", "staging-mail.invalid", "staging-test-key"} {
			if strings.Contains(rec.Body.String(), value) {
				t.Errorf("status for %s exposed an environment value", name)
			}
		}
	}
	if _, err := e.sqlDB.Exec(`UPDATE deployments SET error = 'application failed' WHERE project_id = ?`, old.ID); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodGet, "/api/v1/projects/app/staging", nil, cookie)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "recreate_flagged_keys") {
		t.Fatalf("ineligible staging includes recreation-only flags: %d: %s", rec.Code, rec.Body.String())
	}
}
