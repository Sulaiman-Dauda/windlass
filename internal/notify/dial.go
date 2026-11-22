package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"time"
)

// ErrRefusedAddress is returned when a channel's destination resolves to an
// address Windlass will not send to.
var ErrRefusedAddress = errors.New("destination is a private, loopback or link-local address")

// refused reports whether ip is an address an alert must not be sent to.
//
// The point is the socket proxy on 127.0.0.1:2375 and the Caddy admin API on
// 127.0.0.1:2019: a webhook URL aimed at either would turn the alert sender
// into a way to drive Docker or rewrite routes. Cloud metadata services sit on
// link-local 169.254.169.254, and the private ranges hold whatever else is on
// the host's network.
func refused(ip netip.Addr, allowLoopback bool) bool {
	ip = ip.Unmap()
	if ip.IsLoopback() {
		return !allowLoopback
	}
	for _, p := range extraRefused {
		if p.Contains(ip) {
			return true
		}
	}
	return ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}

// extraRefused are ranges netip does not count as private but that reach
// something other than the public internet: shared address space (carrier
// NAT, Tailscale, and Alibaba Cloud's metadata service on 100.100.100.200),
// IETF protocol assignments, benchmarking, and deprecated IPv6 site-local.
var extraRefused = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("fec0::/10"),
}

// dialer returns a dialer that checks each address after DNS resolution,
// just before the socket connects. Checking the name earlier would let a name
// resolve to a public address for the check and a private one for the dial.
func dialer(allowLoopback bool) *net.Dialer {
	return &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil {
				return fmt.Errorf("unexpected dial address %q", address)
			}
			if refused(ip, allowLoopback) {
				return fmt.Errorf("%w: %s", ErrRefusedAddress, ip)
			}
			return nil
		},
	}
}

// dialFunc is what the senders dial with. Tests swap it to reach a local
// receiver; nothing else does.
var dialFunc = func(ctx context.Context, network, addr string, allowLoopback bool) (net.Conn, error) {
	return dialer(allowLoopback).DialContext(ctx, network, addr)
}
