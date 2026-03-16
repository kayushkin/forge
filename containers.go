package forge

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// ContainerManager handles Docker operations for slots.
type ContainerManager struct {
	forge *Forge
	mu    sync.Mutex
}

// NewContainerManager creates a container manager.
func NewContainerManager(forge *Forge) *ContainerManager {
	return &ContainerManager{forge: forge}
}

// BuildSlot builds a Docker image for a slot.
func (cm *ContainerManager) BuildSlot(ctx context.Context, slotID int) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Get slot info
	slot, err := cm.getSlot(slotID)
	if err != nil {
		return err
	}

	// Get project info
	project, err := cm.getProject(slot.ProjectID)
	if err != nil {
		return err
	}

	// Update status
	cm.updateSlotStatus(slotID, "building")

	// Generate Dockerfile
	dockerfile := project.Dockerfile
	if project.DockerfileTemplate != "" {
		dockerfile, err = cm.getTemplate(project.DockerfileTemplate)
		if err != nil {
			return fmt.Errorf("get template: %w", err)
		}
	}

	// Get repos for this project
	repos, err := cm.getProjectRepos(slot.ProjectID)
	if err != nil {
		return fmt.Errorf("get repos: %w", err)
	}

	// Build image with repos mounted
	imageName := fmt.Sprintf("forge-%s-%d", slot.ProjectID, slot.SlotNum)
	buildCtx := fmt.Sprintf("/tmp/forge-build-%d", slotID)

	// Create build context
	if err := os.MkdirAll(buildCtx, 0755); err != nil {
		return fmt.Errorf("create build context: %w", err)
	}
	defer os.RemoveAll(buildCtx)

	// Write Dockerfile
	if err := os.WriteFile(buildCtx+"/Dockerfile", []byte(dockerfile), 0644); err != nil {
		return fmt.Errorf("write dockerfile: %w", err)
	}

	// Build command with repo mounts
	args := []string{"build", "-t", imageName, buildCtx}

	// Add build args for repo paths
	for _, repo := range repos {
		localPath := expandHome(repo.LocalPath)
		if localPath == "" {
			localPath = expandHome(repo.RepoPath)
		}
		if localPath != "" {
			args = append(args, "--mount", fmt.Sprintf("type=bind,src=%s,dst=/repos/%s", localPath, repo.RepoID))
		}
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		cm.updateSlotStatus(slotID, "error")
		return fmt.Errorf("docker build: %w\n%s", err, output)
	}

	// Update slot with image ID
	cm.updateSlotImage(slotID, imageName)

	return nil
}

// StartSlot starts a container for a slot.
func (cm *ContainerManager) StartSlot(ctx context.Context, slotID int) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	slot, err := cm.getSlot(slotID)
	if err != nil {
		return err
	}

	if slot.ImageID == "" {
		return fmt.Errorf("slot %d has no image, build first", slotID)
	}

	// Calculate ports
	basePort := slot.BasePort + slot.SlotNum*10

	// Start container
	containerName := slot.ContainerName
	args := []string{
		"run", "-d",
		"--name", containerName,
		"--network", "forge",
	}

	// Port mappings
	for i := 0; i < 10; i++ {
		port := basePort + i
		args = append(args, "-p", fmt.Sprintf("%d:%d", port, 9000+i))
	}

	// Mount repos
	repos, _ := cm.getProjectRepos(slot.ProjectID)
	for _, repo := range repos {
		localPath := expandHome(repo.LocalPath)
		if localPath == "" {
			localPath = expandHome(repo.RepoPath)
		}
		if localPath != "" {
			args = append(args, "-v", fmt.Sprintf("%s:/repos/%s", localPath, repo.RepoID))
		}
	}

	args = append(args, slot.ImageID)

	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		cm.updateSlotStatus(slotID, "error")
		return fmt.Errorf("docker run: %w\n%s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	cm.updateSlotContainer(slotID, containerID, "running")

	return nil
}

// StopSlot stops a slot's container.
func (cm *ContainerManager) StopSlot(ctx context.Context, slotID int) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	slot, err := cm.getSlot(slotID)
	if err != nil {
		return err
	}

	if slot.ContainerID == "" {
		return nil // Not running
	}

	cmd := exec.CommandContext(ctx, "docker", "stop", slot.ContainerName)
	if output, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[forge] stop container: %v\n%s", err, output)
	}

	cmd = exec.CommandContext(ctx, "docker", "rm", slot.ContainerName)
	if output, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[forge] rm container: %v\n%s", err, output)
	}

	cm.updateSlotContainer(slotID, "", "stopped")

	return nil
}

// Exec runs a command in a slot's container.
func (cm *ContainerManager) Exec(ctx context.Context, slotID int, cmd string, args ...string) (string, error) {
	slot, err := cm.getSlot(slotID)
	if err != nil {
		return "", err
	}

	if slot.ContainerID == "" {
		return "", fmt.Errorf("slot %d container not running", slotID)
	}

	dockerArgs := []string{"exec", slot.ContainerName, cmd}
	dockerArgs = append(dockerArgs, args...)

	execCmd := exec.CommandContext(ctx, "docker", dockerArgs...)
	output, err := execCmd.CombinedOutput()
	return string(output), err
}

