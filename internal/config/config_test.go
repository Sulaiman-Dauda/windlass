package config

import (
	"os"
	"testing"
)

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

// Compose, Git and plugins inherit the process environment, so the update
// token must be gone from it once read.
func TestLoadKeepsUpdateTokenOutOfChildEnvironments(t *testing.T) {
	t.Setenv("WINDLASS_UPDATE_TOKEN", "test-not-a-token")
	t.Setenv("WINDLASS_LOG_LEVEL", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpdateToken != "test-not-a-token" {
		t.Errorf("UpdateToken = %q", cfg.UpdateToken)
	}
	if _, ok := os.LookupEnv("WINDLASS_UPDATE_TOKEN"); ok {
		t.Error("WINDLASS_UPDATE_TOKEN is still in the environment")
	}
}
