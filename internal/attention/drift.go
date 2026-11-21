package attention

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/deploy"
)

// KindDrift is a project whose containers no longer match its files or
// local images.
const KindDrift = "drift"

// DriftChecker is the part of the deploy service the drift rows need.
type DriftChecker interface {
	Drift(ctx context.Context, project string) (deploy.Drift, error)
}

// DriftTTL is how long one drift sweep is kept. A sweep runs two Compose
// commands per project, so it runs in the background, never inside a
// List, and a deploy or a file edit marks it stale at once.
const DriftTTL = 5 * time.Minute

// driftTimeout bounds one sweep of every project.
const driftTimeout = 2 * time.Minute

// SetDrift turns the drift rows on. Without it there are none.
func (s *Service) SetDrift(d DriftChecker) {
	s.driftMu.Lock()
	s.drift = d
	s.driftMu.Unlock()
}

// DriftChanged marks the last sweep stale, so the next List starts one, and
// drops the cached list. Deploys and edits to project files call it.
func (s *Service) DriftChanged() {
	s.driftMu.Lock()
	s.driftExpiry = time.Time{}
	s.driftMu.Unlock()
	s.Invalidate()
}

// driftItems returns the rows from the last sweep and starts a new one in
// the background when it is stale. The first List after start has none.
func (s *Service) driftItems(containers []agent.Container) []Item {
	s.driftMu.Lock()
	defer s.driftMu.Unlock()
	if s.drift == nil {
		return nil
	}
	if time.Now().After(s.driftExpiry) && !s.driftRunning {
		s.driftRunning = true
		go s.sweepDrift(projectsWithContainers(containers))
	}
	return s.driftRows
}

// projectsWithContainers names each Compose project that has a container.
// A project that was never deployed has nothing to drift from.
func projectsWithContainers(containers []agent.Container) []string {
	seen := map[string]bool{}
	var names []string
	for _, c := range containers {
		if c.ComposeProject != "" && !seen[c.ComposeProject] {
			seen[c.ComposeProject] = true
			names = append(names, c.ComposeProject)
		}
	}
	sort.Strings(names)
	return names
}

func (s *Service) sweepDrift(projects []string) {
	ctx, cancel := context.WithTimeout(context.Background(), driftTimeout)
	defer cancel()
	s.driftMu.Lock()
	checker := s.drift
	s.driftMu.Unlock()
	var rows []Item
	for _, name := range projects {
		d, err := checker.Drift(ctx, name)
		if err != nil {
			// Not a Windlass project, or its files do not parse: the
			// project page says why, and a row here would only guess.
			s.logger.Debug("attention: drift", "project", name, "error", err)
			continue
		}
		if item, ok := driftItem(d); ok {
			rows = append(rows, item)
		}
	}
	s.driftMu.Lock()
	s.driftRows, s.driftExpiry, s.driftRunning = rows, time.Now().Add(DriftTTL), false
	s.driftMu.Unlock()
	s.Invalidate()
}

// driftItem is one row for a drifted project. The detail names the
// services, a few at most, by what changed.
func driftItem(d deploy.Drift) (Item, bool) {
	if !d.Drifted {
		return Item{}, false
	}
	var config, image, missing, orphan []string
	for _, r := range d.Services {
		switch {
		case r.Orphan:
			orphan = append(orphan, r.Service)
		case r.Missing:
			missing = append(missing, r.Service)
		case r.ConfigChanged:
			config = append(config, r.Service)
		case r.ImageChanged:
			image = append(image, r.Service)
		}
	}
	var parts []string
	if len(config) > 0 {
		parts = append(parts, names(config)+" "+verb(config, "runs", "run")+" an older configuration")
	}
	if len(image) > 0 {
		parts = append(parts, names(image)+" "+verb(image, "runs", "run")+" an older image than the tag now names")
	}
	if len(missing) > 0 {
		parts = append(parts, names(missing)+" "+verb(missing, "has", "have")+" no container")
	}
	if len(orphan) > 0 {
		parts = append(parts, names(orphan)+" "+verb(orphan, "is", "are")+" no longer in the files")
	}
	detail := strings.Join(parts, "; ") + ". A deploy brings the containers in line."
	return Item{Kind: KindDrift, Tone: ToneInfo, Project: d.Project,
		Title:  d.Project + " differs from its files",
		Detail: detail,
		Link:   "/projects/" + d.Project, Action: "Open project"}, true
}

func verb(list []string, one, many string) string {
	if len(list) == 1 {
		return one
	}
	return many
}

// names lists up to three services and counts the rest.
func names(list []string) string {
	const show = 3
	if len(list) <= show {
		if len(list) == 1 {
			return list[0]
		}
		return strings.Join(list[:len(list)-1], ", ") + " and " + list[len(list)-1]
	}
	return fmt.Sprintf("%s and %d more", strings.Join(list[:show], ", "), len(list)-show)
}
