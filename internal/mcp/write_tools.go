package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// How long a write tool waits for its API call, and how long the call may
// run in all. Deploy, rollback and promote only queue a deployment, so they
// answer at once. The restart route stops itself at 2 minutes. A backup has
// no limit in the panel or the scheduler, so the bound here is only a
// backstop against a call that never returns, set well past any real one.
// Variables so a test can shorten them.
var (
	queueWait    = defaultCallTimeout
	queueLimit   = time.Minute
	restartWait  = time.Minute
	restartLimit = 3 * time.Minute
	backupWait   = time.Minute
	backupLimit  = 12 * time.Hour
)

// backupGap is how long create_backup waits after a manual backup that did
// not fail. Manual backups count toward a schedule's retention, so an agent
// in a loop could otherwise push every older backup out at the next prune;
// the panel can still take one at any time.
const backupGap = 10 * time.Minute

// recentManualBackup returns the newest manual backup of project made
// within backupGap that did not fail, if there is one.
func (t *toolCtx) recentManualBackup(project string, now time.Time) (object, error) {
	raw, err := t.getJSON(projectPath(project, "backups"), nil)
	if err != nil {
		return nil, err
	}
	list, err := decodeList(raw)
	if err != nil {
		return nil, err
	}
	return newestRecentManual(list, now), nil
}

func newestRecentManual(list []object, now time.Time) object {
	var newest object
	var newestAt time.Time
	for _, b := range list {
		if stringField(b, "kind") != "manual" || stringField(b, "status") == "failed" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, stringField(b, "created_at"))
		if err != nil || now.Sub(at) >= backupGap || at.Before(newestAt) {
			continue
		}
		newest, newestAt = b, at
	}
	return newest
}

// stillRunning is the status of a write that had not finished in its wait.
const stillRunning = "still_running"

const scopeErrorFmt = "%s needs a token that can deploy: scope deploy, write or admin, owned by a member or admin. " +
	"This token's scope is %s%s. Ask the user for such a token (Settings, API tokens) or to make this change in the panel"

// tokenInfo is what /auth/tokens/current says about the caller's token.
type tokenInfo struct {
	Scope     string `json:"scope"`
	CanDeploy bool   `json:"can_deploy"`
	User      struct {
		Role string `json:"role"`
	} `json:"user"`
}

func (s *Server) currentToken(r *http.Request) (tokenInfo, error) {
	t := &toolCtx{s: s, r: r}
	raw, err := t.getJSON("/auth/tokens/current", nil)
	if err != nil {
		return tokenInfo{}, err
	}
	var info tokenInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return tokenInfo{}, fmt.Errorf("unexpected answer from Windlass: %v", err)
	}
	return info, nil
}

// visibleTools is the catalogue filtered to what the caller's token can
// use: the deploy-scope tools only for a token the deploy routes accept.
// If the token cannot be looked up, only the read tools are shown; the
// calls themselves are checked again by the router either way.
func (s *Server) visibleTools(r *http.Request) []toolDef {
	info, err := s.currentToken(r)
	canDeploy := err == nil && info.CanDeploy
	out := make([]toolDef, 0, len(s.tools))
	for _, t := range s.tools {
		if t.minScope == scopeDeploy && !canDeploy {
			continue
		}
		out = append(out, t)
	}
	return out
}

// checkScope refuses a deploy-scope tool for a token that cannot deploy,
// before anything is sent, with an error the model can pass on.
func (s *Server) checkScope(r *http.Request, def *toolDef) error {
	if def.minScope != scopeDeploy {
		return nil
	}
	info, err := s.currentToken(r)
	if err != nil {
		return fmt.Errorf("could not check what this token may do: %v", err)
	}
	if info.CanDeploy {
		return nil
	}
	owner := ""
	if info.Scope != "read" && info.User.Role == "viewer" {
		owner = ", but its owner is a viewer"
	}
	return fmt.Errorf(scopeErrorFmt, def.Name, info.Scope, owner)
}

// writeResult is the outcome of a write sent with sendWrite.
type writeResult struct {
	raw json.RawMessage
	// running is true when the call had not finished within the wait. It
	// carries on; the tool says how to check on it.
	running bool
}

