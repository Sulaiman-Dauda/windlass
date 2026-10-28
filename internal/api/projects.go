package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/store/db"
)

type projectDTO struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Source     string `json:"source"`
	GitRepo    string `json:"git_repo,omitempty"`
	GitBranch  string `json:"git_branch,omitempty"`
	AutoDeploy bool   `json:"auto_deploy"`
	CreatedAt  string `json:"created_at"`
	// StagingOf names the live project when this is a staging; Staging
	// names this project's staging when it has one.
	StagingOf string `json:"staging_of,omitempty"`
	Staging   string `json:"staging,omitempty"`
}

func toProjectDTO(p db.Project) projectDTO {
	return projectDTO{
		ID:         p.ID,
		Name:       p.Name,
		Source:     p.Source,
		GitRepo:    p.GitRepo.String,
		GitBranch:  p.GitBranch.String,
		AutoDeploy: p.AutoDeploy != 0,
		CreatedAt:  p.CreatedAt,
		StagingOf:  p.StagingOf.String,
	}
}

// withStaging fills in the staging of each live project in list, from the
// index: a staging names its live project in staging_of.
func withStaging(list []projectDTO) {
	stagingOf := map[string]string{}
	for _, p := range list {
		if p.StagingOf != "" && p.Name == projects.StagingName(p.StagingOf) {
			stagingOf[p.StagingOf] = p.Name
		}
	}
	for i := range list {
		list[i].Staging = stagingOf[list[i].Name]
	}
}

func (a *API) projectRoutes(r chi.Router) {
	r.Get("/", a.handleListProjects)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireRole("member"))
		r.Post("/", a.handleCreateProject)
		r.Post("/scan", a.handleScanProjects)
		// limitAuth: delete re-verifies the password, so it is a credential
		// endpoint and shares the brute-force budget.
		// RejectToken: deletion asks for the password and there may be no
		// backup, so it stays with a person at the panel.
		r.With(auth.RejectToken).Delete("/{name}", a.limitAuth(a.handleDeleteProject))
		r.Put("/{name}/files/*", a.handleWriteProjectFile)
		r.Put("/{name}/env", a.handleSetProjectEnv)
		r.Put("/{name}/git", a.handleConfigureProjectGit)
		r.Post("/{name}/staging/preflight", a.handleStagingPreflight)
		r.Post("/{name}/staging", a.handleCreateStaging)
		r.With(auth.RejectToken).Post("/{name}/staging/recreate", a.limitAuth(a.handleRecreateStaging))
		r.Put("/{name}/staging", a.handleSetStaging)
		// Member only. A token without the secrets flag gets both files
		// masked, and Copy to live then refuses the masked content.
		r.Get("/{name}/staging/diff/{file}", a.handleStagingDiff)
	})
	r.Get("/{name}/staging", a.handleGetStaging)
	r.Get("/{name}", a.handleGetProject)
	r.Get("/{name}/files", a.handleListProjectFiles)
	r.Get("/{name}/files/*", a.handleReadProjectFile)
	r.Get("/{name}/env", a.handleGetProjectEnv)
	r.Get("/{name}/services", a.handleProjectServices)
	r.Get("/{name}/logs", a.handleProjectLogs)
	r.With(auth.RejectToken, auth.RequireRole("member")).Get("/{name}/terminal", a.handleTerminal)
	r.Route("/{name}/deployments", a.deploymentRoutes)
	r.Route("/{name}/domains", a.domainRoutes)
	r.Route("/{name}/backups", a.backupRoutes)
	r.With(auth.RequireRole("member")).Post("/{name}/actions/{action}", a.handleProjectAction)
}

func (a *API) handleScanProjects(w http.ResponseWriter, r *http.Request) {
	if err := a.Projects.Reconcile(r.Context()); err != nil {
		a.internalError(w, "scan projects", err)
		return
	}
	list, err := a.Projects.List(r.Context())
	if err != nil {
		a.internalError(w, "list projects after scan", err)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "project.scan", "project", "*", remoteIP(r),
		map[string]any{"count": len(list)})
	writeJSON(w, http.StatusOK, map[string]int{"count": len(list)})
}

