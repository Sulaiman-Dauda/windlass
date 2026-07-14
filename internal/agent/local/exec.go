package local

import (
	"context"
	"fmt"
	"sync"

	"github.com/moby/moby/client"

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
	exec, err := cli.ExecCreate(ctx, req.ContainerID, client.ExecCreateOptions{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		TTY:          req.TTY,
		Cmd:          cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("exec create: %w", err)
	}

	attach, err := cli.ExecAttach(ctx, exec.ID, client.ExecAttachOptions{TTY: req.TTY})
	if err != nil {
		return nil, fmt.Errorf("exec attach: %w", err)
	}

	if req.TTY && req.Cols > 0 {
		_, _ = cli.ExecResize(ctx, exec.ID, client.ExecResizeOptions{
			Width: uint(req.Cols), Height: uint(req.Rows),
		})
	}

	s := &execSession{
		cli:    cli,
		execID: exec.ID,
		attach: attach.HijackedResponse,
		out:    make(chan []byte, 32),
		done:   make(chan struct{}),
	}
	go s.pump()
	return s, nil
}

type execSession struct {
	cli    *client.Client
	execID string
	attach client.HijackedResponse

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
	_, err := s.cli.ExecResize(context.Background(), s.execID, client.ExecResizeOptions{
		Width: uint(cols), Height: uint(rows),
	})
	return err
}

func (s *execSession) Output() <-chan []byte { return s.out }

func (s *execSession) Wait() (int, error) {
	<-s.done
	inspect, err := s.cli.ExecInspect(context.Background(), s.execID, client.ExecInspectOptions{})
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
