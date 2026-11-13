package local

import (
	"strings"
	"testing"
	"time"
)

func TestSplitLogTime(t *testing.T) {
	at, text := splitLogTime("2026-11-13T10:00:02.123456789Z GET /health 200")
	if !at.Equal(time.Date(2026, 11, 13, 10, 0, 2, 123456789, time.UTC)) || text != "GET /health 200" {
		t.Errorf("got %v %q", at, text)
	}
	if _, text := splitLogTime("2026-11-13T10:00:02Z"); text != "" {
		t.Errorf("empty line text = %q", text)
	}
	// No stamp: the line is kept whole, timed now.
	before := time.Now().Add(-time.Second)
	at, text = splitLogTime("plain line")
	if text != "plain line" || at.Before(before) {
		t.Errorf("got %v %q", at, text)
	}
}

func TestParseConfigHashes(t *testing.T) {
	got := parseConfigHashes("web 3f1a\ndb 9c2e\n\nwarning: something odd here\n")
	if len(got) != 2 || got["web"] != "3f1a" || got["db"] != "9c2e" {
		t.Fatalf("got %v", got)
	}
}

func TestAnonymousVolume(t *testing.T) {
	if !anonymousVolume(strings.Repeat("3f", 32)) {
		t.Error("a 64-hex name is anonymous")
	}
	for _, named := range []string{"shop_pgdata", strings.Repeat("3F", 32), strings.Repeat("a", 63)} {
		if anonymousVolume(named) {
			t.Errorf("%q is not anonymous", named)
		}
	}
}