func (a *API) handleListProjects(w http.ResponseWriter, r *http.Request) {
	list, err := a.Projects.List(r.Context())
	if err != nil {
		a.internalError(w, "list projects", err)
		return
	}
	out := make([]projectDTO, 0, len(list))
	for _, p := range list {
		out = append(out, toProjectDTO(p))
	}
	withStaging(out)
	writeJSON(w, http.StatusOK, out)
}

type createProjectRequest struct {
	Name string `json:"name"`
}

func (a *API) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)

	p, err := a.Projects.Create(r.Context(), projects.CreateReq{Name: req.Name})
	if errors.Is(err, projects.ErrAlreadyExists) {
		writeError(w, http.StatusConflict, "conflict", "a project with this name already exists")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "project.create", "project", p.Name, remoteIP(r), nil)
	writeJSON(w, http.StatusCreated, toProjectDTO(p))
}

func (a *API) handleGetProject(w http.ResponseWriter, r *http.Request) {
	p, err := a.Projects.Get(r.Context(), chi.URLParam(r, "name"))
	if errors.Is(err, projects.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		a.internalError(w, "get project", err)
		return
	}
	dto := toProjectDTO(p)
	if s, ok, err := a.Projects.StagingFor(r.Context(), p.Name); err == nil && ok {
		dto.Staging = s.Name
	}
	writeJSON(w, http.StatusOK, dto)
}

