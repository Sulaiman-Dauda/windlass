package deploy

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
)

// Drift compares what each container runs with what the project's files
// and local images say it should run now. It only reads: nothing is pulled,
// built or recreated.
type Drift struct {
	Project   string         `json:"project"`
	CheckedAt time.Time      `json:"checked_at"`
	Drifted   bool           `json:"drifted"`
	Files     []string       `json:"extra_files,omitempty"`
	Services  []ServiceDrift `json:"services"`
}

// ServiceDrift is one container, or one service with no container.
//
//   - ConfigChanged: the Compose configuration for the service is not the
//     one the container was created from. A deploy recreates it.
//   - ImageChanged: the image tag now names a different local image, for
//     example after a pull or build that was not followed by an up.
//   - Missing: the files declare the service and no container exists.
//   - Orphan: the container's service is no longer in the files. A deploy
//     removes it.
type ServiceDrift struct {
	Service       string `json:"service"`
	Container     string `json:"container,omitempty"`
	ConfigChanged bool   `json:"config_changed"`
	ImageChanged  bool   `json:"image_changed"`
	Missing       bool   `json:"missing,omitempty"`
	Orphan        bool   `json:"orphan,omitempty"`
}

// Drift reports the drift for project. The files are hashed the way the
// last successful deploy ran them, with the rollback override for a pinned
// one, so a rollback that is live does not read as drift.
func (s *Service) Drift(ctx context.Context, project string) (Drift, error) {
	p, err := s.projects.Get(ctx, project)
	if err != nil {
		return Drift{}, err
	}
	var extra []string
	last, err := s.q.LatestSucceededDeployment(ctx, p.ID)
	switch {
	case err == nil && pinned(last):
		extra = []string{rollbackFile}
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return Drift{}, err
	}
	hashes, err := s.agent.Compose().ConfigHashes(ctx, project, extra)
	if err != nil {
		return Drift{}, err
	}
	cfg, err := s.agent.Compose().Config(ctx, project)
	if err != nil {
		return Drift{}, err
	}
	containers, err := s.agent.Docker().ListContainers(ctx, agent.ContainerFilter{ComposeProject: project})
	if err != nil {
		return Drift{}, err
	}

	// A tag resolves to one image ID; look each up once.
	imageIDs := map[string]string{}
	imageID := func(ref string) string {
		if id, ok := imageIDs[ref]; ok {
			return id
		}
		id, err := s.agent.Docker().ImageID(ctx, ref)
		if err != nil {
			id = "" // no local image to compare with: not drift we can show
		}
		imageIDs[ref] = id
		return id
	}

	d := Drift{Project: project, CheckedAt: time.Now().UTC(), Files: extra}
	seen := map[string]bool{}
	for _, c := range containers {
		seen[c.ComposeService] = true
		want, declared := hashes[c.ComposeService]
		row := ServiceDrift{Service: c.ComposeService, Container: c.Name}
		if !declared {
			row.Orphan = true
		} else {
			row.ConfigChanged = c.ConfigHash != "" && c.ConfigHash != want
			// A live rollback runs images pinned by digest, which cannot move;
			// cfg is read without the override, so its tags would.
			if ref := cfg.Services[c.ComposeService].Image; len(extra) == 0 && ref != "" && c.ImageID != "" {
				if id := imageID(ref); id != "" && id != c.ImageID {
					row.ImageChanged = true
				}
			}
		}
		d.Services = append(d.Services, row)
	}
	for service := range hashes {
		if !seen[service] {
			d.Services = append(d.Services, ServiceDrift{Service: service, Missing: true})
		}
	}
	sort.Slice(d.Services, func(i, j int) bool {
		a, b := d.Services[i], d.Services[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		return a.Container < b.Container
	})
	d.Drifted = slices.ContainsFunc(d.Services, func(r ServiceDrift) bool {
		return r.ConfigChanged || r.ImageChanged || r.Missing || r.Orphan
	})
	if d.Services == nil {
		d.Services = []ServiceDrift{}
	}
	return d, nil
}
