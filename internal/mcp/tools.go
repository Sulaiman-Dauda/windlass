package mcp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Output limits. A tool that hits one says so in its result rather than
// silently cutting the list.
const (
	MaxListItems = 200
	MaxLogLines  = 500
	MaxLogBytes  = 64 << 10
)

// Tool is one entry in the catalogue, as sent in tools/list.
type Tool struct {
	Name         string          `json:"name"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Annotations  Annotations     `json:"annotations"`
}

// Annotations are hints clients use to decide when to ask the user. Every
// tool sets all four explicitly, because the spec defaults assume the worst
// (not read-only, destructive) and Claude's connector review requires them.
type Annotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint"`
	DestructiveHint bool `json:"destructiveHint"`
	IdempotentHint  bool `json:"idempotentHint"`
	OpenWorldHint   bool `json:"openWorldHint"`
}

// Tools returns the catalogue in a stable order: the read-only tools
// list_projects, get_project, list_deployments, get_deployment, get_logs,
// get_staging and list_backups, then the deploy-scope tools deploy,
// restart, rollback, promote and create_backup. No tool returns env values
// or files under a project's env path, none writes files or env, and none
// exists for routes that refuse tokens.
func Tools() []Tool {
	defs := catalogue()
	out := make([]Tool, len(defs))
	for i, d := range defs {
		out[i] = d.Tool
	}
	return out
}

// toolDef is a catalogue entry plus what the server needs to run it. The
// input schema is generated from params, so the schema a client sees and
// the checks the server applies cannot drift apart.
type toolDef struct {
	Tool
	params []param
	run    func(*toolCtx) (any, error)
	// minScope is the narrowest token scope that can use the tool: "read"
	// or "deploy". tools/list hides a tool the token cannot use.
	minScope string
}

// Token scopes a tool can need.
const (
	scopeRead   = "read"
	scopeDeploy = "deploy"
)

type paramKind string

const (
	kindString  paramKind = "string"
	kindInteger paramKind = "integer"
	kindBoolean paramKind = "boolean"
)

type param struct {
	name        string
	kind        paramKind
	description string
	required    bool
	// Integer bounds, inclusive. Used only when kind is integer.
	min, max int64
	// pattern restricts a string. Used only when kind is string.
	pattern *regexp.Regexp
	// maxLen bounds a string's length in bytes.
	maxLen int
	// mustBeTrue is for confirm: a boolean that is refused unless true.
	mustBeTrue bool
}

// projectNameRE is the Windlass project name rule (agent.ValidProjectName),
// repeated here because this package imports nothing from Windlass.
var projectNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// destinationRE is the backup destinations the API accepts.
var destinationRE = regexp.MustCompile(`^(local|s3)$`)

// serviceNameRE is the Compose service name rule.
var serviceNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

var projectParam = param{
	name: "project", kind: kindString, required: true, pattern: projectNameRE, maxLen: 63,
	description: "Project name, as returned by list_projects.",
}

var readOnly = Annotations{ReadOnlyHint: true, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: false}

// destructive is for tools that change what is running: Claude and other
// clients ask the user before each call.
var destructive = Annotations{ReadOnlyHint: false, DestructiveHint: true, IdempotentHint: false, OpenWorldHint: false}

// confirmParam is required on every destructive tool, because annotations
// are only hints and not every client asks the user.
var confirmParam = param{
	name: "confirm", kind: kindBoolean, required: true, mustBeTrue: true,
	description: "Must be true. Before setting it, tell the user exactly what this call will change and " +
		"ask; set it only once they say yes in reply. A request to make the change (\"redeploy X\") is not " +
		"that yes: ask first, even then.",
}

// stagingHint is shared by the tools that change a live project.
const stagingHint = "If the project has a staging (list_projects shows it), prefer changing the staging and " +
	"then promote, unless the user asked for live."

