package local

import (
	"context"
	"fmt"
	"sync"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"

	"github.com/windlass-dev/windlass/internal/agent"
)

func (e execLocal) Start(ctx context.Context, req agent.ExecReq) (agent.ExecSession, error) {
	cli, err := e.l.docker()
	if err != nil {
		return nil, err
	}

	cmd := req.Cmd
	if len(cmd) == 0 {
		cmd = []string{"/bin/sh"}
	}
	exec, err := cli.ContainerExecCreate(ctx, req.ContainerID, container.ExecOptions{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          req.TTY,
		Cmd:          cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("exec create: %w", err)
	}

	attach, err := cli.ContainerExecAttach(ctx, exec.ID, container.ExecAttachOptions{Tty: req.TTY})
	if err != nil {
		return nil, fmt.Errorf("exec attach: %w", err)
	}

	if req.TTY && req.Cols > 0 {
		_ = cli.ContainerExecResize(ctx, exec.ID, container.ResizeOptions{
			Width: uint(req.Cols), Height: uint(req.Rows),
		})
	}

	s := &execSession{
		cli:    cli,
		execID: exec.ID,
		attach: attach,
		out:    make(chan []byte, 32),
		done:   make(chan struct{}),
	}
	go s.pump()
	return s, nil
}

type execSession struct {
	cli    *client.Client
	execID string
	attach types.HijackedResponse

	out  chan []byte
	done chan struct{}
	once sync.Once
}

// pump copies exec output (raw TTY stream) to the output channel.
func (s *execSession) pump() {
	defer s.Close()
	buf := make([]byte, 32*1024)
	for {
		n, err := s.attach.Reader.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			select {
			case s.out <- chunk:
			case <-s.done:
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *execSession) Write(p []byte) error {
	_, err := s.attach.Conn.Write(p)
	return err
}

func (s *execSession) Resize(cols, rows uint16) error {
	return s.cli.ContainerExecResize(context.Background(), s.execID, container.ResizeOptions{
		Width: uint(cols), Height: uint(rows),
	})
}

func (s *execSession) Output() <-chan []byte { return s.out }

func (s *execSession) Wait() (int, error) {
	<-s.done
	inspect, err := s.cli.ContainerExecInspect(context.Background(), s.execID)
	if err != nil {
		return -1, err
	}
	return inspect.ExitCode, nil
}

func (s *execSession) Close() error {
	s.once.Do(func() {
		s.attach.Close()
		close(s.done)
	})
	return nil
}
