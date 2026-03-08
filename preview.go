package forge

import (
	"database/sql"
	"fmt"
	"time"
)

// Preview represents a running preview instance
type Preview struct {
	ID           string
	Project      string
	SlotID       int
	TargetID     string
	Branch       string
	CommitHash   string
	Host         string
	Port         int
	PID          int
	URL          string
	Status       string // pending, building, running, stopped, failed
	Error        string
	AgentID      string
	SessionID    string
	Orchestrator string
	StartedAt    *time.Time
	StoppedAt    *time.Time
}

// PreviewRequest configures a new preview
type PreviewRequest struct {
	Project      string
	SlotID       int
	TargetID     string
	AgentID      string
	SessionID    string
	Orchestrator string
}

// StartPreview deploys a slot's work to a target for preview
func (f *Forge) StartPreview(req PreviewRequest) (*Preview, error) {
	// Get target config
	target, err := f.GetTarget(req.TargetID)
	if err != nil {
		return nil, fmt.Errorf("get target: %w", err)
	}

	// Get slot info
	var path, branch string
	err = f.db.QueryRow(`SELECT path, branch FROM slots WHERE project = ? AND id = ?`, req.Project, req.SlotID).Scan(&path, &branch)
	if err != nil {
		return nil, fmt.Errorf("get slot: %w", err)
	}

	// Get commit hash
	commitHash, _ := gitHead(path)

	// Resolve port and URL
	port := target.ResolvePort(req.SlotID)
	url := target.ResolveURL(req.SlotID, port)

	// Generate preview ID
	id := fmt.Sprintf("%s-%d-%d", req.Project, req.SlotID, time.Now().Unix())

	ts := now()
	preview := &Preview{
		ID:           id,
		Project:      req.Project,
		SlotID:       req.SlotID,
		TargetID:     req.TargetID,
		Branch:       branch,
		CommitHash:   commitHash,
		Host:         target.Host,
		Port:         port,
		URL:          url,
		Status:       "pending",
		AgentID:      req.AgentID,
		SessionID:    req.SessionID,
		Orchestrator: req.Orchestrator,
	}

	// Upsert preview record
	_, err = f.db.Exec(`
		INSERT INTO previews (id, project, slot_id, target_id, branch, commit_hash, host, port, url, status, agent_id, session_id, orchestrator, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			status = excluded.status,
			branch = excluded.branch,
			commit_hash = excluded.commit_hash,
			started_at = excluded.started_at
	`, id, req.Project, req.SlotID, req.TargetID, branch, commitHash, target.Host, port, url, "pending", req.AgentID, req.SessionID, req.Orchestrator, ts)
	if err != nil {
		return nil, fmt.Errorf("insert preview: %w", err)
	}

	// Update slot status
	f.db.Exec(`UPDATE slots SET status = 'previewing' WHERE project = ? AND id = ?`, req.Project, req.SlotID)

	// Execute deployment based on target kind
	switch target.Kind {
	case "ssh":
		if err := f.deploySSH(preview, target, path); err != nil {
			preview.Status = "failed"
			preview.Error = err.Error()
			f.updatePreviewStatus(id, "failed", err.Error(), 0)
			return preview, fmt.Errorf("ssh deploy: %w", err)
		}
	case "local":
		if err := f.deployLocal(preview, target, path); err != nil {
			preview.Status = "failed"
			preview.Error = err.Error()
			f.updatePreviewStatus(id, "failed", err.Error(), 0)
			return preview, fmt.Errorf("local deploy: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported target kind: %s", target.Kind)
	}

	preview.Status = "running"
	return preview, nil
}

// StopPreview stops a running preview for a slot
func (f *Forge) StopPreview(projectID string, slotID int) error {
	// Find active preview
	var id, targetID string
	var pid int
	var host sql.NullString
	err := f.db.QueryRow(`
		SELECT id, target_id, pid, host FROM previews
		WHERE project = ? AND slot_id = ? AND status = 'running'
		ORDER BY started_at DESC LIMIT 1
	`, projectID, slotID).Scan(&id, &targetID, &pid, &host)
	if err == sql.ErrNoRows {
		return nil // nothing to stop
	}
	if err != nil {
		return err
	}

	// Get target to determine stop method
	target, err := f.GetTarget(targetID)
	if err != nil {
		return err
	}

	// Stop based on target kind
	switch target.Kind {
	case "ssh":
		if pid > 0 && host.Valid {
			stopRemoteProcess(target, pid)
		}
	case "local":
		if pid > 0 {
			stopLocalProcess(pid)
		}
	}

	return f.updatePreviewStatus(id, "stopped", "", 0)
}

// ListPreviews returns all previews, optionally filtered
func (f *Forge) ListPreviews(projectID string, statusFilter string) ([]Preview, error) {
	query := `SELECT id, project, slot_id, target_id, branch, commit_hash, host, port, pid, url, status, error, agent_id, session_id, orchestrator, started_at, stopped_at FROM previews`
	args := []any{}
	conditions := []string{}

	if projectID != "" {
		conditions = append(conditions, "project = ?")
		args = append(args, projectID)
	}
	if statusFilter != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, statusFilter)
	}
	if len(conditions) > 0 {
		query += " WHERE "
		for i, c := range conditions {
			if i > 0 {
				query += " AND "
			}
			query += c
		}
	}
	query += " ORDER BY started_at DESC"

	rows, err := f.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Preview
	for rows.Next() {
		var p Preview
		var host, commitHash, errorMsg, agentID, sessionID, orchestrator sql.NullString
		var startedAt, stoppedAt sql.NullInt64
		if err := rows.Scan(&p.ID, &p.Project, &p.SlotID, &p.TargetID, &p.Branch, &commitHash, &host, &p.Port, &p.PID, &p.URL, &p.Status, &errorMsg, &agentID, &sessionID, &orchestrator, &startedAt, &stoppedAt); err != nil {
			return nil, err
		}
		if host.Valid {
			p.Host = host.String
		}
		if commitHash.Valid {
			p.CommitHash = commitHash.String
		}
		if errorMsg.Valid {
			p.Error = errorMsg.String
		}
		if agentID.Valid {
			p.AgentID = agentID.String
		}
		if sessionID.Valid {
			p.SessionID = sessionID.String
		}
		if orchestrator.Valid {
			p.Orchestrator = orchestrator.String
		}
		if startedAt.Valid {
			t := time.Unix(startedAt.Int64, 0)
			p.StartedAt = &t
		}
		if stoppedAt.Valid {
			t := time.Unix(stoppedAt.Int64, 0)
			p.StoppedAt = &t
		}
		results = append(results, p)
	}
	return results, nil
}

// updatePreviewStatus updates a preview's status
func (f *Forge) updatePreviewStatus(id, status, errMsg string, pid int) error {
	if status == "stopped" || status == "failed" {
		_, err := f.db.Exec(`UPDATE previews SET status = ?, error = ?, stopped_at = ? WHERE id = ?`, status, errMsg, now(), id)
		return err
	}
	_, err := f.db.Exec(`UPDATE previews SET status = ?, error = ?, pid = ? WHERE id = ?`, status, errMsg, pid, id)
	return err
}