// catalogue builds the tool table in the order tools/list returns it.
func catalogue() []toolDef {
	defs := []toolDef{
		{
			Tool: Tool{
				Name:  "list_projects",
				Title: "List projects",
				Description: "List the Docker Compose projects on this Windlass server the token can see: " +
					"name, source (git or files), Git repo and branch, auto-deploy, and staging links " +
					"(staging names this project's staging, staging_of names the live project a staging belongs to).",
				Annotations: readOnly,
			},
			run: listProjects,
		},
		{
			Tool: Tool{
				Name:  "get_project",
				Title: "Get a project",
				Description: "Get one project's current state: its Compose services with container state, " +
					"health, image, ports and limits; its domains; and its latest deployment. " +
					"Use this first when asked whether something is up or healthy.",
				Annotations: readOnly,
			},
			params: []param{projectParam},
			run:    getProject,
		},
		{
			Tool: Tool{
				Name:  "list_deployments",
				Title: "List deployments",
				Description: "List a project's deployments, newest first: number, status, trigger, Git commit, " +
					"error and timings. Windlass keeps the newest 50 in this list.",
				Annotations: readOnly,
			},
			params: []param{projectParam, {
				name: "limit", kind: kindInteger, min: 1, max: 50,
				description: "How many deployments to return, newest first. Default 20, at most 50.",
			}},
			run: listDeployments,
		},
		{
			Tool: Tool{
				Name:  "get_deployment",
				Title: "Get a deployment",
				Description: "Get one deployment and its event log (build, pull, up and health steps with messages). " +
					"Use it to find out why a deployment failed. Event text has env values and API tokens " +
					"replaced with [redacted]. For a deployment still running, the events so far are returned.",
				Annotations: readOnly,
			},
			params: []param{projectParam, {
				name: "number", kind: kindInteger, required: true, min: 1, max: 1 << 53,
				description: "Deployment number, as shown by list_deployments.",
			}},
			run: getDeployment,
		},
		{
			Tool: Tool{
				Name:  "get_logs",
				Title: "Read container logs",
				Description: fmt.Sprintf("Read the most recent log lines of one service's container. "+
					"Returns the tail only (no follow), at most %d lines and %d KiB, newest kept. "+
					"The project's env values and any Windlass API token are replaced with [redacted]. "+
					"If the project has several services, name one; get_project lists them.",
					MaxLogLines, MaxLogBytes>>10),
				Annotations: readOnly,
			},
			params: []param{projectParam, {
				name: "service", kind: kindString, pattern: serviceNameRE, maxLen: 128,
				description: "Compose service name. Optional when the project has one service.",
			}, {
				name: "tail", kind: kindInteger, min: 1, max: MaxLogLines,
				description: fmt.Sprintf("How many of the newest lines to read. Default 100, at most %d.", MaxLogLines),
			}},
			run: getLogs,
		},
		{
			Tool: Tool{
				Name:  "get_staging",
				Title: "Get staging status",
				Description: "Compare a project's staging with live: which deployment each is on, whether staging is " +
					"ahead and can be promoted (and why not), auto-promote, branches, and which Compose files " +
					"differ once staging's own hostnames are allowed for. Works from either the live project or " +
					"its staging. It does not compare env keys or values.",
				Annotations: readOnly,
			},
			params: []param{projectParam},
			run:    getStaging,
		},
		{
			Tool: Tool{
				Name:  "list_backups",
				Title: "List backups",
				Description: "List a project's backups, newest first (kind, destination, size in bytes, status, " +
					"error, time) and its backup schedule (interval, destination, how many are kept, enabled).",
				Annotations: readOnly,
			},
			params: []param{projectParam, {
				name: "limit", kind: kindInteger, min: 1, max: MaxListItems,
				description: fmt.Sprintf("How many backups to return, newest first. Default 50, at most %d.", MaxListItems),
			}},
			run: listBackups,
		},
		{
			Tool: Tool{
				Name:  "deploy",
				Title: "Deploy a project",
				Description: "Deploy a project now, as the panel's Deploy button does: a Git project pulls the newest " +
					"commit of its branch, then docker compose up runs. Returns the new deployment's number at once; " +
					"the deployment carries on in the background, so follow it with get_deployment. Fails if a " +
					"deployment is already running. To change what is deployed, change the code or Compose file in " +
					"Git first; this server cannot edit files or env. " + stagingHint,
				Annotations: destructive,
			},
			params:   []param{projectParam, confirmParam},
			run:      deployProject,
			minScope: scopeDeploy,
		},
		{
			Tool: Tool{
				Name:  "restart",
				Title: "Restart a project",
				Description: "Restart every container of a project (docker compose restart). The app is down for a " +
					"few seconds. Nothing is pulled or rebuilt; use deploy for new code. Check the result with get_project.",
				Annotations: destructive,
			},
			params:   []param{projectParam, confirmParam},
			run:      restartProject,
			minScope: scopeDeploy,
		},
		{
			Tool: Tool{
				Name:  "rollback",
				Title: "Roll back a deployment",
				Description: "Roll a project back to an earlier successful deployment: its commit and image digests " +
					"are deployed again as a new deployment. Returns the new deployment's number at once; follow it " +
					"with get_deployment. Use list_deployments to pick the number.",
				Annotations: destructive,
			},
			params: []param{projectParam, {
				name: "number", kind: kindInteger, required: true, min: 1, max: 1 << 53,
				description: "The earlier deployment to go back to, as shown by list_deployments.",
			}, confirmParam},
			run:      rollbackProject,
			minScope: scopeDeploy,
		},
		{
			Tool: Tool{
				Name:  "promote",
				Title: "Promote staging to live",
				Description: "Deploy what staging verified to its live project: the same commit and image digests as " +
					"a staging deployment that succeeded. Name either the live project or its staging. Without a " +
					"number, the newest successful staging deployment is promoted; get_staging shows which one and " +
					"whether promote is possible. Returns the new live deployment's number; follow it with get_deployment.",
				Annotations: destructive,
			},
			params: []param{projectParam, {
				name: "number", kind: kindInteger, min: 1, max: 1 << 53,
				description: "Staging deployment number to promote. Default: the newest successful one.",
			}, confirmParam},
			run:      promoteStaging,
			minScope: scopeDeploy,
		},
		{
			Tool: Tool{
				Name:  "create_backup",
				Title: "Back up a project",
				Description: "Take a backup of a project now: its volumes and any databases Windlass detects, to local " +
					"storage or S3. It counts toward the project's backup retention, so the next scheduled backup " +
					"may delete the oldest one to make room. One every 10 minutes at most. A big project can take " +
					"minutes; if it is still running when this returns, check list_backups. Restoring is done in " +
					"the panel, not here.",
				Annotations: destructive,
			},
			params: []param{projectParam, {
				name: "destination", kind: kindString, pattern: destinationRE, maxLen: 5,
				description: "local (the default) or s3, if an S3 destination is set up in the panel.",
			}, confirmParam},
			run:      createBackup,
			minScope: scopeDeploy,
		},
	}
	for i := range defs {
		if defs[i].minScope == "" {
			defs[i].minScope = scopeRead
		}
		defs[i].InputSchema = inputSchema(defs[i].params)
	}
	return defs
}

