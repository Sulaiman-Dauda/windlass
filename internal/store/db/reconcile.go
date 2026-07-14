package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// EnsureProjectIndex creates or refreshes the rebuildable project index from
// filesystem metadata. Runtime history and platform state remain untouched.
func (q *Queries) EnsureProjectIndex(ctx context.Context, arg CreateProjectParams) (Project, error) {
	_, err := q.db.ExecContext(ctx, `
		INSERT INTO projects (name, source, git_repo, git_branch, auto_deploy)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			source = excluded.source,
			git_repo = excluded.git_repo,
			git_branch = excluded.git_branch,
			auto_deploy = excluded.auto_deploy`,
		arg.Name, arg.Source, arg.GitRepo, arg.GitBranch, arg.AutoDeploy)
	if err != nil {
		return Project{}, err
	}
	return q.GetProjectByName(ctx, arg.Name)
}

// ListProjectIndexes returns only projects currently discovered on disk.
// Rows for temporarily missing projects remain so platform history survives.
func (q *Queries) ListProjectIndexes(ctx context.Context, names []string) ([]Project, error) {
	if len(names) == 0 {
		return []Project{}, nil
	}
	args := make([]any, len(names))
	marks := make([]string, len(names))
	for i, name := range names {
		args[i], marks[i] = name, "?"
	}
	rows, err := q.db.QueryContext(ctx, fmt.Sprintf(`SELECT id, name, source, git_repo, git_branch,
		auto_deploy, created_at, git_connection_id, webhook_secret_enc FROM projects
		WHERE name IN (%s) ORDER BY created_at DESC`, strings.Join(marks, ",")), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Project, 0, len(names))
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Source, &p.GitRepo, &p.GitBranch,
			&p.AutoDeploy, &p.CreatedAt, &p.GitConnectionID, &p.WebhookSecretEnc); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (q *Queries) ReplaceProjectDomains(ctx context.Context, projectID int64, domains []CreateDomainParams) error {
	beginner, ok := q.db.(interface {
		BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	})
	if !ok {
		return fmt.Errorf("database does not support transactions")
	}
	tx, err := beginner.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	keep := make([]any, 0, len(domains)+1)
	keep = append(keep, projectID)
	deleteSQL := `DELETE FROM domains WHERE project_id = ?`
	if len(domains) > 0 {
		marks := make([]string, len(domains))
		for i, domain := range domains {
			marks[i], keep = "?", append(keep, domain.Hostname)
		}
		deleteSQL += ` AND hostname NOT IN (` + strings.Join(marks, ",") + `)`
	}
	if _, err := tx.ExecContext(ctx, deleteSQL, keep...); err != nil {
		return err
	}
	for _, domain := range domains {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO domains (project_id, hostname, service, container_port) VALUES (?, ?, ?, ?)
			 ON CONFLICT(hostname) DO UPDATE SET project_id=excluded.project_id,
			 service=excluded.service, container_port=excluded.container_port`,
			projectID, domain.Hostname, domain.Service, domain.ContainerPort); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ProjectByID is used only to map platform requests back to their
// filesystem-owned project manifest.
func (q *Queries) ProjectByID(ctx context.Context, id int64) (Project, error) {
	row := q.db.QueryRowContext(ctx, `SELECT id, name, source, git_repo, git_branch,
		auto_deploy, created_at, git_connection_id, webhook_secret_enc FROM projects WHERE id = ?`, id)
	var p Project
	err := row.Scan(&p.ID, &p.Name, &p.Source, &p.GitRepo, &p.GitBranch,
		&p.AutoDeploy, &p.CreatedAt, &p.GitConnectionID, &p.WebhookSecretEnc)
	if err != nil {
		return Project{}, err
	}
	return p, nil
}

func (q *Queries) ListProtectedImageDigests(ctx context.Context, deploymentsPerProject int) ([]string, error) {
	rows, err := q.db.QueryContext(ctx, `
		WITH ranked AS (
			SELECT id, ROW_NUMBER() OVER (PARTITION BY project_id ORDER BY number DESC) AS rank
			FROM deployments WHERE status = 'succeeded'
		)
		SELECT DISTINCT a.image_digest
		FROM deployment_artifacts a JOIN ranked r ON r.id = a.deployment_id
		WHERE r.rank <= ?`, deploymentsPerProject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			return nil, err
		}
		out = append(out, digest)
	}
	return out, rows.Err()
}
