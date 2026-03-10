package forge

import (
	"database/sql"
	"fmt"
	"time"
)

// CreateProjectV3 creates a new project with container config.
func (f *Forge) CreateProjectV3(opts CreateProjectV3Opts) error {
	if opts.ID == "" {
		return fmt.Errorf("project ID required")
	}
	if opts.BasePort == 0 {
		return fmt.Errorf("base port required")
	}

	now := time.Now().Unix()
	_, err := f.db.Exec(`
		INSERT INTO projects_v3 (id, name, description, dockerfile, dockerfile_template, build_cmd, test_cmd, start_cmd, slot_count, base_port, port_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, opts.ID, opts.Name, opts.Description, opts.Dockerfile, opts.DockerfileTemplate, opts.BuildCmd, opts.TestCmd, opts.StartCmd, opts.SlotCount, opts.BasePort, opts.PortCount, now, now)

	return err
}

type CreateProjectV3Opts struct {
	ID                 string
	Name               string
	Description        string
	Dockerfile         string
	DockerfileTemplate string
	BuildCmd           string
	TestCmd            string
	StartCmd           string
	SlotCount          int
	BasePort           int
	PortCount          int
}

// AddProjectRepo adds a repo to a project.
func (f *Forge) AddProjectRepo(projectID, repoID, repoPath string) error {
	_, err := f.db.Exec(`
		INSERT INTO project_repos (project_id, repo_id, repo_path, branch)
		VALUES (?, ?, ?, 'main')
	`, projectID, repoID, repoPath)
	return err
}

// InitSlotsV3 creates slots for a project.
func (f *Forge) InitSlotsV3(projectID string) error {
	// Get project
	var slotCount, basePort int
	err := f.db.QueryRow(`SELECT slot_count, base_port FROM projects_v3 WHERE id = ?`, projectID).Scan(&slotCount, &basePort)
	if err != nil {
		return fmt.Errorf("project not found: %w", err)
	}

	now := time.Now().Unix()

	// Create slots
	for i := 0; i < slotCount; i++ {
		containerName := fmt.Sprintf("%s-%d", projectID, i)
		_, err := f.db.Exec(`
			INSERT INTO slots_v3 (project_id, slot_num, container_name, status, base_port, created_at)
			VALUES (?, ?, ?, 'idle', ?, ?)
		`, projectID, i, containerName, basePort, now)
		if err != nil {
			return fmt.Errorf("create slot %d: %w", i, err)
		}
	}

	return nil
}

// AcquireSlotV3 acquires a slot for an agent.
func (f *Forge) AcquireSlotV3(projectID, agentID, sessionID string) (*SlotV3, error) {
	now := time.Now().Unix()

	// Find idle slot
	result, err := f.db.Exec(`
		UPDATE slots_v3
		SET status = 'active', agent_id = ?, session_id = ?, acquired_at = ?
		WHERE id = (
			SELECT id FROM slots_v3
			WHERE project_id = ? AND status = 'idle'
			ORDER BY slot_num
			LIMIT 1
		)
	`, agentID, sessionID, now, projectID)
	if err != nil {
		return nil, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, fmt.Errorf("no idle slots for project %s", projectID)
	}

	// Get the acquired slot
	var slot SlotV3
	err = f.db.QueryRow(`
		SELECT id, project_id, slot_num, container_name, status, container_id, image_id, base_port, agent_id, session_id
		FROM slots_v3
		WHERE project_id = ? AND session_id = ?
	`, projectID, sessionID).Scan(&slot.ID, &slot.ProjectID, &slot.SlotNum, &slot.ContainerName, &slot.Status, &slot.ContainerID, &slot.ImageID, &slot.BasePort, &slot.AgentID, &slot.SessionID)
	if err != nil {
		return nil, err
	}

	return &slot, nil
}

// ReleaseSlotV3 releases a slot.
func (f *Forge) ReleaseSlotV3(slotID int) error {
	_, err := f.db.Exec(`
		UPDATE slots_v3
		SET status = 'idle', agent_id = NULL, session_id = NULL, acquired_at = NULL
		WHERE id = ?
	`, slotID)
	return err
}

// GetSlotV3 gets a slot by ID.
func (f *Forge) GetSlotV3(slotID int) (*SlotV3, error) {
	var slot SlotV3
	err := f.db.QueryRow(`
		SELECT id, project_id, slot_num, container_name, status, container_id, image_id, base_port, agent_id, session_id
		FROM slots_v3
		WHERE id = ?
	`, slotID).Scan(&slot.ID, &slot.ProjectID, &slot.SlotNum, &slot.ContainerName, &slot.Status, &slot.ContainerID, &slot.ImageID, &slot.BasePort, &slot.AgentID, &slot.SessionID)
	if err != nil {
		return nil, err
	}
	return &slot, nil
}

// GetSlotByContainer gets a slot by container name.
func (f *Forge) GetSlotByContainer(containerName string) (*SlotV3, error) {
	var slot SlotV3
	err := f.db.QueryRow(`
		SELECT id, project_id, slot_num, container_name, status, container_id, image_id, base_port, agent_id, session_id
		FROM slots_v3
		WHERE container_name = ?
	`, containerName).Scan(&slot.ID, &slot.ProjectID, &slot.SlotNum, &slot.ContainerName, &slot.Status, &slot.ContainerID, &slot.ImageID, &slot.BasePort, &slot.AgentID, &slot.SessionID)
	if err != nil {
		return nil, err
	}
	return &slot, nil
}

// ListProjectSlotsV3 lists all slots for a project.
func (f *Forge) ListProjectSlotsV3(projectID string) ([]SlotV3, error) {
	rows, err := f.db.Query(`
		SELECT id, project_id, slot_num, container_name, status, container_id, image_id, base_port, agent_id, session_id
		FROM slots_v3
		WHERE project_id = ?
		ORDER BY slot_num
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var slots []SlotV3
	for rows.Next() {
		var s SlotV3
		var containerID, imageID, agentID, sessionID sql.NullString
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.SlotNum, &s.ContainerName, &s.Status, &containerID, &imageID, &s.BasePort, &agentID, &sessionID); err != nil {
			return nil, err
		}
		s.ContainerID = containerID.String
		s.ImageID = imageID.String
		s.AgentID = agentID.String
		s.SessionID = sessionID.String
		slots = append(slots, s)
	}
	return slots, nil
}