// sendWrite sends one write to the API, detached from the MCP request so
// that a client hanging up never cuts a restart or backup off halfway. It
// waits up to wait for the answer; the call itself may run up to limit.
//
// The mcp.call audit line is written when the route answers 2xx, at
// whatever point that happens, so it pairs with the route's own line:
// neither is written for a refusal or a failure.
func (t *toolCtx) sendWrite(method, path string, body any, wait, limit time.Duration, auditProject string) (writeResult, error) {
	var b []byte
	if body != nil {
		var err error
		if b, err = json.Marshal(body); err != nil {
			return writeResult{}, err
		}
	}
	req, done, err := t.newAPIRequest(method, path, nil, b, limit, true)
	if err != nil {
		return writeResult{}, err
	}
	rec := newRecorder()
	var panicked any
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer done()
		defer func() { panicked = recover() }()
		t.s.api.ServeHTTP(rec, req)
		rec.mu.Lock()
		ok := rec.status >= 200 && rec.status <= 299
		rec.mu.Unlock()
		if ok && t.s.cfg.Audit != nil {
			t.s.cfg.Audit(t.r, t.tool.Name, auditProject)
		}
	}()

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-finished:
	case <-timer.C:
		return writeResult{running: true}, nil
	case <-t.r.Context().Done():
		// Nobody is left to read the answer; the write carries on.
		return writeResult{}, t.r.Context().Err()
	}
	if panicked != nil {
		return writeResult{}, fmt.Errorf("the API failed while handling %s", t.tool.Name)
	}
	raw, err := rec.jsonResult()
	if err != nil {
		return writeResult{}, t.scrubAPIError(err, auditProject)
	}
	return writeResult{raw: raw}, nil
}

// scrubAPIError scrubs the message of a server-side failure, which can quote
// Compose or dump-tool output and with it an env value.
func (t *toolCtx) scrubAPIError(err error, project string) error {
	e, ok := err.(*apiError)
	if !ok || e.status < 500 {
		return err
	}
	sc := t.s.scrubberOrWithhold(t, project)
	scrubbed := *e
	scrubbed.message = sc.scrub(e.message)
	return &scrubbed
}

// deploymentAnswer shapes the reply of the three tools that queue a
// deployment.
func (t *toolCtx) deploymentAnswer(project string, res writeResult, action string) (any, error) {
	if res.running {
		return map[string]any{
			"project": project, "status": stillRunning,
			"next": "Windlass has not answered yet. Call list_deployments for " + project + " in a minute to see the new deployment.",
		}, nil
	}
	var d object
	if err := json.Unmarshal(res.raw, &d); err != nil {
		return nil, fmt.Errorf("unexpected answer from Windlass: %v", err)
	}
	scrubError(d, t.s.scrubberOrWithhold(t, project))
	var number int64
	_ = json.Unmarshal(d["number"], &number)
	return map[string]any{
		"project": project, "result": action, "deployment": d,
		"next": fmt.Sprintf("The deployment runs in the background. Call get_deployment with project %s and number %d "+
			"to follow it until its status is succeeded or failed.", project, number),
	}, nil
}

func deployProject(t *toolCtx) (any, error) {
	name := t.str("project")
	res, err := t.sendWrite(http.MethodPost, projectPath(name, "deployments"), nil, queueWait, queueLimit, name)
	if err != nil {
		return nil, err
	}
	return t.deploymentAnswer(name, res, "deploying")
}

func rollbackProject(t *toolCtx) (any, error) {
	name := t.str("project")
	number := strconv.FormatInt(t.int("number", 0), 10)
	res, err := t.sendWrite(http.MethodPost, projectPath(name, "deployments", number, "rollback"), nil,
		queueWait, queueLimit, name)
	if err != nil {
		return nil, err
	}
	return t.deploymentAnswer(name, res, "rolling back to #"+number)
}

func restartProject(t *toolCtx) (any, error) {
	name := t.str("project")
	res, err := t.sendWrite(http.MethodPost, projectPath(name, "actions", "restart"), nil,
		restartWait, restartLimit, name)
	if err != nil {
		return nil, err
	}
	if res.running {
		return map[string]any{
			"project": name, "status": stillRunning,
			"next": "The restart is still going. Call get_project for " + name + " in a minute to see the services' state.",
		}, nil
	}
	return map[string]any{
		"project": name, "result": "restarted",
		"next": "Call get_project for " + name + " to check every service is running again.",
	}, nil
}

