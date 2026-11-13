package local

import "testing"

func TestBuildxTotal(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want int64
		ok   bool
	}{
		// buildx prune prints its table, then this summary line.
		{"ID\tRECLAIMABLE\tSIZE\tLAST ACCESSED\nabc\ttrue\t1.2GB\t3 weeks ago\nTotal:\t20.9GB\n", 209e8, true},
		{"Total:\t0B\n", 0, true},
		{"Total:\t512kB", 512e3, true},
		{"Total: 1.5 MB\n", 15e5, true},
		{"nothing useful", 0, false},
		{"Total:\tlots", 0, false},
		{"Total:\t3XB", 0, false},
	} {
		got, ok := buildxTotal(tc.out)
		if got != tc.want || ok != tc.ok {
			t.Errorf("buildxTotal(%q) = %d, %v; want %d, %v", tc.out, got, ok, tc.want, tc.ok)
		}
	}
}

func TestBuilderStateVolume(t *testing.T) {
	for name, want := range map[string]bool{
		"buildx_buildkit_default_state":  true,
		"buildx_buildkit_builder0_state": true,
		"buildx_buildkit_default":        false,
		"shop_data_state":                false,
		"pg_buildx_buildkit_x_state":     false,
	} {
		if got := builderStateVolume(name); got != want {
			t.Errorf("builderStateVolume(%q) = %v", name, got)
		}
	}
}
