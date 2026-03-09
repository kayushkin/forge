package forge

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Environment is a deployment slot containing multiple repos
type Environment struct {
	ID           int
	Name         string
	BasePort     int
	Status       string // idle, active, deploying
	AgentID      string
	SessionID    string
	Orchestrator string
	AcquiredAt   *time.Time
	CreatedAt    time.Time
	Repos        []EnvironmentRepo // loaded separately
}

// EnvironmentRepo is a repo checked out in an environment
type EnvironmentRepo struct {
	EnvironmentID int
	ProjectID     string
	WorktreePath  string
	Branch        string
	CommitHash    string
	Dirty         bool
}

// ErrNoEnvironments is returned when all environments are busy
var ErrNoEnvironments = fmt.Errorf("no available environments")

// DefaultEnvDir is where environments live
var DefaultEnvDir = expandPath("~/life/repos/.envs")

// DefaultProjects lists projects that go in each environment
var DefaultProjects = []string{"inber", "bus", "si", "kayushkin", "agent-store", "forge", "model-store", "aiauth"}

// Port allocations per environment (base_port + offset)
var PortOffsets = map[string]int{
	"inber":      0,  // not directly exposed
	"bus":        10, // 9010, 9110, etc.
	"si":         20, // 9020, 9120, etc.
	"kayushkin":  0,  // 9000, 9100, etc. (primary)
	"logstack":   30, // 9030, 9130, etc.
}

// InitEnvironments creates the environment slots
func (f *Forge) InitEnvironments(count int) error {
	if err := os.MkdirAll(DefaultEnvDir, 0755); err != nil {
		return fmt.Errorf("create env dir: %w", err)
	}

	for i := 0; i < count; i++ {
		name := fmt.Sprintf("env-%d", i)
		basePort := 9000 + (i * 100) // env-0: 9000, env-1: 9100, env-2: 9200

		// Insert environment record
		_, err := f.db.Exec(`
			INSERT OR IGNORE INTO environments (id, name, base_port, status, created_at)
			VALUES (?, ?, ?, 'idle', ?)
		`, i, name, basePort, time.Now().Unix())
		if err != nil {
			return fmt.Errorf("create environment %d: %w", i, err)
		}

		envPath := filepath.Join(DefaultEnvDir, name)
		if err := os.MkdirAll(envPath, 0755); err != nil {
			return fmt.Errorf("create env path %s: %w", envPath, err)
		}

		// Create worktrees for each project
		for _, projectID := range DefaultProjects {
			if err := f.initEnvironmentRepo(i, projectID); err != nil {
				return fmt.Errorf("init repo %s in env %d: %w", projectID, i, err)
			}
		}
	}

	return nil
}

// initEnvironmentRepo creates a worktree for a project in an environment
func (f *Forge) initEnvironmentRepo(envID int, projectID string) error {
	p, err := f.GetProject(projectID)
	if err != nil {
		return fmt.Errorf("project %s not found: %w", projectID, err)
	}

	var envName string
	if err := f.db.QueryRow(`SELECT name FROM environments WHERE id = ?`, envID).Scan(&envName); err != nil {
		return err
	}

	envPath := filepath.Join(DefaultEnvDir, envName)
	worktreePath := filepath.Join(envPath, projectID)
	branch := fmt.Sprintf("env/%s", envName)

	// Check if worktree already exists
	if _, err := os.Stat(worktreePath); err == nil {
		// Record in DB
		_, err = f.db.Exec(`
			INSERT OR IGNORE INTO environment_repos (environment_id, project_id, worktree_path, branch)
			VALUES (?, ?, ?, ?)
		`, envID, projectID, worktreePath, branch)
		return err
	}

	// Create worktree
	baseRepo := expandPath(p.BaseRepo)
	cmd := exec.Command("git", "worktree", "add", worktreePath, "-b", branch)
	cmd.Dir = baseRepo
	if _, err := cmd.CombinedOutput(); err != nil {
		// Branch might already exist, try without -b
		cmd = exec.Command("git", "worktree", "add", worktreePath, branch)
		cmd.Dir = baseRepo
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("create worktree: %w\n%s", err, out)
		}
	}

	// Record in DB
	_, err = f.db.Exec(`
		INSERT OR IGNORE INTO environment_repos (environment_id, project_id, worktree_path, branch)
		VALUES (?, ?, ?, ?)
	`, envID, projectID, worktreePath, branch)
	return err
}

// AcquireEnvironment leases an available environment
func (f *Forge) AcquireEnvironment(opts AcquireOpts) (*Environment, error) {
	var id int
	var name string
	var basePort int
	err := f.db.QueryRow(`
		SELECT id, name, base_port FROM environments
		WHERE status = 'idle'
		ORDER BY id LIMIT 1
	`).Scan(&id, &name, &basePort)
	if err == sql.ErrNoRows {
		return nil, ErrNoEnvironments
	}
	if err != nil {
		return nil, err
	}

	ts := time.Now().Unix()
	_, err = f.db.Exec(`
		UPDATE environments SET status = 'active', agent_id = ?, session_id = ?, orchestrator = ?, acquired_at = ?
		WHERE id = ? AND status = 'idle'
	`, opts.AgentID, opts.SessionID, opts.Orchestrator, ts, id)
	if err != nil {
		return nil, err
	}

	acq := time.Unix(ts, 0)
	return &Environment{
		ID:           id,
		Name:         name,
		BasePort:     basePort,
		Status:       "active",
		AgentID:      opts.AgentID,
		SessionID:    opts.SessionID,
		Orchestrator: opts.Orchestrator,
		AcquiredAt:   &acq,
	}, nil
}