// promoteStaging accepts the live project or its staging, as get_staging
// does, and promotes on the staging, where the API route lives.
func promoteStaging(t *toolCtx) (any, error) {
	name := t.str("project")
	raw, err := t.getJSON(projectPath(name, "staging"), nil)
	if isNotFound(err) {
		if _, perr := t.getJSON(projectPath(name), nil); perr != nil {
			return nil, perr
		}
		return nil, fmt.Errorf("%s has no staging, so there is nothing to promote. Use deploy to deploy it directly", name)
	}
	if err != nil {
		return nil, err
	}
	var st struct {
		Live       string `json:"live"`
		Staging    string `json:"staging"`
		Promotable *struct {
			Number int64 `json:"number"`
		} `json:"promotable"`
		PromoteBlocked string `json:"promote_blocked"`
	}
	if err := json.Unmarshal(raw, &st); err != nil || st.Staging == "" || st.Live == "" {
		return nil, fmt.Errorf("unexpected staging answer from Windlass")
	}
	number := t.int("number", 0)
	if number == 0 {
		if st.PromoteBlocked != "" || st.Promotable == nil {
			reason := st.PromoteBlocked
			if reason == "" {
				reason = "staging has no succeeded deployment yet"
			}
			return nil, fmt.Errorf("nothing to promote from %s to %s: %s", st.Staging, st.Live, reason)
		}
		number = st.Promotable.Number
	}
	n := strconv.FormatInt(number, 10)
	res, err := t.sendWrite(http.MethodPost, projectPath(st.Staging, "deployments", n, "promote"), nil,
		queueWait, queueLimit, st.Live)
	if err != nil {
		return nil, err
	}
	return t.deploymentAnswer(st.Live, res, fmt.Sprintf("promoting %s #%s", st.Staging, n))
}

func createBackup(t *toolCtx) (any, error) {
	name := t.str("project")
	// Held until the POST has answered or its wait has passed; the backup's
	// row exists from the start of the POST, so the next check sees it.
	lock, _ := t.s.backupLocks.LoadOrStore(name, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
	recent, err := t.recentManualBackup(name, time.Now())
	if err != nil {
		return nil, err
	}
	if recent != nil {
		return nil, fmt.Errorf("%s already has a manual backup from the last %d minutes (id %s, %s, %s); use that one. "+
			"Another can be taken here after that, or now in the panel", name, int(backupGap.Minutes()),
			string(recent["id"]), stringField(recent, "created_at"), stringField(recent, "status"))
	}
	var body any
	if dest := t.str("destination"); dest != "" {
		body = map[string]string{"destination": dest}
	}
	res, err := t.sendWrite(http.MethodPost, projectPath(name, "backups"), body, backupWait, backupLimit, name)
	if e, ok := err.(*apiError); ok && e.status == http.StatusBadGateway {
		// The backup ran and failed: the API sends its row, error included.
		var b object
		if json.Unmarshal(e.body, &b) == nil && len(b) > 0 {
			scrubError(b, t.s.scrubberOrWithhold(t, name))
			msg := stringField(b, "error")
			if msg == "" {
				msg = "no reason recorded"
			}
			return nil, fmt.Errorf("the backup of %s failed: %s", name, msg)
		}
	}
	if err != nil {
		return nil, err
	}
	if res.running {
		return map[string]any{
			"project": name, "status": stillRunning,
			"next": "The backup is still running and will carry on. Call list_backups for " + name + " in a few minutes.",
		}, nil
	}
	var b object
	if err := json.Unmarshal(res.raw, &b); err != nil {
		return nil, fmt.Errorf("unexpected answer from Windlass: %v", err)
	}
	scrubError(b, t.s.scrubberOrWithhold(t, name))
	return map[string]any{
		"project": name, "result": "backed up", "backup": b,
		"next": "Restoring is done in the panel, not through this server.",
	}, nil
}
