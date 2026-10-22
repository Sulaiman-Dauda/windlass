package api

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestMaskComposeFile(t *testing.T) {
	in := `x-db: &db
  DB_PASSWORD: anchorsekret
x-token: &tok tokensekret

services:
  app:
    image: ghcr.io/acme/app:1 # pinned
    environment:
      <<: *db
      API_KEY: mapsekret # rotate monthly
      QUOTED: "quoted sekret"
      FROM_ENV: ${API_KEY}
      EMPTY:
      ALIASED: *tok
      PEM: |
        -----BEGIN KEY-----
        pemsekret
        -----END KEY-----
    build:
      context: .
      args:
        - NPM_TOKEN=argsekret
        - PLAIN_PASSTHROUGH
        - REF=$HOME
  worker:
    image: redis:7
    environment: [ "FLOW=flowsekret", "B=c" ]
  db:
    image: postgres:16
    environment:
      - POSTGRES_PASSWORD=listsekret
      - 'SINGLE=quotedlist sekret'
configs:
  conf:
    content: |
      password = configsekret
`
	out := maskComposeFile(in)
	if strings.Contains(out, "sekret") {
		t.Errorf("a value survived:\n%s", out)
	}
	for _, keep := range []string{"ghcr.io/acme/app:1 # pinned", "API_KEY:", "FROM_ENV: ${API_KEY}", "EMPTY:",
		"- PLAIN_PASSTHROUGH", "- REF=$HOME", "NPM_TOKEN=********", "POSTGRES_PASSWORD=********",
		"FLOW=********", "&tok", "\n      <<: *db\n", "image: postgres:16"} {
		if !strings.Contains(out, keep) {
			t.Errorf("missing %q in:\n%s", keep, out)
		}
	}
	// Still a Compose file a viewer can read the structure of.
	var doc struct {
		Services map[string]struct {
			Environment any `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("masked file no longer parses: %v\n%s", err, out)
	}
	env, _ := doc.Services["app"].Environment.(map[string]any)
	if env["DB_PASSWORD"] != maskedEnvValue || env["ALIASED"] != maskedEnvValue || env["FROM_ENV"] != "${API_KEY}" ||
		env["PEM"] != maskedEnvValue {
		t.Errorf("parsed app environment = %v", env)
	}
}

// Each of these once showed a value to a viewer.
func TestMaskComposeFileHardCases(t *testing.T) {
	cases := map[string]string{
		"service merge":   "x-base: &base\n  environment:\n    SECRET: sekret1\nservices:\n  web:\n    <<: *base\n    image: nginx\n",
		"build merge":     "x-b: &b\n  args:\n    TOKEN: sekret1\nservices:\n  web:\n    build:\n      <<: *b\n      context: .\n",
		"root merge":      "x-root: &root\n  services:\n    web:\n      environment:\n        A: sekret1\n<<: *root\n",
		"merge list":      "x-a: &a\n  environment: {A: sekret1}\nx-b: &b\n  image: nginx\nservices:\n  web:\n    <<: [*b, *a]\n",
		"plain next line": "services:\n  web:\n    environment:\n      B:\n        sekret1\n        sekret2\n      C: x\n",
		"quoted wrap":     "services:\n  web:\n    environment:\n      A: \"line1\n  sekret1\"\n",
		"escaped break":   "services:\n  web:\n    environment:\n      A: \"line1\\\n  sekret1\"\n",
		"flow multiline":  "services:\n  web:\n    environment: {A: x,\n      B: sekret1}\n",
		"nested flow":     "services: {web: {environment: {A: sekret1}}}\n",
		"block value":     "services:\n  web:\n    environment:\n      KEY:\n        nested: sekret1\n",
		"list mapping":    "services:\n  web:\n    environment:\n      - nested: sekret1\n",
		"line comment":    "services:\n  web:\n    environment:\n      A: x # was sekret1\n",
		"head comment":    "services:\n  web:\n    environment:\n      # OLD_PASS: sekret1\n      A: x\n",
		"foot comment":    "services:\n  web:\n    environment:\n      A: x\n      # OLD_PASS: sekret1\n    image: nginx\n",
		"list comment":    "services:\n  web:\n    environment:\n      # - OLD=sekret1\n      - A=x\n",
		"multi doc":       "services:\n  a:\n    image: nginx\n---\nservices:\n  web:\n    environment:\n      A: sekret1\n",
	}
	for name, in := range cases {
		out := maskComposeFile(in)
		if strings.Contains(out, "sekret") {
			t.Errorf("%s leaked:\n%s", name, out)
		}
		if !strings.Contains(out, maskedEnvValue) {
			t.Errorf("%s has no mask:\n%s", name, out)
		}
		dec := yaml.NewDecoder(strings.NewReader(out))
		for {
			var v any
			err := dec.Decode(&v)
			if err != nil {
				if err.Error() != "EOF" {
					t.Errorf("%s no longer parses: %v\n%s", name, err, out)
				}
				break
			}
		}
	}
}

func TestMaskComposeFileLeavesOtherYAMLAndHidesBroken(t *testing.T) {
	plain := "name: site\nsettings:\n  password: notcompose\n"
	if got := maskComposeFile(plain); got != plain {
		t.Errorf("non-compose YAML changed: %q", got)
	}
	noValues := "services:\n  app:\n    image: nginx\n"
	if got := maskComposeFile(noValues); got != noValues {
		t.Errorf("compose without values changed: %q", got)
	}
	broken := "services:\n  app:\n    environment:\n      KEY: sekret\n   bad: [\n"
	if got := maskComposeFile(broken); strings.Contains(got, "sekret") || !strings.Contains(got, maskedEnvValue) {
		t.Errorf("broken YAML = %q", got)
	}
}
