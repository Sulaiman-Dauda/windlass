package deploy

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/windlass-dev/windlass/internal/store/db"
)

// TagSuggestSetting turns the bad-tag suggestion on or off. It is on unless
// the value is "off": the lookup sends only the image name, which the pull
// that failed had already sent to Docker Hub.
const TagSuggestSetting = "deploy_tag_suggestions"

// maxSuggestions is how many tags a failed deploy offers.
const maxSuggestions = 5

// TagSuggestion is the nearest existing tags for an image whose tag does
// not exist on Docker Hub.
type TagSuggestion struct {
	// Image is the reference as written in the Compose file, Tag its tag.
	Image string `json:"image"`
	Tag   string `json:"tag"`
	// Repository is the Docker Hub namespace/name that was searched.
	Repository string `json:"repository"`
	// Tags is nearest first; empty when Docker Hub listed nothing close.
	Tags []string `json:"tags"`
}

// ErrNoTagSuggestion means the deployment did not fail on a missing tag of
// a Docker Hub image, or the suggestion is turned off.
var ErrNoTagSuggestion = errors.New("no tag suggestion for this deployment")

// TagSuggestEnabled reports whether failed deploys look up nearby tags.
func (s *Service) TagSuggestEnabled(ctx context.Context) bool {
	v, err := s.q.GetSetting(ctx, TagSuggestSetting)
	return err != nil || strings.TrimSpace(v) != "off"
}

// SetTagSuggestEnabled stores the setting.
func (s *Service) SetTagSuggestEnabled(ctx context.Context, on bool) error {
	v := "on"
	if !on {
		v = "off"
	}
	return s.q.SetSetting(ctx, db.SetSettingParams{Key: TagSuggestSetting, Value: v})
}

// maxSuggestedImages caps how many missing images one failure looks up.
const maxSuggestedImages = 3

// TagSuggestions reads a failed deployment's error and log for image tags
// that do not exist and, for each one on Docker Hub, lists the repository's
// nearest tags. It reads Docker Hub on each call and writes nothing.
func (s *Service) TagSuggestions(ctx context.Context, d db.Deployment) ([]TagSuggestion, error) {
	if d.Status != "failed" || !s.TagSuggestEnabled(ctx) {
		return nil, ErrNoTagSuggestion
	}
	lines := []string{d.ErrorLine.String, d.Error.String}
	if evs, err := s.Events(ctx, d.ID, 0); err == nil {
		for _, ev := range evs {
			lines = append(lines, ev.Message)
		}
	}
	var out []TagSuggestion
	seen := map[string]bool{}
	for _, line := range lines {
		ref := missingRef(line)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		repo, tag, ok := hubRepository(ref)
		if !ok {
			continue
		}
		tags, err := s.hubCandidates(ctx, repo, tag)
		if err != nil {
			return nil, err
		}
		out = append(out, TagSuggestion{Image: ref, Tag: tag, Repository: repo, Tags: nearestTags(tag, tags, maxSuggestions)})
		if len(out) == maxSuggestedImages {
			break
		}
	}
	if len(out) == 0 {
		return nil, ErrNoTagSuggestion
	}
	return out, nil
}

// hubCandidates lists the tags to rank for a missing one: the most recent,
// and for a version tag those that share its first two numbers, which a
// busy repository's recent tags may not reach ("1.27.9" on nginx, whose
// last few hundred pushes are all newer lines).
func (s *Service) hubCandidates(ctx context.Context, repo, tag string) ([]string, error) {
	tags, err := s.agent.Docker().HubTags(ctx, repo, "")
	if err != nil {
		return nil, err
	}
	if p, ok := parseTag(tag); ok && len(p.nums) > 1 {
		prefix := strconv.Itoa(p.nums[0]) + "." + strconv.Itoa(p.nums[1])
		near, err := s.agent.Docker().HubTags(ctx, repo, prefix)
		if err != nil {
			return nil, err
		}
		tags = append(near, tags...)
	}
	return tags, nil
}

