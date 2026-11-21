package local

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
)

// probeTimeout bounds each network step of a domain probe.
const probeTimeout = 5 * time.Second

// ProbeDomain runs each check from this server. The listener and
// certificate checks dial the Caddy host the admin API is on, so they see
// what Caddy serves without depending on DNS or hairpin NAT.
func (h hostLocal) ProbeDomain(ctx context.Context, hostname string) (agent.DomainProbe, error) {
	p := agent.DomainProbe{Addresses: []string{}, Local: []string{}}

	lctx, cancel := context.WithTimeout(ctx, probeTimeout)
	addrs, err := net.DefaultResolver.LookupIPAddr(lctx, hostname)
	cancel()
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		p.NotFound = true
	case err != nil:
		p.DNSError = err.Error()
	}
	for _, a := range addrs {
		ip := a.IP.String()
		p.Addresses = append(p.Addresses, ip)
		if isLocalAddr(a.IP) {
			p.Local = append(p.Local, ip)
		}
	}
	p.Outbound = outboundAddr()

	host := h.caddyHost()
	p.Listen80 = canDial(ctx, net.JoinHostPort(host, "80"))
	p.Listen443 = canDial(ctx, net.JoinHostPort(host, "443"))
	if p.Listen443 {
		p.Cert, p.CertError = servedCert(ctx, net.JoinHostPort(host, "443"), hostname)
	} else {
		p.CertError = "nothing accepts connections on port 443"
	}
	return p, nil
}

// caddyHost is the host part of the Caddy admin URL, where Caddy's own
// listeners are too.
func (h hostLocal) caddyHost() string {
	u, err := url.Parse(h.l.cfg.CaddyAdmin)
	if err != nil || u.Hostname() == "" {
		return "127.0.0.1"
	}
	return u.Hostname()
}

// isLocalAddr reports whether this server owns ip, by binding a UDP socket
// to it. Listing interfaces needs netlink, which the systemd unit does not
// allow; a bind to an address the server does not have fails.
func isLocalAddr(ip net.IP) bool {
	if ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
		return false
	}
	c, err := net.ListenPacket("udp", net.JoinHostPort(ip.String(), "0"))
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// outboundAddr is the source address the kernel picks to reach a public
// address. A UDP connect sends no packet.
func outboundAddr() string {
	c, err := net.Dial("udp", "1.1.1.1:53")
	if err != nil {
		return ""
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}

func canDial(ctx context.Context, addr string) bool {
	d := net.Dialer{Timeout: probeTimeout}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// servedCert reads the certificate served at addr for hostname and checks
// it against the system roots. It is the one TLS check, shared by the
// domain check and the certificate expiry alert.
func servedCert(ctx context.Context, addr, hostname string) (*agent.CertInfo, string) {
	d := tls.Dialer{
		NetDialer: &net.Dialer{Timeout: probeTimeout},
		// Verified below, so an untrusted certificate is still described.
		Config: &tls.Config{ServerName: hostname, InsecureSkipVerify: true}, //nolint:gosec // verified by hand below
	}
	dctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	conn, err := d.DialContext(dctx, "tcp", addr)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "internal error") {
			msg = "Caddy has no certificate for this hostname yet"
		}
		return nil, msg
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, "no certificate was presented"
	}
	leaf := certs[0]
	info := &agent.CertInfo{Issuer: issuerName(leaf), NotAfter: leaf.NotAfter.UTC()}
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: hostname, Intermediates: inter}); err != nil {
		info.VerifyError = err.Error()
	} else {
		info.Trusted = true
	}
	return info, ""
}

func issuerName(c *x509.Certificate) string {
	if len(c.Issuer.Organization) > 0 {
		if c.Issuer.CommonName != "" {
			return c.Issuer.Organization[0] + " " + c.Issuer.CommonName
		}
		return c.Issuer.Organization[0]
	}
	return c.Issuer.CommonName
}
