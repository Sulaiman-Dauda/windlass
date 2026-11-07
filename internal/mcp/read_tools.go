package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// How long get_deployment waits for the event stream. A finished
// deployment's stream replays its stored events and ends at once; a running
// one streams until the client leaves, so we take what has arrived.
const (
	eventsFinishedTimeout = 15 * time.Second
	eventsRunningTimeout  = 2 * time.Second
)

// apiDeploymentsLimit is how many deployments the REST list returns.
const apiDeploymentsLimit = 50

type object = map[string]json.RawMessage

func decodeList(raw json.RawMessage) ([]object, error) {
	var list []object
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("unexpected answer from Windlass: %v", err)
	}
	if list == nil {
		list = []object{}
	}
	return list, nil
}

func capList[T any](items []T, max int) ([]T, bool) {
	if len(items) > max {
		return items[:max], true
	}
	return items, false
}

func stringField(o object, key string) string {
	var s string
	if raw, ok := o[key]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

func setString(o object, key, value string) {
	b, _ := json.Marshal(value)
	o[key] = b
}

// cleanProject strips credentials from a project's Git address.
func cleanProject(o object) object {
	if repo := stringField(o, "git_repo"); repo != "" {
		setString(o, "git_repo", stripURLCredentials(repo))
	}
	return o
}

// scrubError scrubs the error text of a deployment or backup, which is
// Compose or dump-tool output and can quote a value.
func scrubError(o object, sc *scrubber) object {
	if msg := stringField(o, "error"); msg != "" {
		setString(o, "error", sc.scrub(msg))
	}
	return o
}

func listProjects(t *toolCtx) (any, error) {
	raw, err := t.getJSON("/projects", nil)
	if err != nil {
		return nil, err
	}
	list, err := decodeList(raw)
	if err != nil {
		return nil, err
	}
	total := len(list)
	list, truncated := capList(list, MaxListItems)
	for _, p := range list {
		cleanProject(p)
	}
	return map[string]any{"projects": list, "total": total, "truncated": truncated}, nil
}

func getProject(t *toolCtx) (any, error) {
	name := t.str("project")
	raw, err := t.getJSON(projectPath(name), nil)
	if err != nil {
		return nil, err
	}
	var project object
	if err := json.Unmarshal(raw, &project); err != nil {
		return nil, fmt.Errorf("unexpected answer from Windlass: %v", err)
	}
	out := map[string]any{"project": cleanProject(project)}
	sc := t.s.scrubberOrWithhold(t, name)

	raw, err = t.getJSON(projectPath(name, "services"), nil)
	if err != nil {
		return nil, err
	}
	var services struct {
		Services []object `json:"services"`
		Note     string   `json:"note"`
	}
	if err := json.Unmarshal(raw, &services); err != nil {
		return nil, fmt.Errorf("unexpected answer from Windlass: %v", err)
	}
	if services.Services == nil {
		services.Services = []object{}
	}
	svc, svcCut := capList(services.Services, MaxListItems)
	out["services"] = svc
	if svcCut {
		out["services_truncated"] = true
	}
	if services.Note != "" {
		out["services_note"] = sc.scrub(services.Note)
	}

	raw, err = t.getJSON(projectPath(name, "domains"), nil)
	if err != nil {
		return nil, err
	}
	domains, err := decodeList(raw)
	if err != nil {
		return nil, err
	}
	domains, domCut := capList(domains, MaxListItems)
	out["domains"] = domains
	if domCut {
		out["domains_truncated"] = true
	}

	raw, err = t.getJSON(projectPath(name, "deployments"), nil)
	if err != nil {
		return nil, err
	}
	deployments, err := decodeList(raw)
	if err != nil {
		return nil, err
	}
	if len(deployments) > 0 {
		out["last_deployment"] = scrubError(deployments[0], sc)
	} else {
		out["last_deployment"] = nil
	}
	return out, nil
}

func listDeployments(t *toolCtx) (any, error) {
	name := t.str("project")
	limit := int(t.int("limit", 20))
	raw, err := t.getJSON(projectPath(name, "deployments"), nil)
	if err != nil {
		return nil, err
	}
	list, err := decodeList(raw)
	if err != nil {
		return nil, err
	}
	sc := t.s.scrubberOrWithhold(t, name)
	all := len(list)
	list, truncated := capList(list, limit)
	for _, d := range list {
		scrubError(d, sc)
	}
	out := map[string]any{"project": name, "deployments": list, "returned": len(list), "truncated": truncated}
	if all >= apiDeploymentsLimit {
		out["truncated"] = true
		out["note"] = fmt.Sprintf("Windlass lists only the newest %d deployments", apiDeploymentsLimit)
	}
	return out, nil
}

type deploymentEvent struct {
	Seq     int64  `json:"seq"`
	Type    string `json:"type"`
	Message string `json:"message"`
	Ts      string `json:"ts"`
}

func getDeployment(t *toolCtx) (any, error) {
	name := t.str("project")
	number := t.int("number", 0)
	path := projectPath(name, "deployments", queryInt(number))
	raw, err := t.getJSON(path, nil)
	if err != nil {
		return nil, err
	}
	var d object
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("unexpected answer from Windlass: %v", err)
	}
	finished := stringField(d, "finished_at") != ""
	timeout := eventsRunningTimeout
	if finished {
		timeout = eventsFinishedTimeout
	}
	res, err := t.stream(path+"/events", nil, timeout, MaxLogLines)
	if err != nil {
		return nil, err
	}
	sc, scErr := t.s.scrubberFor(t.r.Context(), name)
	if scErr != nil {
		// The record is safe to show; its error text and events are not.
		sc = withholdingScrubber()
	}
	scrubError(d, sc)
	out := map[string]any{"deployment": d, "still_running": !finished}
	if scErr != nil {
		out["events"] = []deploymentEvent{}
		out["events_withheld"] = scErr.Error()
		return out, nil
	}

	events := make([]deploymentEvent, 0, len(res.events))
	for _, ev := range res.events {
		var e deploymentEvent
		if json.Unmarshal(ev.data, &e) != nil {
			continue
		}
		e.Message = sc.scrub(e.Message)
		events = append(events, e)
	}
	// Keep the newest events within MaxLogBytes of message text: the end of
	// a failed deployment is where the reason is.
	truncated := res.truncated
	total, start := 0, len(events)
	for start > 0 {
		n := len(events[start-1].Message) + 64
		if total+n > MaxLogBytes {
			truncated = true
			break
		}
		total += n
		start--
	}
	out["events"] = events[start:]
	out["events_truncated"] = truncated
	return out, nil
}

