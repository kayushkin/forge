package forge

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Target represents a preview deployment target
type Target struct {
	ID          string
	Kind        string // "ssh", "local", "docker"
	Host        string
	User        string
	DeployDir   string
	BasePort    int
	URLTemplate string // "{slot}.dev.kayushkin.com" or "localhost:{port}"
	CreatedAt   time.Time
}

// RegisterTarget registers or updates a preview target
func (f *Forge) RegisterTarget(t Target) error {
	if t.BasePort == 0 {
		t.BasePort = 9000
	}
	_, err := f.db.Exec(`
		INSERT INTO targets (id, kind, host, user, deploy_dir, base_port, url_template, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind = excluded.kind,
			host = excluded.host,
			user = excluded.user,
			deploy_dir = excluded.deploy_dir,
			base_port = excluded.base_port,
			url_template = excluded.url_template
	`, t.ID, t.Kind, t.Host, t.User, t.DeployDir, t.BasePort, t.URLTemplate, now())
	return err
}

// GetTarget retrieves a target by ID
func (f *Forge) GetTarget(id string) (*Target, error) {
	t := &Target{ID: id}
	var createdAt int64
	err := f.db.QueryRow(`
		SELECT kind, host, user, deploy_dir, base_port, url_template, created_at
		FROM targets WHERE id = ?
	`, id).Scan(&t.Kind, &t.Host, &t.User, &t.DeployDir, &t.BasePort, &t.URLTemplate, &createdAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("target %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	t.CreatedAt = time.Unix(createdAt, 0)
	return t, nil
}

// ListTargets returns all registered targets
func (f *Forge) ListTargets() ([]Target, error) {
	rows, err := f.db.Query(`
		SELECT id, kind, host, user, deploy_dir, base_port, url_template, created_at
		FROM targets ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Target
	for rows.Next() {
		var t Target
		var createdAt int64
		if err := rows.Scan(&t.ID, &t.Kind, &t.Host, &t.User, &t.DeployDir, &t.BasePort, &t.URLTemplate, &createdAt); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(createdAt, 0)
		results = append(results, t)
	}
	return results, nil
}

// ResolveURL builds the preview URL for a target + slot
func (t *Target) ResolveURL(slotID, port int) string {
	url := t.URLTemplate
	url = strings.ReplaceAll(url, "{slot}", fmt.Sprintf("%d", slotID))
	url = strings.ReplaceAll(url, "{port}", fmt.Sprintf("%d", port))
	return url
}

// ResolvePort returns the port for a given slot
func (t *Target) ResolvePort(slotID int) int {
	return t.BasePort + slotID
}
