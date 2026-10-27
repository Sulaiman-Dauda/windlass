package deploy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/dbdetect"
)

// SeedReadyTimeout bounds the wait for staging's database to finish its
// first start, which runs the image's init scripts.
var SeedReadyTimeout = 5 * time.Minute

// seedPoll is how often the readiness check runs.
var seedPoll = 2 * time.Second

// seed copies live's data into a new staging project before its services
// start. Compose creates staging's volumes first, so they carry Compose's
// labels and up reuses them. Each named volume is copied file by file,
// except a database's: a running database's files are not safe to copy, so
// it is dumped from live and loaded into staging's own fresh server.
func (s *Service) seed(ctx context.Context, deploymentID int64, live, staging string, sink agent.LogSink) error {
	s.event(ctx, deploymentID, "step", "copying data from "+live)
	liveCfg, err := s.agent.Compose().Config(ctx, live)
	if err != nil {
		return fmt.Errorf("read %s's compose file: %w", live, err)
	}
	stagingCfg, err := s.agent.Compose().Config(ctx, staging)
	if err != nil {
		return err
	}
	containers, err := s.agent.Docker().ListContainers(ctx, agent.ContainerFilter{ComposeProject: live})
	if err != nil {
		return err
	}
	dbContainer, engine := dbdetect.Best(containers)
	dbService := ""
	if engine != "" {
		if _, ok := stagingCfg.Services[dbContainer.ComposeService]; ok {
			dbService = dbContainer.ComposeService
		}
	}

	if err := s.agent.Compose().Create(ctx, staging, sink); err != nil {
		return fmt.Errorf("create staging's containers and volumes: %w", err)
	}

	skip := map[string]bool{}
	if dbService != "" {
		for _, m := range liveCfg.Services[dbService].Mounts {
			if m.Type == "volume" {
				skip[m.Source] = true
			}
		}
	}
	otherDB := dbdetect.OtherVolumes(liveCfg, dbService)
	sizes, err := s.agent.Docker().VolumeSizes(ctx)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(liveCfg.Volumes))
	for key := range liveCfg.Volumes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		from, to := liveCfg.Volumes[key], stagingCfg.Volumes[key]
		switch {
		case from.External || skip[key]:
			continue
		case otherDB[key] != "":
			s.event(ctx, deploymentID, "log", fmt.Sprintf(
				"volume %s: belongs to database %s, and only one database is copied, so staging's starts empty", key, otherDB[key]))
			continue
		case to.Name == "" || to.External:
			s.event(ctx, deploymentID, "log", fmt.Sprintf("volume %s: staging does not define it, not copied", key))
			continue
		}
		if _, ok := sizes[from.Name]; !ok {
			s.event(ctx, deploymentID, "log", fmt.Sprintf("volume %s: live has not made it yet, staging starts empty", key))
			continue
		}
		s.event(ctx, deploymentID, "log", fmt.Sprintf("copying volume %s", key))
		if err := s.agent.Docker().CopyVolume(ctx, from.Name, to.Name, sink); err != nil {
			return fmt.Errorf("copy volume %s: %w", key, err)
		}
	}

	if dbService == "" {
		return nil
	}
	if dbContainer.State != "running" {
		return fmt.Errorf("%s's %s database (%s) is not running, so it cannot be copied. Start %s, then create staging again",
			live, engine, dbService, live)
	}
	return s.copyDatabase(ctx, deploymentID, engine, dbContainer, staging, dbService, sink)
}

// copyDatabase starts staging's database service alone, waits for it to
// finish initialising and streams live's dump into it.
func (s *Service) copyDatabase(ctx context.Context, deploymentID int64, engine string, from agent.Container,
	staging, service string, sink agent.LogSink) error {
	s.event(ctx, deploymentID, "log", fmt.Sprintf("starting staging's %s database (%s)", engine, service))
	if err := s.agent.Compose().Up(ctx, agent.ComposeUpReq{
		Project: staging, Services: []string{service}, NoDeps: true,
	}, sink); err != nil {
		return fmt.Errorf("start staging's database: %w", err)
	}
	to, err := s.waitForDatabase(ctx, engine, staging, service)
	if err != nil {
		return err
	}
	s.event(ctx, deploymentID, "log", "copying the database")
	if err := s.streamDump(ctx, engine, from.ID, to.ID); err != nil {
		return fmt.Errorf("copy the database: %w", err)
	}
	s.event(ctx, deploymentID, "log", "database copied")
	return nil
}

