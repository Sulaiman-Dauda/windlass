package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Channel kinds.
const (
	ChannelWebhook = "webhook"
	ChannelNtfy    = "ntfy"
	ChannelDiscord = "discord"
	ChannelSlack   = "slack"
	ChannelEmail   = "email"
)

type sender func(ctx context.Context, cfg map[string]string, e Event) error

var senders = map[string]sender{
	ChannelWebhook: sendWebhook,
	ChannelNtfy:    sendNtfy,
	ChannelDiscord: sendDiscord,
	ChannelSlack:   sendSlack,
	ChannelEmail:   sendEmail,
}

// fields lists each kind's config keys; anything else is dropped on save.
var fields = map[string][]string{
	ChannelWebhook: {"url", "secret"},
	ChannelNtfy:    {"server", "topic", "token"},
	ChannelDiscord: {"url"},
	ChannelSlack:   {"url"},
	ChannelEmail:   {"host", "port", "security", "username", "password", "from", "to"},
}

// destinationFields decide where a kind's secret is sent. Changing one drops
// a stored secret, so an edit cannot send the old token or password to a new
// server, where a test would hand it over. The webhook secret only signs and
// is never sent, so its URL is not listed.
var destinationFields = map[string][]string{
	ChannelNtfy:  {"server"},
	ChannelEmail: {"host", "port", "security", "username"},
}

// secretFields are never returned by the API. A Discord or Slack webhook URL
// is itself the credential: anyone holding it can post to the channel.
var secretFields = map[string][]string{
	ChannelWebhook: {"secret"},
	ChannelNtfy:    {"token"},
	ChannelDiscord: {"url"},
	ChannelSlack:   {"url"},
	ChannelEmail:   {"password"},
}

func cleanConfig(kind string, in map[string]string) map[string]string {
	out := map[string]string{}
	for _, f := range fields[kind] {
		if v := strings.TrimSpace(in[f]); v != "" {
			out[f] = v
		}
	}
	return out
}

func redact(kind string, cfg map[string]string) map[string]any {
	out := map[string]any{}
	for _, f := range fields[kind] {
		out[f] = cfg[f]
	}
	for _, f := range secretFields[kind] {
		out[f] = ""
		out[f+"_set"] = cfg[f] != ""
	}
	return out
}

func checkHTTPURL(raw, field string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("%s must be a full http or https URL", field)
	}
	if u.User != nil {
		return fmt.Errorf("%s must not hold a user name or password", field)
	}
	return nil
}

func validateConfig(kind string, cfg map[string]string) error {
	switch kind {
	case ChannelWebhook:
		if cfg["url"] == "" {
			return errors.New("the webhook URL is required")
		}
		if err := checkHTTPURL(cfg["url"], "the webhook URL"); err != nil {
			return err
		}
		if len(cfg["secret"]) < 16 {
			return errors.New("the signing secret must be at least 16 characters")
		}
	case ChannelNtfy:
		if cfg["topic"] == "" {
			return errors.New("the ntfy topic is required")
		}
		if strings.ContainsAny(cfg["topic"], "/?# ") {
			return errors.New("the ntfy topic is one word, without slashes or spaces")
		}
		if s := cfg["server"]; s != "" {
			if err := checkHTTPURL(s, "the ntfy server"); err != nil {
				return err
			}
		}
	case ChannelDiscord:
		if cfg["url"] == "" {
			return errors.New("the Discord webhook URL is required")
		}
		if err := checkHTTPURL(cfg["url"], "the Discord webhook URL"); err != nil {
			return err
		}
	case ChannelSlack:
		if cfg["url"] == "" {
			return errors.New("the Slack webhook URL is required")
		}
		if err := checkHTTPURL(cfg["url"], "the Slack webhook URL"); err != nil {
			return err
		}
	case ChannelEmail:
		if cfg["host"] == "" {
			return errors.New("the SMTP server is required")
		}
		if p := cfg["port"]; p != "" {
			if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
				return errors.New("the SMTP port must be a number from 1 to 65535")
			}
		}
		switch cfg["security"] {
		case "", "starttls", "tls", "none":
		default:
			return errors.New("security must be starttls, tls or none")
		}
		if _, err := mail.ParseAddress(cfg["from"]); err != nil {
			return errors.New("the From address is not a valid email address")
		}
		if len(recipients(cfg["to"])) == 0 {
			return errors.New("at least one To address is required")
		}
		for _, r := range recipients(cfg["to"]) {
			if _, err := mail.ParseAddress(r); err != nil {
				return fmt.Errorf("%q is not a valid email address", r)
			}
		}
		if cfg["username"] != "" && cfg["password"] == "" {
			return errors.New("a username needs a password")
		}
		if cfg["password"] != "" && cfg["security"] == "none" {
			return errors.New("a password needs starttls or tls, so it is not sent in the clear")
		}
	default:
		return fmt.Errorf("unknown channel kind %q", kind)
	}
	return nil
}

