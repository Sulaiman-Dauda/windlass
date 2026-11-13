package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/windlass-dev/windlass/internal/agent"
)

// AdoptionConflict describes a stack already on the server, started from a
// directory with this project's name, that Compose knows under another
// project name. Windlass always runs `docker compose -p <directory name>`,
// so deploying would start a second copy beside it with new, empty named
// volumes rather than take it over.
type AdoptionConflict struct {
	Project    string   `json:"project"`
	RunningAs  string   `json:"running_as"`
	WorkingDir string   `json:"working_dir"`
	Containers []string `json:"containers"`
	// NewVolumes are the named volumes the deploy would create, empty.
	NewVolumes []string `json:"new_volumes,omitempty"`
}

// Message explains the outcome and the fix in a sentence or three.
func (c AdoptionConflict) Message() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Containers started from %s run as Compose project %q, but Windlass deploys "+
		"this directory as %q. Deploying now would start a second copy beside them", c.WorkingDir,
		c.RunningAs, c.Project)
	if len(c.NewVolumes) > 0 {
		fmt.Fprintf(&b, " with new, empty volumes (%s), so it would not see the running stack's data",
			strings.Join(c.NewVolumes, ", "))
	} else {
		b.WriteString(", and any named volume it creates would start empty")
	}
	b.WriteString(". ")
	if agent.ValidProjectName(c.RunningAs) {
		fmt.Fprintf(&b, "To keep the data, rename the directory to %s and scan again. ", c.RunningAs)
	} else {
		fmt.Fprintf(&b, "%q is not a valid project name, so the directory cannot simply be renamed: "+
			"copy the data into the new volumes first. ", c.RunningAs)
	}
	fmt.Fprintf(&b, "To start fresh instead, remove the old stack with `docker compose -p %s down` "+
		"(this keeps its volumes) and deploy again.", c.RunningAs)
	return b.String()
}

// AdoptionError blocks a first deploy that would hit an AdoptionConflict.
type AdoptionError struct{ Conflict AdoptionConflict }

func (e *AdoptionError) Error() string { return e.Conflict.Message() }

// FirstDeployConflict returns the adoption conflict for a project that has
// never deployed successfully, or nil. Once Windlass has deployed it, its
// own containers are the stack and the check no longer applies.
func (s *Service) FirstDeployConflict(ctx context.Context, project string, projectID int64) (*AdoptionConflict, error) {
	if _, err := s.q.LatestSucceededDeployment(ctx, projectID); err == nil {
		return nil, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	names, err := s.agent.FS().DiscoverProjects(ctx)
	if err != nil {
		return nil, err
	}
	containers, err := s.agent.Docker().ListContainers(ctx, agent.ContainerFilter{})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	conflicts := adoptionConflicts(containers, []string{project}, names)
	if len(conflicts) == 0 {
		return nil, nil
	}
	c := conflicts[0]
	c.NewVolumes = s.newVolumes(ctx, project)
	return &c, nil
}

// Skipped names the directories a scan passed over and why. It never
// returns nil, so the API answers [] rather than null.
func (s *Service) Skipped(ctx context.Context) ([]agent.SkippedProject, error) {
	list, err := s.agent.FS().SkippedProjects(ctx)
	if list == nil {
		list = []agent.SkippedProject{}
	}
	return list, err
}

// AdoptionConflicts reports a conflict for each discovered project that
// has never deployed successfully, for the scan to show before anyone
// presses Deploy.
func (s *Service) AdoptionConflicts(ctx context.Context) ([]AdoptionConflict, error) {
	names, err := s.agent.FS().DiscoverProjects(ctx)
	if err != nil {
		return nil, err
	}
	containers, err := s.agent.Docker().ListContainers(ctx, agent.ContainerFilter{})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	var out []AdoptionConflict
	for _, c := range adoptionConflicts(containers, names, names) {
		p, err := s.q.GetProjectByName(ctx, c.Project)
		if err != nil {
			return nil, err
		}
		if _, err := s.q.LatestSucceededDeployment(ctx, p.ID); err == nil {
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		c.NewVolumes = s.newVolumes(ctx, c.Project)
		out = append(out, c)
	}
	return out, nil
}

// adoptionConflicts matches containers to projects by the last element of
// the directory Compose ran in. That covers a stack started by hand inside
// the projects directory under another name (a `name:` key or
// COMPOSE_PROJECT_NAME) and one whose files were copied in from elsewhere,
// such as /srv/<name>, while the original keeps running.
//
// A stack running as one of the managed projects is never a conflict.
// Compose keeps a container's labels until it recreates it, so after a
// directory is renamed to adopt a stack, its containers still name the old
// directory; without this a new project reusing that name would be blocked.
func adoptionConflicts(containers []agent.Container, projects, managed []string) []AdoptionConflict {
	wanted := map[string]bool{}
	for _, p := range projects {
		wanted[p] = true
	}
	isManaged := map[string]bool{}
	for _, p := range managed {
		isManaged[p] = true
	}
	type key struct{ project, runningAs, dir string }
	found := map[key][]string{}
	for _, c := range containers {
		if c.ComposeProject == "" || c.ComposeWorkingDir == "" {
			continue
		}
		name := filepath.Base(filepath.Clean(c.ComposeWorkingDir))
		if !wanted[name] || c.ComposeProject == name || isManaged[c.ComposeProject] {
			continue
		}
		k := key{name, c.ComposeProject, c.ComposeWorkingDir}
		found[k] = append(found[k], c.Name)
	}
	out := make([]AdoptionConflict, 0, len(found))
	for k, names := range found {
		sort.Strings(names)
		out = append(out, AdoptionConflict{Project: k.project, RunningAs: k.runningAs,
			WorkingDir: k.dir, Containers: names})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		if out[i].RunningAs != out[j].RunningAs {
			return out[i].RunningAs < out[j].RunningAs
		}
		return out[i].WorkingDir < out[j].WorkingDir
	})
	return out
}

// newVolumes names the non-external volumes Compose would create for
// project. It is detail for the message only: when the file does not
// resolve the deploy fails on its own, so an error here returns nothing.
func (s *Service) newVolumes(ctx context.Context, project string) []string {
	cfg, err := s.agent.Compose().Config(ctx, project)
	if err != nil {
		return nil
	}
	var out []string
	for _, v := range cfg.Volumes {
		if !v.External && v.Name != "" {
			out = append(out, v.Name)
		}
	}
	sort.Strings(out)
	return out
}