// ListProjectsV3 lists all projects.
func (f *Forge) ListProjectsV3() ([]ProjectV3, error) {
	rows, err := f.db.Query(`
		SELECT id, name, description, dockerfile_template, build_cmd, test_cmd, start_cmd, slot_count, base_port
		FROM projects_v3
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []ProjectV3
	for rows.Next() {
		var p ProjectV3
		var desc, df, build, test, start sql.NullString
		if err := rows.Scan(&p.ID, &p.Name, &desc, &df, &build, &test, &start, &p.SlotCount, &p.BasePort); err != nil {
			return nil, err
		}
		p.Description = desc.String
		p.DockerfileTemplate = df.String
		p.BuildCmd = build.String
		p.TestCmd = test.String
		p.StartCmd = start.String
		projects = append(projects, p)
	}
	return projects, nil
}

// --- Types ---

type SlotV3 struct {
	ID            int
	ProjectID     string
	SlotNum       int
	ContainerName string
	Status        string // idle, building, running, stopped, error
	ContainerID   string
	ImageID       string
	BasePort      int
	AgentID       string
	SessionID     string
}

type ProjectV3 struct {
	ID                 string
	Name               string
	Description        string
	Dockerfile         string
	DockerfileTemplate string
	BuildCmd           string
	TestCmd            string
	StartCmd           string
	SlotCount          int
	BasePort           int
	PortCount          int
}
