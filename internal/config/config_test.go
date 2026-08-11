package config

import "testing"

func TestDefaultUpdateRepositoryMatchesReleasePublisher(t *testing.T) {
	t.Setenv("WINDLASS_UPDATE_REPO", "")
	t.Setenv("WINDLASS_LOG_LEVEL", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpdateRepo != "Sulaiman-Dauda/windlass" {
		t.Fatalf("UpdateRepo = %q", cfg.UpdateRepo)
	}
}
