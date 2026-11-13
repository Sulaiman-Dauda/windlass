package selfstat

import (
	"runtime"
	"testing"
	"time"
)

func TestRingKeepsTheLastHourOldestFirst(t *testing.T) {
	r := NewRing()
	if s := r.Samples(); s == nil || len(s) != 0 {
		t.Fatalf("empty ring = %v, want an empty non-nil slice", s)
	}
	base := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	for i := range Size + 5 {
		r.Add(Sample{Time: base.Add(time.Duration(i) * time.Minute), RSSBytes: uint64(i)})
	}
	s := r.Samples()
	if len(s) != Size || s[0].RSSBytes != 5 || s[Size-1].RSSBytes != Size+4 {
		t.Fatalf("len %d first %d last %d", len(s), s[0].RSSBytes, s[len(s)-1].RSSBytes)
	}
	for i := 1; i < len(s); i++ {
		if !s[i].Time.After(s[i-1].Time) {
			t.Fatalf("out of order at %d", i)
		}
	}
}

func TestRSSOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("RSS is read from /proc")
	}
	rss, ok := RSS()
	if !ok || rss < 1<<20 || rss > 1<<34 {
		t.Fatalf("rss = %d ok = %v", rss, ok)
	}
}
