package api

import (
	"strings"
	"testing"
)

// A quoted value can span lines (a PEM key), and continuation lines often
// contain "=", so masking line by line on "=" leaks them.
func TestMaskEnvFileMultiline(t *testing.T) {
	in := "A=1\nKEY=\"-----BEGIN KEY-----\nMIIBsekret==\n-----END KEY-----\"\nEMPTY=\nQ=''\n# note\nstray-sekret\nB='x'\n"
	got := maskEnvFile(in)
	for _, leak := range []string{"MIIB", "BEGIN", "END KEY", "sekret"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q survived masking:\n%s", leak, got)
		}
	}
	for _, kept := range []string{"A=", "KEY=", "EMPTY=\n", "Q=''", "# note", "B="} {
		if !strings.Contains(got, kept) {
			t.Errorf("lost %q:\n%s", kept, got)
		}
	}
}

// A commented-out assignment is usually an old credential, and a YAML-style
// "KEY: value" line has no "=" before the secret.
func TestMaskEnvFileCommentsAndYAMLStyle(t *testing.T) {
	got := maskEnvFile("# DATABASE_URL=postgres://u:oldsekret@db/x\n# a note\nDATABASE_URL: postgres://u:sekret@db/x?sslmode=require\nK=`one\nMIIB==sekret\n`\nexport A=sekret\n")
	for _, leak := range []string{"sekret", "MIIB"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q survived masking:\n%s", leak, got)
		}
	}
	for _, kept := range []string{"# DATABASE_URL=", "# a note", "export A="} {
		if !strings.Contains(got, kept) {
			t.Errorf("lost %q:\n%s", kept, got)
		}
	}
}