// ReleaseEnvironment returns an environment to the pool
func (f *Forge) ReleaseEnvironment(envID int) error {
	_, err := f.db.Exec(`
		UPDATE environments SET status = 'idle', agent_id = NULL, session_id = NULL, orchestrator = NULL
		WHERE id = ?
	`, envID)
	return err
}

// GetEnvironment fetches an environment by ID
func (f *Forge) GetEnvironment(envID int) (*Environment, error) {
	var env Environment
	var agentID, sessionID, orchestrator sql.NullString
	var acquiredAt sql.NullInt64
	err := f.db.QueryRow(`
		SELECT id, name, base_port, status, agent_id, session_id, orchestrator, acquired_at
		FROM environments WHERE id = ?
	`, envID).Scan(&env.ID, &env.Name, &env.BasePort, &env.Status, &agentID, &sessionID, &orchestrator, &acquiredAt)
	if err != nil {
		return nil, err
	}
	if agentID.Valid {
		env.AgentID = agentID.String
	}
	if sessionID.Valid {
		env.SessionID = sessionID.String
	}
	if orchestrator.Valid {
		env.Orchestrator = orchestrator.String
	}
	if acquiredAt.Valid {
		t := time.Unix(acquiredAt.Int64, 0)
		env.AcquiredAt = &t
	}
	return &env, nil
}

// GetEnvironmentRepos fetches all repos in an environment
func (f *Forge) GetEnvironmentRepos(envID int) ([]EnvironmentRepo, error) {
	rows, err := f.db.Query(`
		SELECT environment_id, project_id, worktree_path, branch, commit_hash, dirty
		FROM environment_repos WHERE environment_id = ?
	`, envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repos []EnvironmentRepo
	for rows.Next() {
		var r EnvironmentRepo
		var commitHash sql.NullString
		if err := rows.Scan(&r.EnvironmentID, &r.ProjectID, &r.WorktreePath, &r.Branch, &commitHash, &r.Dirty); err != nil {
			return nil, err
		}
		if commitHash.Valid {
			r.CommitHash = commitHash.String
		}
		repos = append(repos, r)
	}
	return repos, nil
}

// AllEnvironments returns all environments
func (f *Forge) AllEnvironments() ([]Environment, error) {
	rows, err := f.db.Query(`
		SELECT id, name, base_port, status, agent_id, session_id, orchestrator, acquired_at
		FROM environments ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var envs []Environment
	for rows.Next() {
		var env Environment
		var agentID, sessionID, orchestrator sql.NullString
		var acquiredAt sql.NullInt64
		if err := rows.Scan(&env.ID, &env.Name, &env.BasePort, &env.Status, &agentID, &sessionID, &orchestrator, &acquiredAt); err != nil {
			return nil, err
		}
		if agentID.Valid {
			env.AgentID = agentID.String
		}
		if sessionID.Valid {
			env.SessionID = sessionID.String
		}
		if orchestrator.Valid {
			env.Orchestrator = orchestrator.String
		}
		if acquiredAt.Valid {
			t := time.Unix(acquiredAt.Int64, 0)
			env.AcquiredAt = &t
		}
		envs = append(envs, env)
	}
	return envs, nil
}

// EnvironmentStatus returns detailed status including git info
func (f *Forge) EnvironmentStatus(envID int) (*Environment, error) {
	env, err := f.GetEnvironment(envID)
	if err != nil {
		return nil, err
	}

	repos, err := f.GetEnvironmentRepos(envID)
	if err != nil {
		return nil, err
	}

	// Enrich with git info
	for i := range repos {
		r := &repos[i]
		if hash, err := gitCommitHash(r.WorktreePath); err == nil {
			r.CommitHash = hash
		}
		if dirty, _ := gitIsDirty(r.WorktreePath); dirty {
			r.Dirty = true
		}
	}

	env.Repos = repos
	return env, nil
}

// CleanEnvironment resets all repos in an environment to origin/main
func (f *Forge) CleanEnvironment(envID int) error {
	repos, err := f.GetEnvironmentRepos(envID)
	if err != nil {
		return err
	}

	for _, r := range repos {
		resetWorktree(r.WorktreePath)
	}
	return nil
}

// SyncEnvironmentRepos updates environment_repos with current git state
func (f *Forge) SyncEnvironmentRepos(envID int) error {
	repos, err := f.GetEnvironmentRepos(envID)
	if err != nil {
		return err
	}

	for _, r := range repos {
		hash, _ := gitCommitHash(r.WorktreePath)
		dirty, _ := gitIsDirty(r.WorktreePath)
		_, err := f.db.Exec(`
			UPDATE environment_repos SET commit_hash = ?, dirty = ? 
			WHERE environment_id = ? AND project_id = ?
		`, hash, dirty, envID, r.ProjectID)
		if err != nil {
			return err
		}
	}
	return nil
}
