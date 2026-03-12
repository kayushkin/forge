package forge

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const slotSchemaSQL = `
CREATE TABLE IF NOT EXISTS slot_agents (
    slot_id INTEGER NOT NULL,
    agent_name TEXT NOT NULL,
    joined_at INTEGER NOT NULL,
    PRIMARY KEY (slot_id, agent_name),
    FOREIGN KEY (slot_id) REFERENCES slots_v3(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS slot_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    slot_id INTEGER NOT NULL,
    agent_name TEXT NOT NULL,
    action TEXT NOT NULL,
    detail TEXT,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_slot_log_slot ON slot_log(slot_id);
`

// SlotInfo is the full state of a slot including agents.
type SlotInfo struct {
	SlotV3
	ChangeName string
	Agents     []string
	Deploys    int
}

// OpenSlot claims an idle slot for a change, with the first agent.
func (f *Forge) OpenSlot(projectID, changeName, agentName string) (*SlotInfo, error) {
	now := time.Now().Unix()

	tx, err := f.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Find idle slot
	var slotID, slotNum, basePort int
	var containerName string
	err = tx.QueryRow(`
		SELECT id, slot_num, container_name, base_port
		FROM slots_v3
		WHERE project_id = ? AND status = 'idle'
		ORDER BY slot_num
		LIMIT 1
	`, projectID).Scan(&slotID, &slotNum, &containerName, &basePort)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no idle slots for project %s", projectID)
	}
	if err != nil {
		return nil, err
	}

	// Claim it
	_, err = tx.Exec(`
		UPDATE slots_v3
		SET status = 'active', agent_id = ?, session_id = ?, acquired_at = ?
		WHERE id = ?
	`, changeName, agentName, now, slotID)
	if err != nil {
		return nil, err
	}

	// Add first agent
	_, err = tx.Exec(`
		INSERT INTO slot_agents (slot_id, agent_name, joined_at)
		VALUES (?, ?, ?)
	`, slotID, agentName, now)
	if err != nil {
		return nil, err
	}

	// Log it
	_, err = tx.Exec(`
		INSERT INTO slot_log (slot_id, agent_name, action, detail, created_at)
		VALUES (?, ?, 'open', ?, ?)
	`, slotID, agentName, "change="+changeName, now)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &SlotInfo{
		SlotV3: SlotV3{
			ID:            slotID,
			ProjectID:     projectID,
			SlotNum:       slotNum,
			ContainerName: containerName,
			Status:        "active",
			BasePort:      basePort,
			AgentID:       changeName,
			SessionID:     agentName,
		},
		ChangeName: changeName,
		Agents:     []string{agentName},
	}, nil
}

// JoinSlot adds an agent to an active slot.
func (f *Forge) JoinSlot(slotID int, agentName string) error {
	now := time.Now().Unix()

	// Check slot is active
	var status string
	err := f.db.QueryRow(`SELECT status FROM slots_v3 WHERE id = ?`, slotID).Scan(&status)
	if err != nil {
		return fmt.Errorf("slot %d not found: %w", slotID, err)
	}
	if status != "active" {
		return fmt.Errorf("slot %d is %s, not active", slotID, status)
	}

	_, err = f.db.Exec(`
		INSERT OR IGNORE INTO slot_agents (slot_id, agent_name, joined_at)
		VALUES (?, ?, ?)
	`, slotID, agentName, now)
	if err != nil {
		return err
	}

	f.logSlotAction(slotID, agentName, "join", "")
	return nil
}

// LeaveSlot removes an agent from a slot.
func (f *Forge) LeaveSlot(slotID int, agentName string) error {
	_, err := f.db.Exec(`
		DELETE FROM slot_agents WHERE slot_id = ? AND agent_name = ?
	`, slotID, agentName)
	if err != nil {
		return err
	}

	f.logSlotAction(slotID, agentName, "leave", "")
	return nil
}

// CloseSlot releases a slot. Fails if agents are still on it.
func (f *Forge) CloseSlot(slotID int) error {
	// Check for remaining agents
	agents, err := f.SlotAgents(slotID)
	if err != nil {
		return err
	}
	if len(agents) > 0 {
		return fmt.Errorf("slot %d still has agents: %s", slotID, strings.Join(agents, ", "))
	}

	_, err = f.db.Exec(`
		UPDATE slots_v3
		SET status = 'idle', agent_id = NULL, session_id = NULL, acquired_at = NULL
		WHERE id = ?
	`, slotID)
	if err != nil {
		return err
	}

	f.logSlotAction(slotID, "system", "close", "")
	return nil
}