func getLogs(t *toolCtx) (any, error) {
	name := t.str("project")
	service := t.str("service")
	tail := t.int("tail", 100)

	if service == "" {
		raw, err := t.getJSON(projectPath(name, "services"), nil)
		if err != nil {
			return nil, err
		}
		var services struct {
			Services []struct {
				Service string `json:"service"`
			} `json:"services"`
		}
		if err := json.Unmarshal(raw, &services); err != nil {
			return nil, fmt.Errorf("unexpected answer from Windlass: %v", err)
		}
		seen := map[string]bool{}
		var names []string
		for _, s := range services.Services {
			if s.Service != "" && !seen[s.Service] {
				seen[s.Service] = true
				names = append(names, s.Service)
			}
		}
		switch {
		case len(names) == 1:
			service = names[0]
		case len(names) > 1:
			sort.Strings(names)
			return nil, fmt.Errorf("%s has %d services; name one in service: %s",
				name, len(names), strings.Join(names, ", "))
		}
	}

	q := url.Values{"follow": {"false"}, "tail": {queryInt(tail)}}
	if service != "" {
		q.Set("service", service)
	}
	res, err := t.stream(projectPath(name, "logs"), q, defaultCallTimeout, MaxLogLines+16)
	if err != nil {
		return nil, err
	}
	sc, err := t.s.scrubberFor(t.r.Context(), name)
	if err != nil {
		return nil, err
	}

	var lines []string
	var streamErr string
	for _, ev := range res.events {
		var payload struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(ev.data, &payload) != nil {
			continue
		}
		switch ev.typ {
		case "log":
			lines = append(lines, sc.scrub(strings.TrimRight(payload.Text, "\r\n")))
		case "error":
			streamErr = sc.scrub(payload.Text)
		}
	}
	lines, cut := tailLines(lines, MaxLogLines, MaxLogBytes)
	out := map[string]any{
		"project":   name,
		"service":   service,
		"lines":     len(lines),
		"truncated": cut || res.truncated,
		"log":       strings.Join(lines, "\n"),
	}
	if service == "" {
		out["service"] = nil
	}
	if streamErr != "" {
		out["error"] = streamErr
	}
	return out, nil
}

func getStaging(t *toolCtx) (any, error) {
	name := t.str("project")
	raw, err := t.getJSON(projectPath(name, "staging"), nil)
	if isNotFound(err) {
		// The route says "no staging" for a missing project too; tell the
		// two apart so the model is not told a typo has no staging.
		if _, perr := t.getJSON(projectPath(name), nil); perr != nil {
			return nil, perr
		}
		return map[string]any{"project": name, "has_staging": false}, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": name, "has_staging": true, "staging": raw}, nil
}

func listBackups(t *toolCtx) (any, error) {
	name := t.str("project")
	limit := int(t.int("limit", 50))
	raw, err := t.getJSON(projectPath(name, "backups"), nil)
	if err != nil {
		return nil, err
	}
	list, err := decodeList(raw)
	if err != nil {
		return nil, err
	}
	total := len(list)
	list, truncated := capList(list, limit)
	sc := t.s.scrubberOrWithhold(t, name)
	for _, b := range list {
		scrubError(b, sc)
	}
	schedule, err := t.getJSON(projectPath(name, "backups", "schedule"), nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"project": name, "backups": list, "total": total, "truncated": truncated, "schedule": schedule,
	}, nil
}
