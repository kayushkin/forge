package forge

import (
	"database/sql"
	"fmt"
	"time"
)

// Project represents a registered project
type Project struct {
	ID            string
	BaseRepo      string
	PoolDir       string
	PoolSize      int
	DefaultBranch string
	RepoURL       string // remote git URL
	BuildCmd      string
	ServeCmd      string
	IsPrimary     bool  // true if this is the primary web project
	PortOffset    int   // relative to environment base_port (-1 = not exposed)
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// RegisterProject registers or updates a project
func (f *Forge) RegisterProject(p Project) error {
	ts := now()
	if p.DefaultBranch == "" {
		p.DefaultBranch = "main"
	}
	if p.PoolSize == 0 {
		p.PoolSize = 3
	}

	_, err := f.db.Exec(`
		INSERT INTO projects (id, base_repo, pool_dir, pool_size, default_branch, repo_url, build_cmd, serve_cmd, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			base_repo = excluded.base_repo,
			pool_dir = excluded.pool_dir,
			pool_size = excluded.pool_size,
			default_branch = excluded.default_branch,
			repo_url = excluded.repo_url,
			build_cmd = excluded.build_cmd,
			serve_cmd = excluded.serve_cmd,
			updated_at = excluded.updated_at
	`, p.ID, p.BaseRepo, p.PoolDir, p.PoolSize, p.DefaultBranch, p.RepoURL, p.BuildCmd, p.ServeCmd, ts, ts)
	return err
}

// GetProject retrieves a project by ID
func (f *Forge) GetProject(id string) (*Project, error) {
	p := &Project{ID: id}
	var createdAt, updatedAt int64
	var repoURL sql.NullString
	err := f.db.QueryRow(`
		SELECT base_repo, pool_dir, pool_size, default_branch, repo_url, build_cmd, serve_cmd, created_at, updated_at
		FROM projects WHERE id = ?
	`, id).Scan(&p.BaseRepo, &p.PoolDir, &p.PoolSize, &p.DefaultBranch, &repoURL, &p.BuildCmd, &p.ServeCmd, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("project %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	p.RepoURL = repoURL.String
	p.CreatedAt = time.Unix(createdAt, 0)
	p.UpdatedAt = time.Unix(updatedAt, 0)
	return p, nil
}

// ListProjects returns all registered projects
func (f *Forge) ListProjects() ([]Project, error) {
	rows, err := f.db.Query(`
		SELECT id, base_repo, pool_dir, pool_size, default_branch, repo_url, build_cmd, serve_cmd, created_at, updated_at
		FROM projects ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Project
	for rows.Next() {
		var p Project
		var createdAt, updatedAt int64
		var repoURL sql.NullString
		if err := rows.Scan(&p.ID, &p.BaseRepo, &p.PoolDir, &p.PoolSize, &p.DefaultBranch, &repoURL, &p.BuildCmd, &p.ServeCmd, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		p.RepoURL = repoURL.String
		p.CreatedAt = time.Unix(createdAt, 0)
		p.UpdatedAt = time.Unix(updatedAt, 0)
		results = append(results, p)
	}
	return results, nil
}

// FindProjectByPath finds a project whose BaseRepo matches the given path.
// Returns nil if no project matches.
func (f *Forge) FindProjectByPath(path string) *Project {
	projects, err := f.ListProjects()
	if err != nil {
		return nil
	}
	for _, p := range projects {
		if p.BaseRepo == path {
			return &p
		}
	}
	return nil
}

// DeleteProject removes a project and all its slots/previews
func (f *Forge) DeleteProject(id string) error {
	_, err := f.db.Exec("DELETE FROM projects WHERE id = ?", id)
	return err
}
