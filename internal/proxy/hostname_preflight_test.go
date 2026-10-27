package proxy

import (
	"context"
	"errors"
	"testing"

	"github.com/windlass-dev/windlass/internal/store/db"
)

func TestRetainedHostnameOnlyExemptsItsOwner(t *testing.T) {
	s, _, q := newTestService(t)
	ctx := context.Background()
	owner, err := q.CreateProject(ctx, db.CreateProjectParams{Name: "app-staging", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := q.CreateProject(ctx, db.CreateProjectParams{Name: "other", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []db.CreateDomainParams{
		{ProjectID: owner.ID, Hostname: "preview.example.com", Service: "web", ContainerPort: 80},
		{ProjectID: other.ID, Hostname: "other.example.com", Service: "web", ContainerPort: 80},
	} {
		if _, err := q.CreateDomain(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.SetSetting(ctx, db.SetSettingParams{Key: panelDomainSettingKey, Value: "panel.example.com"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host  string
		owner int64
		want  error
	}{
		{"preview.example.com", owner.ID, nil},
		{" PREVIEW.example.com ", owner.ID, nil},
		{"preview.example.com", other.ID, ErrConflict},
		{"other.example.com", owner.ID, ErrConflict},
		{"panel.example.com", owner.ID, ErrConflict},
		{"not a hostname", owner.ID, ErrInvalidHostname},
	} {
		if err := s.CheckHostnameForProject(ctx, tc.host, tc.owner); !errors.Is(err, tc.want) {
			t.Errorf("hostname %q for project %d = %v; want %v", tc.host, tc.owner, err, tc.want)
		}
	}
	if err := s.CheckHostname(ctx, "preview.example.com"); !errors.Is(err, ErrConflict) {
		t.Errorf("ordinary create must still reject an occupied name: %v", err)
	}
}
