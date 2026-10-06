package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
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
		// Only a terminal takes input. Without a TTY, an attached stdin that
		// nobody writes to leaves a password prompt waiting forever.
		AttachStdin:  req.TTY,
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
		tty:    req.TTY,
		out:    make(chan []byte, 32),
		done:   make(chan struct{}),
	}
	go s.pump()
	// The client only uses ctx to connect, so end the session ourselves when
	// the caller's deadline passes or it cancels. Docker has no way to stop an
	// exec, so the command itself runs on in the container until it finishes.
	go func() {
		select {
		case <-ctx.Done():
			s.Close()
		case <-s.done:
		}
	}()
	return s, nil
}

type execSession struct {
	cli    *client.Client
	execID string
	attach client.HijackedResponse
	tty    bool

	out    chan []byte
	done   chan struct{}
	once   sync.Once
	stderr tailBuffer
}

// pump copies the command's output to the output channel and closes the
// channel when the command ends. With a TTY the stream is the terminal's raw
// bytes. Without one, Docker multiplexes stdout and stderr behind 8-byte frame
// headers, so the stream is split: Output carries stdout only, and the end of
// stderr is kept for Wait to report.
func (s *execSession) pump() {
	defer close(s.out)
	defer s.Close()
	stdout := chanWriter{s}
	if s.tty {
		_, _ = io.Copy(stdout, s.attach.Reader)
		return
	}
	_, _ = stdcopy.StdCopy(stdout, &s.stderr, s.attach.Reader)
}

// chanWriter hands each write to the session's output channel, and stops the
// copy once the session is closed.
type chanWriter struct{ s *execSession }

func (w chanWriter) Write(p []byte) (int, error) {
	select {
	case w.s.out <- append([]byte(nil), p...):
		return len(p), nil
	case <-w.s.done:
		return 0, errSessionClosed
	}
}

var errSessionClosed = errors.New("exec session closed")

// tailBuffer keeps the last stderrTail bytes written to it.
type tailBuffer struct {
	mu sync.Mutex
	b  []byte
}

const stderrTail = 4 << 10

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > stderrTail {
		t.b = t.b[len(t.b)-stderrTail:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.b))
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
	// The stream can end a moment before Docker records the exit code, so
	// wait briefly for the exec to stop running before reading it.
	var inspect client.ExecInspectResult
	for range 50 {
		var err error
		inspect, err = s.cli.ExecInspect(context.Background(), s.execID, client.ExecInspectOptions{})
		if err != nil {
			return -1, err
		}
		if !inspect.Running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if inspect.Running {
		return -1, errors.New("exec still running after its output ended")
	}
	if inspect.ExitCode != 0 && !s.tty {
		if msg := s.stderr.String(); msg != "" {
			return inspect.ExitCode, fmt.Errorf("exit status %d: %s", inspect.ExitCode, msg)
		}
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