// missingTagPatterns find the reference in each wording of a missing tag:
//
//	 Image nginx:1.99 Error manifest unknown: manifest unknown              (Compose v5 progress)
//	manifest for nginx:1.99 not found: manifest unknown: manifest unknown  (daemon, older Compose)
//	failed to resolve reference "docker.io/library/nginx:1.99": ...: not found  (containerd store)
//
// The daemon's own summary line in Compose v5 ("Error response from
// daemon: manifest unknown") names no image, so the log is read too.
var missingTagPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bImage (\S+) Error manifest unknown`),
	regexp.MustCompile(`manifest for (\S+) not found`),
	regexp.MustCompile(`failed to resolve reference "([^"]+)": [^"]*\bnot found`),
}

func missingRef(line string) string {
	for _, re := range missingTagPatterns {
		if m := re.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

// hubRepository maps an image reference to its Docker Hub namespace/name
// and tag. A reference on another registry, pinned by digest, or with no
// tag is not ok.
func hubRepository(ref string) (repo, tag string, ok bool) {
	if ref == "" || strings.Contains(ref, "@") {
		return "", "", false
	}
	i := strings.LastIndex(ref, ":")
	if i < 0 || strings.Contains(ref[i:], "/") {
		return "", "", false
	}
	name, tag := ref[:i], ref[i+1:]
	if tag == "" {
		return "", "", false
	}
	parts := strings.Split(name, "/")
	// A first part with a dot, a colon or "localhost" is a registry host.
	if first := parts[0]; len(parts) > 1 && (strings.ContainsAny(first, ".:") || first == "localhost") {
		switch first {
		case "docker.io", "index.docker.io", "registry-1.docker.io":
			parts = parts[1:]
		default:
			return "", "", false
		}
	}
	switch len(parts) {
	case 1:
		parts = []string{"library", parts[0]}
	case 2:
	default:
		return "", "", false
	}
	if parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return strings.ToLower(parts[0] + "/" + parts[1]), tag, true
}

// versionTag splits "v1.25.3-alpine" into [1 25 3] and "-alpine".
var versionTag = regexp.MustCompile(`^v?(\d+(?:\.\d+)*)(.*)$`)

type parsedTag struct {
	nums   []int
	suffix string
}

func parseTag(t string) (parsedTag, bool) {
	m := versionTag.FindStringSubmatch(t)
	if m == nil {
		return parsedTag{}, false
	}
	var p parsedTag
	for _, f := range strings.Split(m[1], ".") {
		n, err := strconv.Atoi(f)
		if err != nil {
			return parsedTag{}, false
		}
		p.nums = append(p.nums, n)
	}
	p.suffix = m[2]
	return p, true
}

// nearestTags orders tags by closeness to want and returns at most n.
//
// For a version tag ("1.99-alpine") the candidates are version tags with
// the same suffix, ranked by how many leading numbers match, then by the
// gap at the first number that differs, preferring the same number of
// parts; a shorter floating tag ("1.25") comes after the concrete ones. Anything else is ranked by edit distance. Tags that are not close
// at all (an edit distance over half the length) are left out.
func nearestTags(want string, tags []string, n int) []string {
	type cand struct {
		tag   string
		score [4]int // lower is nearer
	}
	var cands []cand
	wp, wantVersion := parseTag(want)
	if wantVersion {
		for _, t := range tags {
			p, ok := parseTag(t)
			if !ok || p.suffix != wp.suffix || t == want {
				continue
			}
			common := 0
			for common < len(p.nums) && common < len(wp.nums) && p.nums[common] == wp.nums[common] {
				common++
			}
			// A tag that runs out of numbers first ("1" for "1.28.3") is a
			// floating tag: it goes after every concrete version that
			// matches as many leading numbers.
			gap := 1 << 30
			if common < len(p.nums) && common < len(wp.nums) {
				gap = abs(p.nums[common] - wp.nums[common])
			}
			cands = append(cands, cand{t, [4]int{-common, gap, abs(len(p.nums) - len(wp.nums)), 0}})
		}
	}
	if len(cands) == 0 {
		limit := (len(want) + 1) / 2
		for _, t := range tags {
			if t == want {
				continue
			}
			if dist := editDistance(strings.ToLower(want), strings.ToLower(t)); dist <= limit {
				cands = append(cands, cand{t, [4]int{dist, 0, 0, 0}})
			}
		}
	}
	// Stable, so equal scores keep Docker Hub's most-recent-first order.
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i].score, cands[j].score
		for k := range a {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return false
	})
	out := []string{}
	seen := map[string]bool{}
	for _, c := range cands {
		if len(out) == n {
			break
		}
		if !seen[c.tag] {
			seen[c.tag] = true
			out = append(out, c.tag)
		}
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// editDistance is the Levenshtein distance between a and b, by byte.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