func recipients(s string) []string {
	var out []string
	for _, r := range strings.Split(s, ",") {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

func send(ctx context.Context, kind string, cfg map[string]string, e Event) error {
	fn, ok := senders[kind]
	if !ok {
		return permanent(fmt.Errorf("unknown channel kind %q", kind))
	}
	err := fn(ctx, cfg, e)
	if errors.Is(err, ErrRefusedAddress) {
		return permanent(err)
	}
	return err
}

// httpClient dials through the address check, ignores proxy variables (a
// proxy would make the check meaningless) and does not follow redirects, so a
// public URL cannot bounce the request somewhere private.
var httpClient = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialFunc(ctx, network, addr, false)
		},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		MaxIdleConns:          2,
		IdleConnTimeout:       30 * time.Second,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func post(ctx context.Context, rawURL, contentType string, body []byte, header http.Header) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return permanent(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "Windlass")
	resp, err := httpClient.Do(req)
	if err != nil {
		// url.Error repeats the URL, which for Discord and Slack is the secret.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return err
	}
	defer resp.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	msg := fmt.Errorf("the receiver answered %d %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	// 4xx other than 408 and 429 means the request itself is wrong; sending
	// it again will not help.
	if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 408 && resp.StatusCode != 429 {
		return permanent(msg)
	}
	return msg
}

// sign returns the X-Windlass-Signature value: HMAC-SHA256 over
// "<timestamp>.<body>", hex encoded. The timestamp is in the signed text so a
// receiver can refuse a replayed old message.
func sign(secret, timestamp string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(timestamp))
	m.Write([]byte("."))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func sendWebhook(ctx context.Context, cfg map[string]string, e Event) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	ts := strconv.FormatInt(e.Time.Unix(), 10)
	h := http.Header{}
	h.Set("X-Windlass-Event", e.Kind)
	h.Set("X-Windlass-Timestamp", ts)
	h.Set("X-Windlass-Signature", sign(cfg["secret"], ts, body))
	return post(ctx, cfg["url"], "application/json", body, h)
}

func headline(e Event) string {
	t := e.Title
	if e.Project != "" && !strings.Contains(t, e.Project) {
		t = e.Project + ": " + t
	}
	return t
}

func sendNtfy(ctx context.Context, cfg map[string]string, e Event) error {
	server := strings.TrimRight(cfg["server"], "/")
	if server == "" {
		server = "https://ntfy.sh"
	}
	h := http.Header{}
	// ntfy reads headers as Latin-1; the RFC 2047 form keeps any other
	// character readable.
	h.Set("Title", mimeHeader(oneLine(headline(e))))
	h.Set("Tags", ntfyTag(e))
	if e.Kind == KindDeployFailed || e.Kind == KindRestartLoop || e.Kind == KindCertExpiring {
		if !e.Recovered {
			h.Set("Priority", "high")
		}
	}
	if t := cfg["token"]; t != "" {
		h.Set("Authorization", "Bearer "+t)
	}
	return post(ctx, server+"/"+url.PathEscape(cfg["topic"]), "text/plain; charset=utf-8", []byte(e.Message), h)
}

func ntfyTag(e Event) string {
	switch {
	case e.Recovered:
		return "white_check_mark"
	case e.Kind == KindTest || e.Kind == KindDeploySucceeded:
		return "information_source"
	default:
		return "warning"
	}
}

func sendDiscord(ctx context.Context, cfg map[string]string, e Event) error {
	body, _ := json.Marshal(map[string]any{
		"content":          "**" + headline(e) + "**\n" + e.Message,
		"allowed_mentions": map[string]any{"parse": []string{}},
	})
	return post(ctx, cfg["url"], "application/json", body, nil)
}

func sendSlack(ctx context.Context, cfg map[string]string, e Event) error {
	body, _ := json.Marshal(map[string]any{"text": "*" + headline(e) + "*\n" + e.Message})
	return post(ctx, cfg["url"], "application/json", body, nil)
}

func mimeHeader(s string) string {
	for _, r := range s {
		if r > 126 {
			return "=?UTF-8?B?" + b64(s) + "?="
		}
	}
	return s
}

func sendEmail(ctx context.Context, cfg map[string]string, e Event) error {
	host := cfg["host"]
	security := cfg["security"]
	if security == "" {
		security = "starttls"
	}
	port := cfg["port"]
	if port == "" {
		port = map[string]string{"starttls": "587", "tls": "465", "none": "25"}[security]
	}
	// A relay on the same host is the usual self-hosted setup, so loopback
	// is allowed here and nowhere else.
	conn, err := dialFunc(ctx, "tcp", net.JoinHostPort(host, port), true)
	if err != nil {
		return err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	_ = conn.SetDeadline(deadline)
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if security == "tls" {
		conn = tls.Client(conn, tlsCfg)
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer c.Close()
	if security == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return permanent(fmt.Errorf("the server does not offer STARTTLS"))
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if cfg["username"] != "" {
		// PlainAuth refuses to send over an unencrypted link except to
		// localhost, which matches the rule in validateConfig.
		if err := c.Auth(smtp.PlainAuth("", cfg["username"], cfg["password"], host)); err != nil {
			return err
		}
	}
	from, _ := mail.ParseAddress(cfg["from"])
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	to := recipients(cfg["to"])
	for _, r := range to {
		addr, err := mail.ParseAddress(r)
		if err != nil {
			return permanent(err)
		}
		if err := c.Rcpt(addr.Address); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	var msg strings.Builder
	msg.WriteString("From: " + from.String() + "\r\n")
	msg.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	msg.WriteString("Subject: " + mimeHeader(oneLine("[Windlass] "+headline(e))) + "\r\n")
	msg.WriteString("Date: " + e.Time.Format(time.RFC1123Z) + "\r\n")
	msg.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	msg.WriteString(strings.ReplaceAll(e.Message, "\n", "\r\n") + "\r\n")
	if _, err := io.WriteString(w, msg.String()); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// oneLine keeps a header to one line, so a title cannot add headers.
func oneLine(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}