// ExecInteractive runs an interactive command (for agent shells).
func (cm *ContainerManager) ExecInteractive(ctx context.Context, slotID int, cmd string) (*exec.Cmd, error) {
	slot, err := cm.getSlot(slotID)
	if err != nil {
		return nil, err
	}

	if slot.ContainerID == "" {
		return nil, fmt.Errorf("slot %d container not running", slotID)
	}

	execCmd := exec.CommandContext(ctx, "docker", "exec", "-it", slot.ContainerName, cmd)
	return execCmd, nil
}

// Logs returns container logs.
func (cm *ContainerManager) Logs(ctx context.Context, slotID int, tail int) (string, error) {
	slot, err := cm.getSlot(slotID)
	if err != nil {
		return "", err
	}

	if slot.ContainerID == "" {
		return "", fmt.Errorf("slot %d container not running", slotID)
	}

	args := []string{"logs"}
	if tail > 0 {
		args = append(args, "--tail", fmt.Sprint(tail))
	}
	args = append(args, slot.ContainerName)

	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// --- Helpers ---

type slotV3 struct {
	ID            int
	ProjectID     string
	SlotNum       int
	ContainerName string
	Status        string
	ContainerID   string
	ImageID       string
	BasePort      int
	AgentID       string
	SessionID     string
}

type projectV3 struct {
	ID                string
	Name              string
	Dockerfile        string
	DockerfileTemplate string
	BuildCmd          string
	TestCmd           string
	StartCmd          string
	BasePort          int
}

type projectRepo struct {
	ProjectID string
	RepoID    string
	RepoURL   string
	RepoPath  string
	LocalPath string
	Branch    string
}

func (cm *ContainerManager) getSlot(slotID int) (*slotV3, error) {
	var s slotV3
	err := cm.forge.db.QueryRow(`
		SELECT id, project_id, slot_num, container_name, status, container_id, image_id, base_port, agent_id, session_id
		FROM slots_v3 WHERE id = ?
	`, slotID).Scan(&s.ID, &s.ProjectID, &s.SlotNum, &s.ContainerName, &s.Status, &s.ContainerID, &s.ImageID, &s.BasePort, &s.AgentID, &s.SessionID)
	if err != nil {
		return nil, fmt.Errorf("slot %d not found: %w", slotID, err)
	}
	return &s, nil
}

func (cm *ContainerManager) getProject(projectID string) (*projectV3, error) {
	var p projectV3
	err := cm.forge.db.QueryRow(`
		SELECT id, name, dockerfile, dockerfile_template, build_cmd, test_cmd, start_cmd, base_port
		FROM projects_v3 WHERE id = ?
	`, projectID).Scan(&p.ID, &p.Name, &p.Dockerfile, &p.DockerfileTemplate, &p.BuildCmd, &p.TestCmd, &p.StartCmd, &p.BasePort)
	if err != nil {
		return nil, fmt.Errorf("project %s not found: %w", projectID, err)
	}
	return &p, nil
}

func (cm *ContainerManager) getProjectRepos(projectID string) ([]projectRepo, error) {
	rows, err := cm.forge.db.Query(`
		SELECT pr.project_id, pr.repo_id, pr.repo_url, pr.repo_path, r.local_path, pr.branch
		FROM project_repos pr
		LEFT JOIN repos r ON pr.repo_id = r.id
		WHERE pr.project_id = ?
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repos []projectRepo
	for rows.Next() {
		var r projectRepo
		var repoURL, localPath sql.NullString
		if err := rows.Scan(&r.ProjectID, &r.RepoID, &repoURL, &r.RepoPath, &localPath, &r.Branch); err != nil {
			return nil, err
		}
		r.RepoURL = repoURL.String
		r.LocalPath = localPath.String
		repos = append(repos, r)
	}
	return repos, nil
}

func (cm *ContainerManager) getTemplate(templateID string) (string, error) {
	var dockerfile string
	err := cm.forge.db.QueryRow(`SELECT dockerfile FROM dockerfile_templates WHERE id = ?`, templateID).Scan(&dockerfile)
	if err != nil {
		return "", fmt.Errorf("template %s not found: %w", templateID, err)
	}
	return dockerfile, nil
}

func (cm *ContainerManager) updateSlotStatus(slotID int, status string) {
	cm.forge.db.Exec(`UPDATE slots_v3 SET status = ? WHERE id = ?`, status, slotID)
}

func (cm *ContainerManager) updateSlotImage(slotID int, imageID string) {
	cm.forge.db.Exec(`UPDATE slots_v3 SET image_id = ? WHERE id = ?`, imageID, slotID)
}

func (cm *ContainerManager) updateSlotContainer(slotID int, containerID, status string) {
	cm.forge.db.Exec(`UPDATE slots_v3 SET container_id = ?, status = ? WHERE id = ?`, containerID, status, slotID)
}