// waitForDatabase returns staging's database container once it answers over
// TCP.
func (s *Service) waitForDatabase(ctx context.Context, engine, staging, service string) (agent.Container, error) {
	deadline := time.Now().Add(SeedReadyTimeout)
	last := "it has not started"
	for {
		containers, err := s.agent.Docker().ListContainers(ctx, agent.ContainerFilter{ComposeProject: staging})
		if err != nil {
			return agent.Container{}, err
		}
		for _, c := range containers {
			if c.ComposeService != service {
				continue
			}
			if c.State != "running" {
				last = "its container is " + c.State
				break
			}
			code, err := s.execQuiet(ctx, c.ID, dbdetect.ReadyCommands[engine])
			if err == nil && code == 0 {
				return c, nil
			}
			last = "it does not answer yet"
		}
		if time.Now().After(deadline) {
			return agent.Container{}, fmt.Errorf("staging's database was not ready after %s: %s", SeedReadyTimeout, last)
		}
		select {
		case <-ctx.Done():
			return agent.Container{}, ctx.Err()
		case <-time.After(seedPoll):
		}
	}
}

// execQuiet runs a shell command in a container and returns its exit code,
// discarding its output.
func (s *Service) execQuiet(ctx context.Context, containerID, command string) (int, error) {
	sess, err := s.agent.Exec().Start(ctx, agent.ExecReq{ContainerID: containerID, Cmd: []string{"sh", "-c", command}})
	if err != nil {
		return -1, err
	}
	defer sess.Close()
	for range sess.Output() {
	}
	return sess.Wait()
}

// streamDump pipes the dump from one container into the load in another
// without holding it in memory.
func (s *Service) streamDump(ctx context.Context, engine, fromID, toID string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	load, err := s.agent.Exec().Start(ctx, agent.ExecReq{
		ContainerID: toID, Cmd: []string{"sh", "-c", dbdetect.LoadCommands[engine]}, Stdin: true,
	})
	if err != nil {
		return err
	}
	defer load.Close()
	dump, err := s.agent.Exec().Start(ctx, agent.ExecReq{
		ContainerID: fromID, Cmd: []string{"sh", "-c", dbdetect.CopyDumpCommands[engine]},
	})
	if err != nil {
		return err
	}
	defer dump.Close()

	// The load's output is drained alongside, so a chatty load cannot stall
	// on a full channel while the dump is still writing into it.
	loadDone := make(chan struct{})
	go func() {
		defer close(loadDone)
		for range load.Output() {
		}
	}()

	var writeErr error
	for chunk := range dump.Output() {
		if writeErr != nil {
			continue
		}
		if err := load.Write(chunk); err != nil {
			writeErr = err
			cancel()
		}
	}
	code, dumpErr := dump.Wait()
	if writeErr == nil && (dumpErr != nil || code != 0) {
		cancel()
		<-loadDone
		return fmt.Errorf("dump from live failed (exit %d): %w", code, orExit(dumpErr))
	}
	if err := load.CloseWrite(); err != nil && writeErr == nil {
		writeErr = err
	}
	<-loadDone
	loadCode, loadErr := load.Wait()
	switch {
	case loadErr != nil || loadCode != 0:
		return fmt.Errorf("load into staging failed (exit %d): %w", loadCode, orExit(loadErr))
	case writeErr != nil:
		return fmt.Errorf("load into staging: %w", writeErr)
	}
	return nil
}

func orExit(err error) error {
	if err == nil {
		return errors.New("non-zero exit")
	}
	return errors.New(strings.TrimSpace(err.Error()))
}