// ForceCloseSlot releases a slot regardless of agents.
func (f *Forge) ForceCloseSlot(slotID int) error {
	f.db.Exec(`DELETE FROM slot_agents WHERE slot_id = ?`, slotID)

	_, err := f.db.Exec(`
		UPDATE slots_v3
		SET status = 'idle', agent_id = NULL, session_id = NULL, acquired_at = NULL
		WHERE id = ?
	`, slotID)
	if err != nil {
		return err
	}

	f.logSlotAction(slotID, "system", "force-close", "")
	return nil
}

// IsSlotMember checks if an agent is on a slot.
func (f *Forge) IsSlotMember(slotID int, agentName string) (bool, error) {
	var count int
	err := f.db.QueryRow(`
		SELECT COUNT(*) FROM slot_agents WHERE slot_id = ? AND agent_name = ?
	`, slotID, agentName).Scan(&count)
	return count > 0, err
}

// SlotAgents returns agents on a slot.
func (f *Forge) SlotAgents(slotID int) ([]string, error) {
	rows, err := f.db.Query(`
		SELECT agent_name FROM slot_agents WHERE slot_id = ? ORDER BY joined_at
	`, slotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		agents = append(agents, name)
	}
	return agents, nil
}

// LogSlotDeploy records a deploy action for a slot.
func (f *Forge) LogSlotDeploy(slotID int, agentName, detail string) {
	f.logSlotAction(slotID, agentName, "deploy", detail)
}

// SlotLog returns recent log entries for a slot (or all slots if slotID=0).
func (f *Forge) SlotLog(slotID int, limit int) ([]SlotLogEntry, error) {
	var rows *sql.Rows
	var err error
	if slotID > 0 {
		rows, err = f.db.Query(`
			SELECT sl.id, sl.slot_id, s.slot_num, sl.agent_name, sl.action, sl.detail, sl.created_at
			FROM slot_log sl
			JOIN slots_v3 s ON sl.slot_id = s.id
			WHERE sl.slot_id = ?
			ORDER BY sl.id DESC LIMIT ?
		`, slotID, limit)
	} else {
		rows, err = f.db.Query(`
			SELECT sl.id, sl.slot_id, s.slot_num, sl.agent_name, sl.action, sl.detail, sl.created_at
			FROM slot_log sl
			JOIN slots_v3 s ON sl.slot_id = s.id
			ORDER BY sl.id DESC LIMIT ?
		`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []SlotLogEntry
	for rows.Next() {
		var e SlotLogEntry
		var detail sql.NullString
		if err := rows.Scan(&e.ID, &e.SlotID, &e.SlotNum, &e.AgentName, &e.Action, &detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Detail = detail.String
		entries = append(entries, e)
	}
	return entries, nil
}

// GetSlotByNum finds a slot by project + slot number.
func (f *Forge) GetSlotByNum(projectID string, slotNum int) (*SlotV3, error) {
	var slot SlotV3
	var containerID, imageID, agentID, sessionID sql.NullString
	err := f.db.QueryRow(`
		SELECT id, project_id, slot_num, container_name, status, container_id, image_id, base_port, agent_id, session_id
		FROM slots_v3
		WHERE project_id = ? AND slot_num = ?
	`, projectID, slotNum).Scan(&slot.ID, &slot.ProjectID, &slot.SlotNum, &slot.ContainerName,
		&slot.Status, &containerID, &imageID, &slot.BasePort, &agentID, &sessionID)
	if err != nil {
		return nil, err
	}
	slot.ContainerID = containerID.String
	slot.ImageID = imageID.String
	slot.AgentID = agentID.String
	slot.SessionID = sessionID.String
	return &slot, nil
}

func (f *Forge) logSlotAction(slotID int, agent, action, detail string) {
	f.db.Exec(`
		INSERT INTO slot_log (slot_id, agent_name, action, detail, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, slotID, agent, action, detail, time.Now().Unix())
}

type SlotLogEntry struct {
	ID        int
	SlotID    int
	SlotNum   int
	AgentName string
	Action    string
	Detail    string
	CreatedAt int64
}

// InitSlotSchema creates the slot_agents and slot_log tables.
func (f *Forge) InitSlotSchema() error {
	_, err := f.db.Exec(slotSchemaSQL)
	return err
}