// inputSchema renders params as a JSON Schema object. Unknown properties are
// refused so a model learns at once that it guessed an argument name.
func inputSchema(params []param) json.RawMessage {
	props := map[string]any{}
	required := []string{}
	for _, p := range params {
		prop := map[string]any{"type": string(p.kind), "description": p.description}
		switch p.kind {
		case kindInteger:
			prop["minimum"] = p.min
			prop["maximum"] = p.max
		case kindString:
			if p.pattern != nil {
				prop["pattern"] = p.pattern.String()
			}
			if p.maxLen > 0 {
				prop["maxLength"] = p.maxLen
			}
		}
		props[p.name] = prop
		if p.required {
			required = append(required, p.name)
		}
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	b, err := json.Marshal(schema)
	if err != nil {
		panic(err)
	}
	return b
}

// checkArgs validates arguments against the tool's params. Its errors are
// worded for the model, which sees them as a tool error and can retry.
func (d *toolDef) checkArgs(args map[string]json.RawMessage) error {
	known := make(map[string]param, len(d.params))
	for _, p := range d.params {
		known[p.name] = p
	}
	var unknown []string
	for name := range args {
		if _, ok := known[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		names := make([]string, 0, len(d.params))
		for _, p := range d.params {
			names = append(names, p.name)
		}
		if len(names) == 0 {
			return fmt.Errorf("%s takes no arguments; got %s", d.Name, strings.Join(unknown, ", "))
		}
		return fmt.Errorf("unknown argument %s; %s takes %s",
			strings.Join(unknown, ", "), d.Name, strings.Join(names, ", "))
	}
	for _, p := range d.params {
		raw, ok := args[p.name]
		if !ok || string(raw) == "null" {
			if p.mustBeTrue {
				return p.notConfirmed()
			}
			if p.required {
				return fmt.Errorf("%s is required", p.name)
			}
			continue
		}
		if err := p.check(raw); err != nil {
			return err
		}
	}
	return nil
}

func (p param) check(raw json.RawMessage) error {
	switch p.kind {
	case kindString:
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return fmt.Errorf("%s must be a string", p.name)
		}
		if p.maxLen > 0 && len(s) > p.maxLen {
			return fmt.Errorf("%s is longer than %d characters", p.name, p.maxLen)
		}
		if p.pattern != nil && !p.pattern.MatchString(s) {
			return fmt.Errorf("%s %q is not a valid %s name", p.name, s, p.name)
		}
	case kindInteger:
		var n json.Number
		if json.Unmarshal(raw, &n) != nil {
			return fmt.Errorf("%s must be a whole number", p.name)
		}
		v, err := n.Int64()
		if err != nil {
			// Accept 5.0, which some clients send for integers.
			f, ferr := n.Float64()
			if ferr != nil || f != float64(int64(f)) {
				return fmt.Errorf("%s must be a whole number", p.name)
			}
			v = int64(f)
		}
		if v < p.min || v > p.max {
			return fmt.Errorf("%s must be between %d and %d", p.name, p.min, p.max)
		}
	case kindBoolean:
		var b bool
		if json.Unmarshal(raw, &b) != nil {
			return fmt.Errorf("%s must be true or false", p.name)
		}
		if p.mustBeTrue && !b {
			return p.notConfirmed()
		}
	}
	return nil
}

// notConfirmed tells the model to get the user's agreement, not to retry
// with confirm set on its own.
func (p param) notConfirmed() error {
	return fmt.Errorf("%s must be true: ask the user first, then call again with %s set to true", p.name, p.name)
}
