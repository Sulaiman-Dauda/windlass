package projects

import (
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/windlass-dev/windlass/internal/agent"
)

// Issue is one finding of the staging isolation check.
type Issue struct {
	Service string `json:"service,omitempty"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// CheckIsolation finds what in a resolved compose file would make a second
// copy of the project share something with the first. Compose's -p scopes
// containers, networks and volumes by project, but not the settings below,
// so a staging copy that has any of them would collide with live or write
// into its data. Blockers stop the copy; warnings are listed so the person
// creating staging knows the copy reads a path on the host.
func CheckIsolation(cfg agent.ResolvedConfig, project string) (blockers, warnings []Issue) {
	names := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		svc := cfg.Services[name]
		if svc.ContainerName != "" {
			blockers = append(blockers, Issue{Service: name,
				Message: fmt.Sprintf("sets container_name: %s, and two containers cannot share a name", svc.ContainerName),
				Fix:     "Remove container_name. Compose names the container itself."})
		}
		for _, p := range svc.PublishedPorts {
			blockers = append(blockers, Issue{Service: name,
				Message: fmt.Sprintf("publishes host port %s, which live already holds", p.Published),
				Fix:     "Remove the ports entry and reach the service through a domain, or use expose."})
		}
		switch mode := svc.NetworkMode; {
		case mode == "host":
			blockers = append(blockers, Issue{Service: name,
				Message: "uses network_mode: host, so its ports are the host's",
				Fix:     "Remove network_mode and reach the service through a domain."})
		case strings.HasPrefix(mode, "container:"):
			blockers = append(blockers, Issue{Service: name,
				Message: fmt.Sprintf("joins another container's network with network_mode: %s", mode),
				Fix:     "Use a service in the same compose file, or remove network_mode."})
		}
		for _, key := range svc.Networks {
			n, ok := cfg.Networks[key]
			switch {
			case !ok:
			case n.External:
				blockers = append(blockers, Issue{Service: name,
					Message: fmt.Sprintf("joins external network %s, where live's services answer to the same names", n.Name),
					Fix:     "Use a network the compose file defines, or remove the external network."})
			case n.Name != "" && n.Name != project+"_"+key:
				blockers = append(blockers, Issue{Service: name,
					Message: fmt.Sprintf("joins network %s, whose fixed name: means live's services answer on it too", n.Name),
					Fix:     "Remove name: from the network so Compose names it per project."})
			}
		}
		// A fixed image name on a built service is one tag for both
		// projects. Running containers keep their image, but anything that
		// recreates live without building would start staging's build.
		if svc.Build && svc.Image != "" && !strings.HasPrefix(svc.Image, project+"-") {
			warnings = append(warnings, Issue{Service: name,
				Message: fmt.Sprintf("builds into image %s, which a copy would build into too", svc.Image),
				Fix:     "Use image: ${COMPOSE_PROJECT_NAME}-" + name + ", or drop image: so Compose names it per project."})
		}
		for _, m := range svc.Mounts {
			switch m.Type {
			case "volume":
				v, ok := cfg.Volumes[m.Source]
				switch {
				case !ok:
				case v.External:
					blockers = append(blockers, Issue{Service: name,
						Message: fmt.Sprintf("mounts external volume %s, which live writes to", v.Name),
						Fix:     "Use a volume the compose file defines, without external."})
				case v.Name != "" && v.Name != project+"_"+m.Source:
					blockers = append(blockers, Issue{Service: name,
						Message: fmt.Sprintf("mounts volume %s, whose fixed name: means a copy would share it", v.Name),
						Fix:     "Remove name: from the volume so Compose names it per project."})
				}
			case "bind":
				if insideDir(cfg.WorkingDir, m.Source) {
					continue
				}
				if m.ReadOnly {
					warnings = append(warnings, Issue{Service: name,
						Message: fmt.Sprintf("reads %s from the host, read-only", m.Source)})
					continue
				}
				blockers = append(blockers, Issue{Service: name,
					Message: fmt.Sprintf("writes to %s on the host, which live uses too", m.Source),
					Fix:     "Use a named volume, a path inside the project, or mount it :ro."})
			}
		}
	}
	return blockers, warnings
}

// FixedVolumes lists, sorted, the volumes the compose file declares with a
// name: other than the one Compose would give them. docker compose down -v
// removes every declared volume that is not external, mounted or not, so
// deleting a staging with one could remove live's data.
func FixedVolumes(cfg agent.ResolvedConfig, project string) []string {
	var names []string
	for key, v := range cfg.Volumes {
		if !v.External && v.Name != "" && v.Name != project+"_"+key {
			names = append(names, v.Name)
		}
	}
	sort.Strings(names)
	return names
}

// insideDir reports whether path is dir or below it. An empty dir holds
// nothing.
func insideDir(dir, path string) bool {
	if dir == "" || path == "" {
		return false
	}
	dir, path = filepath.Clean(dir), filepath.Clean(path)
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// HealthURLsOnHosts lists the health checks whose URL names one of hosts. A
// staging copy with one would pass or fail on live's health, not its own.
func HealthURLsOnHosts(cfg agent.ResolvedConfig, hosts []string) []Issue {
	var issues []Issue
	for _, check := range cfg.HealthChecks {
		u, err := url.Parse(check.URL)
		if err != nil {
			continue
		}
		for _, h := range hosts {
			if strings.EqualFold(u.Hostname(), h) {
				issues = append(issues, Issue{Service: check.Service,
					Message: fmt.Sprintf("health URL %s checks live's domain", check.URL),
					Fix:     "Point windlass.health.url at the staging domain."})
			}
		}
	}
	return issues
}

// HostnameRewrite replaces one live hostname with its staging hostname.
type HostnameRewrite struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// RewriteHostnames replaces each From with its To wherever it stands as a
// whole hostname: not inside a longer name such as api.app.example.com or
// staging-app.example.com. It returns the text and how many it replaced.
func RewriteHostnames(text string, rewrites []HostnameRewrite) (string, int) {
	total := 0
	for _, r := range rewrites {
		if r.From == "" || r.From == r.To {
			continue
		}
		var b strings.Builder
		rest := text
		for {
			i := strings.Index(rest, r.From)
			if i < 0 {
				b.WriteString(rest)
				break
			}
			end := i + len(r.From)
			if hostBoundaryBefore(rest, i) && hostBoundaryAfter(rest, end) {
				b.WriteString(rest[:i])
				b.WriteString(r.To)
				total++
			} else {
				b.WriteString(rest[:end])
			}
			rest = rest[end:]
		}
		text = b.String()
	}
	return text, total
}

func hostChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-'
}

func hostBoundaryBefore(s string, i int) bool {
	return i == 0 || !(hostChar(s[i-1]) || s[i-1] == '.')
}

// hostBoundaryAfter allows a full stop that ends a sentence but not one that
// carries the name on, as in app.example.com.evil.
func hostBoundaryAfter(s string, end int) bool {
	if end == len(s) {
		return true
	}
	if s[end] == '.' {
		return end+1 == len(s) || !hostChar(s[end+1])
	}
	return !hostChar(s[end])
}

// outsideWorldWords are the parts of an environment key that mark a value
// reaching people or services outside the server: mail, SMS, payments,
// webhooks, storage and automation.
var outsideWorldWords = map[string]bool{
	"MAIL": true, "MAILER": true, "EMAIL": true, "SMTP": true, "IMAP": true,
	"SENDGRID": true, "MAILGUN": true, "POSTMARK": true, "SES": true, "RESEND": true,
	"BREVO": true, "SENDINBLUE": true, "MAILJET": true, "MAILCHIMP": true, "MANDRILL": true,
	"SMS": true, "TWILIO": true, "VONAGE": true, "NEXMO": true, "MESSAGEBIRD": true,
	"STRIPE": true, "PAYPAL": true, "BRAINTREE": true, "SQUARE": true, "GOCARDLESS": true,
	"ADYEN": true, "MOLLIE": true, "KLARNA": true, "PADDLE": true, "SUMUP": true,
	"WEBHOOK": true, "WEBHOOKS": true, "HOOK": true, "CALLBACK": true,
	"S3": true, "AWS": true, "BUCKET": true, "STORAGE": true, "R2": true, "SPACES": true,
	"GCS": true, "B2": true, "MINIO": true, "CLOUDINARY": true,
	"ZAPIER": true, "SLACK": true, "DISCORD": true, "PUSHER": true, "ONESIGNAL": true,
	"FCM": true, "APNS": true, "PUSH": true,
}

// OutsideWorldKeys lists, sorted, the environment keys whose values probably
// let an application reach real people or services: a staging copy would
// email real customers or take real payments with them unchanged.
func OutsideWorldKeys(vars map[string]string) []string {
	var keys []string
	for key := range vars {
		for _, word := range strings.FieldsFunc(strings.ToUpper(key), func(r rune) bool { return r == '_' || r == '-' }) {
			if outsideWorldWords[word] {
				keys = append(keys, key)
				break
			}
		}
	}
	sort.Strings(keys)
	return keys
}
