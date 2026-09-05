package local

import (
	"encoding/json"
	"testing"
)

// Compose is inconsistent about which resource numbers it quotes: cpus comes
// back as a JSON number and mem_limit as a string. Parsing has to accept both
// shapes for either field, or a valid compose file fails to deploy.
func TestComposeConfigAcceptsQuotedAndBareLimits(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		mem     int64
		cpus    float64
	}{
		{"compose emits mem_limit quoted", `{"services":{"api":{"mem_limit":"268435456","cpus":0.5}}}`, 268435456, 0.5},
		{"both bare", `{"services":{"api":{"mem_limit":268435456,"cpus":0.5}}}`, 268435456, 0.5},
		{"both quoted", `{"services":{"api":{"mem_limit":"268435456","cpus":"0.5"}}}`, 268435456, 0.5},
		{"limits absent", `{"services":{"api":{"image":"nginx"}}}`, 0, 0},
		{"empty string", `{"services":{"api":{"mem_limit":""}}}`, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg composeConfig
			if err := json.Unmarshal([]byte(tc.payload), &cfg); err != nil {
				t.Fatalf("parse compose config: %v", err)
			}
			svc := cfg.Services["api"]
			if got := int64(svc.MemLimit); got != tc.mem {
				t.Errorf("mem_limit = %d, want %d", got, tc.mem)
			}
			if got := float64(svc.CPUs); got != tc.cpus {
				t.Errorf("cpus = %v, want %v", got, tc.cpus)
			}
		})
	}
}
