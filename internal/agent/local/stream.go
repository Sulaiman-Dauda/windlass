package local

import (
	"bufio"
	"encoding/base64"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
)

func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// streamCmd runs cmd, forwarding stdout/stderr lines to out (which may be
// nil), and returns the command's error.
func streamCmd(cmd *exec.Cmd, out agent.LogSink) error {
	if out == nil {
		return cmd.Run()
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	var wg sync.WaitGroup
	scan := func(r io.Reader, stream string) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			out(agent.LogLine{Stream: stream, Text: sc.Text(), Time: time.Now().UTC()})
		}
	}
	wg.Add(2)
	go scan(stdout, "stdout")
	go scan(stderr, "stderr")
	wg.Wait()
	return cmd.Wait()
}
