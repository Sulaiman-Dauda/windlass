package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
		r.Delete("/{name}", a.limitAuth(a.handleDeleteProject))
		r.Put("/{name}/files/*", a.handleWriteProjectFile)
		r.Put("/{name}/env", a.handleSetProjectEnv)
		r.Put("/{name}/git", a.handleConfigureProjectGit)
	})
	r.Get("/{name}", a.handleGetProject)
	r.Get("/{name}/files", a.handleListProjectFiles)
	r.Get("/{name}/files/*", a.handleReadProjectFile)
	r.Get("/{name}/env", a.handleGetProjectEnv)
	r.Get("/{name}/services", a.handleProjectServices)
	r.Get("/{name}/logs", a.handleProjectLogs)
	r.With(auth.RequireRole("member")).Get("/{name}/terminal", a.handleTerminal)
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
	writeJSON(w, http.StatusOK, toProjectDTO(p))
}

func (a *API) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	// Deleting stops containers and removes the project directory, so a live
	// session alone is not enough: users with a password must re-enter it.
	// OAuth-only accounts have no password hash and skip this check.
	user, _ := auth.UserFrom(r.Context())
	if user.PasswordHash.Valid {
		var req struct {
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Password == "" {
			writeError(w, http.StatusForbidden, "password_required", "re-enter your password to delete this project")
			return
		}
		if err := auth.VerifyPassword(user.PasswordHash.String, req.Password); err != nil {
			a.Audit.Write(r.Context(), user.ID, "project.delete_denied", "project", name, remoteIP(r), nil)
			writeError(w, http.StatusForbidden, "invalid_password", "password does not match")
			return
		}
	}

	err := a.Projects.Delete(r.Context(), name)
	if errors.Is(err, projects.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		a.internalError(w, "delete project", err)
		return
	}
	a.Audit.Write(r.Context(), user.ID, "project.delete", "project", name, remoteIP(r), nil)
	w.WriteHeader(http.StatusNoContent)
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
	writeJSON(w, http.StatusOK, map[string]string{"path": rel, "content": string(data)})
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
	writeJSON(w, http.StatusOK, vars)
}

func (a *API) handleSetProjectEnv(w http.ResponseWriter, r *http.Request) {
	var vars map[string]string
	if err := json.NewDecoder(r.Body).Decode(&vars); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	name := chi.URLParam(r, "name")
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
