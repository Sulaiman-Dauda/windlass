//go:build linux

package server

import (
	"net"
	"os"
)

// NotifySystemd sends sd_notify state (e.g. "READY=1") when running under a
// Type=notify systemd unit. ~20 lines beats a dependency (principle 10).
func NotifySystemd(state string) {
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		return
	}
	defer conn.Close()
	conn.Write([]byte(state))
}
