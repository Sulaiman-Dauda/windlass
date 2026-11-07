package mcp

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const redacted = "[redacted]"

// minSecretLen is the shortest env value scrubbed. Shorter ones (1, true,
// 8080) would blank ordinary words and numbers all through the logs.
// Under a key that names a secret the floor is minNamedSecretLen, since a
// short password is still a password; three characters or fewer would
// blank too much text even there.
const (
	minSecretLen      = 6
	minNamedSecretLen = 4
)

// secretKeyRE matches a variable name that says it holds a secret.
var secretKeyRE = regexp.MustCompile(`(?i)(pass|pwd|secret|token|private|credential|api_?key|(^|_)key$)`)

// notSecret lists the values a secret-named key often holds that are
// settings, not secrets (TOKEN_AUTH=true).
var notSecret = map[string]bool{"true": true, "false": true, "yes": true, "no": true, "on": true, "off": true}

// minLenFor is the shortest value scrubbed under key. A key from
// Config.EnvValues may carry a prefix ending in a colon; the name is what
// follows the last one.
func minLenFor(key string) int {
	if i := strings.LastIndexByte(key, ':'); i >= 0 {
		key = key[i+1:]
	}
	if secretKeyRE.MatchString(key) {
		return minNamedSecretLen
	}
	return minSecretLen
}

// tokenRE matches a Windlass API token: wl_pat_, a 12-hex public id, a
// 40-hex secret and an 8-hex checksum.
var tokenRE = regexp.MustCompile(`wl_pat_[0-9a-f]{60}`)

func scrubTokens(s string) string {
	if !strings.Contains(s, tokenPrefix) {
		return s
	}
	return tokenRE.ReplaceAllString(s, redacted)
}

// scrubber replaces a project's env values and API tokens in text. One
// that withholds stands in when the env values could not be read: it
// replaces any non-empty text whole.
type scrubber struct {
	env      *strings.Replacer
	withhold bool
}

const withheldText = "[withheld: Windlass could not read this project's env values to scrub this text]"

func withholdingScrubber() *scrubber { return &scrubber{withhold: true} }

// scrubberOrWithhold is scrubberFor for fields that can be blanked rather
// than failing the whole tool.
func (s *Server) scrubberOrWithhold(t *toolCtx, project string) *scrubber {
	sc, err := s.scrubberFor(t.r.Context(), project)
	if err != nil {
		return withholdingScrubber()
	}
	return sc
}

// errNoScrub withholds output when the env values to scrub it with cannot
// be read. Failing closed is the point: a log is never worth a leaked key.
var errNoScrub = errors.New("output withheld: Windlass could not read this project's env values to scrub them from it")

// scrubberFor reads the project's env values through Config.EnvValues.
// Call it only after an API call for the project has succeeded, so the
// caller is known to be allowed to see the project.
func (s *Server) scrubberFor(ctx context.Context, project string) (*scrubber, error) {
	if s.cfg.EnvValues == nil {
		return nil, errNoScrub
	}
	values, err := s.cfg.EnvValues(ctx, project)
	if err != nil {
		return nil, errNoScrub
	}
	return newScrubber(values), nil
}

func newScrubber(values map[string]string) *scrubber {
	seen := map[string]bool{}
	var secrets []string
	add := func(v string, minLen int) {
		v = strings.TrimSpace(v)
		if len(v) >= minLen && !seen[v] && !notSecret[strings.ToLower(v)] {
			seen[v] = true
			secrets = append(secrets, v)
		}
	}
	for k, v := range values {
		minLen := minLenFor(k)
		add(v, minLen)
		// A multi-line value (a PEM key) reaches a log a line at a time.
		if strings.ContainsAny(v, "\r\n") {
			for _, line := range strings.FieldsFunc(v, func(r rune) bool { return r == '\n' || r == '\r' }) {
				add(line, minLen)
			}
		}
	}
	if len(secrets) == 0 {
		return &scrubber{}
	}
	// Longest first, so a value containing another is replaced whole.
	sort.Slice(secrets, func(i, j int) bool {
		if len(secrets[i]) != len(secrets[j]) {
			return len(secrets[i]) > len(secrets[j])
		}
		return secrets[i] < secrets[j]
	})
	pairs := make([]string, 0, 2*len(secrets))
	for _, v := range secrets {
		pairs = append(pairs, v, redacted)
	}
	return &scrubber{env: strings.NewReplacer(pairs...)}
}

func (sc *scrubber) scrub(s string) string {
	if sc != nil && sc.withhold {
		if s == "" {
			return s
		}
		return withheldText
	}
	if sc != nil && sc.env != nil {
		s = sc.env.Replace(s)
	}
	return scrubTokens(s)
}

// tailLines keeps the newest lines that fit in maxLines and maxBytes,
// counting a newline after each. It reports whether anything was cut. A
// single line longer than maxBytes keeps its end, cut on a rune boundary.
func tailLines(lines []string, maxLines, maxBytes int) ([]string, bool) {
	cut := false
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
		cut = true
	}
	total := 0
	start := len(lines)
	for start > 0 {
		n := len(lines[start-1]) + 1
		if total+n > maxBytes {
			break
		}
		total += n
		start--
	}
	if start == len(lines) && len(lines) > 0 {
		// Not even the newest line fits: keep its end.
		return []string{keepEnd(lines[len(lines)-1], maxBytes-1)}, true
	}
	if start > 0 {
		cut = true
	}
	return lines[start:], cut
}

// keepEnd returns the last max bytes of s at most, cut on a rune boundary.
func keepEnd(s string, max int) string {
	if len(s) <= max {
		return s
	}
	keep := s[len(s)-max:]
	for len(keep) > 0 && !utf8.RuneStart(keep[0]) {
		keep = keep[1:]
	}
	return keep
}

// stripURLCredentials removes a user:password@ part from a URL, in case
// someone put a Git token in a repository address.
func stripURLCredentials(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		// git@host:org/repo has no scheme and only a user name. Anything
		// else that will not parse but has an @ could hide a password.
		if strings.Contains(raw, "://") && strings.Contains(raw, "@") {
			return redacted
		}
		return raw
	}
	if u.User == nil {
		return raw
	}
	if _, hasPassword := u.User.Password(); !hasPassword && u.Scheme != "http" && u.Scheme != "https" {
		// ssh://git@host: a user name, not a credential.
		return raw
	}
	u.User = url.User(redacted)
	return strings.Replace(u.String(), url.PathEscape(redacted), redacted, 1)
}