func (a *API) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	user, _ := auth.UserFrom(r.Context())
	var req struct {
		Password string `json:"password"`
	}
	if user.PasswordHash.Valid {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if !a.confirmProjectDeletion(w, r, req.Password, "project.delete_denied") {
		return
	}

	err := a.Projects.Delete(r.Context(), name)
	if errors.Is(err, projects.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if errors.Is(err, projects.ErrHasStaging) {
		writeError(w, http.StatusConflict, "has_staging", err.Error())
		return
	}
	if errors.Is(err, projects.ErrSharedVolume) {
		writeError(w, http.StatusConflict, "shared_volume", err.Error())
		return
	}
	if err != nil {
		a.internalError(w, "delete project", err)
		return
	}
	a.Audit.Write(r.Context(), user.ID, "project.delete", "project", name, remoteIP(r), nil)
	w.WriteHeader(http.StatusNoContent)
}

// Both delete and staging recreation remove the directory and volumes. A
// session alone is insufficient; OAuth-only accounts have no hash to check.
func (a *API) confirmProjectDeletion(w http.ResponseWriter, r *http.Request, password, deniedAction string) bool {
	user, _ := auth.UserFrom(r.Context())
	if !user.PasswordHash.Valid {
		return true
	}
	if password == "" {
		writeError(w, http.StatusForbidden, "password_required", "re-enter your password to delete this project")
		return false
	}
	err := auth.VerifyPassword(r.Context(), user.PasswordHash.String, password)
	if errors.Is(err, auth.ErrHashBusy) {
		writeError(w, http.StatusServiceUnavailable, "busy", "too many password checks at once, try again in a moment")
		return false
	}
	if err != nil {
		a.Audit.Write(r.Context(), user.ID, deniedAction, "project", chi.URLParam(r, "name"), remoteIP(r), nil)
		writeError(w, http.StatusForbidden, "invalid_password", "password does not match")
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Files

func (a *API) handleListProjectFiles(w http.ResponseWriter, r *http.Request) {
	files, err := a.Projects.ListFiles(r.Context(), chi.URLParam(r, "name"), ".")
	if errors.Is(err, projects.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		a.internalError(w, "list files", err)
		return
	}
	writeJSON(w, http.StatusOK, files)
}

func (a *API) handleReadProjectFile(w http.ResponseWriter, r *http.Request) {
	rel := chi.URLParam(r, "*")
	data, err := a.Projects.ReadFile(r.Context(), chi.URLParam(r, "name"), rel)
	if errors.Is(err, projects.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	content := string(data)
	if !auth.CanReadSecrets(r.Context()) {
		switch {
		case isEnvFile(rel):
			content = maskEnvFile(content)
		case isYAMLFile(rel):
			content = maskComposeFile(content)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": rel, "content": content})
}

func (a *API) handleWriteProjectFile(w http.ResponseWriter, r *http.Request) {
	rel := chi.URLParam(r, "*")
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "unreadable body")
		return
	}
	var req struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	name := chi.URLParam(r, "name")
	// A caller who read this file masked would write the mask back over
	// every secret in it. Send them to PUT /env, which keeps masked values.
	if (isEnvFile(rel) || isYAMLFile(rel)) && !auth.CanReadSecrets(r.Context()) && strings.Contains(req.Content, maskedEnvValue) {
		writeError(w, http.StatusBadRequest, "masked_values",
			"this file contains masked values ("+maskedEnvValue+"); set variables with PUT /env instead, which keeps them, "+
				"and keep values out of compose files")
		return
	}
	if err := a.Projects.WriteFile(r.Context(), name, rel, []byte(req.Content)); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "project not found")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "project.file_write", "project", name, remoteIP(r),
		map[string]string{"path": rel})
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Env

const maskedEnvValue = "********"

// isEnvFile matches dotenv files: .env, *.env, and variants such as
// .env.production or .env.local, which hold the same kind of secrets.
func isEnvFile(rel string) bool {
	lower := strings.ToLower(rel)
	return strings.HasSuffix(lower, ".env") || strings.HasPrefix(path.Base(lower), ".env.")
}

var envKeyRe = regexp.MustCompile(`^\s*(export\s+)?[A-Za-z_][A-Za-z0-9_.-]*\s*$`)

// maskEnvFile hides every non-empty value in a dotenv file and keeps keys,
// comments and layout, so a viewer reading the file learns what the env
// endpoint would tell them and nothing more. A quoted value can run over
// several lines (a PEM key), so its continuation lines are hidden too, as
// are commented-out assignments (an old password left as "# KEY=...") and
// any line that is not KEY=value, such as the YAML-style "KEY: value".
func maskEnvFile(content string) string {
	lines := strings.Split(content, "\n")
	var open byte
	for i, line := range lines {
		if open != 0 {
			if strings.IndexByte(line, open) >= 0 {
				open = 0
			}
			lines[i] = maskedEnvValue
			continue
		}
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "#") {
			if key, _, ok := strings.Cut(line, "="); ok {
				lines[i] = key + "=" + maskedEnvValue
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !envKeyRe.MatchString(key) {
			lines[i] = maskedEnvValue
			continue
		}
		value = strings.TrimSpace(value)
		switch value {
		case "", `""`, "''":
			continue
		}
		if strings.IndexByte("\"'`", value[0]) >= 0 && !closesQuote(value) {
			open = value[0]
		}
		lines[i] = key + "=" + maskedEnvValue
	}
	return strings.Join(lines, "\n")
}

// closesQuote reports whether the quoted value at the start of v ends on
// this line, skipping escaped quotes.
func closesQuote(v string) bool {
	for j := 1; j < len(v); j++ {
		switch v[j] {
		case '\\':
			j++
		case v[0]:
			return true
		}
	}
	return false
}

func (a *API) handleGetProjectEnv(w http.ResponseWriter, r *http.Request) {
	vars, err := a.Projects.GetEnv(r.Context(), chi.URLParam(r, "name"))
	if errors.Is(err, projects.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		a.internalError(w, "get env", err)
		return
	}
	// Viewers, and API tokens without the secrets flag, see which keys are
	// set, never the values: they are the project's credentials. An unset
	// value stays empty so it still shows.
	if !auth.CanReadSecrets(r.Context()) {
		for k, v := range vars {
			if v != "" {
				vars[k] = maskedEnvValue
			}
		}
	}
	writeJSON(w, http.StatusOK, vars)
}

func (a *API) handleSetProjectEnv(w http.ResponseWriter, r *http.Request) {
	var vars map[string]string
	if err := json.NewDecoder(r.Body).Decode(&vars); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	name := chi.URLParam(r, "name")
	// A caller who cannot read secrets got "********" for every value. When
	// they send the map back with one change, the masked entries mean "keep
	// what is stored", or a routine edit would overwrite every credential.
	if !auth.CanReadSecrets(r.Context()) {
		stored, err := a.Projects.GetEnv(r.Context(), name)
		if errors.Is(err, projects.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "project not found")
			return
		}
		if err != nil {
			a.internalError(w, "get env", err)
			return
		}
		for k, v := range vars {
			if old, ok := stored[k]; ok && v == maskedEnvValue {
				vars[k] = old
			}
		}
	}
	if err := a.Projects.SetEnv(r.Context(), name, vars); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "project not found")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "project.env_write", "project", name, remoteIP(r), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) internalError(w http.ResponseWriter, op string, err error) {
	a.Logger.Error(op, "error", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal error")
}
