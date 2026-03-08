package forge

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// deploySSH deploys a preview to a remote server via SSH
func (f *Forge) deploySSH(p *Preview, t *Target, localPath string) error {
	// 1. Push branch to remote so server can pull it
	branch := p.Branch
	if err := gitPush(localPath, branch); err != nil {
		return fmt.Errorf("push branch: %w", err)
	}

	// 2. Get project config for build/serve commands
	proj, err := f.GetProject(p.Project)
	if err != nil {
		return fmt.Errorf("get project: %w", err)
	}

	slotDir := fmt.Sprintf("%s/slot-%d", t.DeployDir, p.SlotID)

	// 3. Clone/update on remote
	cloneCmd := fmt.Sprintf(`
		mkdir -p ~/%s
		if [ -d ~/%s/.git ]; then
			cd ~/%s && git fetch origin && git checkout %s 2>/dev/null || git checkout -b %s origin/%s && git reset --hard origin/%s
		else
			rm -rf ~/%s && git clone --branch %s --single-branch %s ~/%s
		fi
	`, slotDir, slotDir, slotDir, branch, branch, branch, branch, slotDir, branch, proj.RepoURL, slotDir)

	if err := sshExec(t, cloneCmd); err != nil {
		return fmt.Errorf("clone/update: %w", err)
	}

	// 4. Build on remote
	if proj.BuildCmd != "" {
		buildCmd := fmt.Sprintf("cd ~/%s && export PATH=$HOME/.local/share/mise/shims:$PATH && %s", slotDir, proj.BuildCmd)
		if err := sshExec(t, buildCmd); err != nil {
			return fmt.Errorf("build: %w", err)
		}
	}

	// 5. Stop existing process on that port
	stopCmd := fmt.Sprintf("fuser -k %d/tcp 2>/dev/null || true", p.Port)
	sshExec(t, stopCmd)

	// 6. Start serve command
	if proj.ServeCmd != "" {
		serveCmd := proj.ServeCmd
		serveCmd = strings.ReplaceAll(serveCmd, "{port}", strconv.Itoa(p.Port))
		serveCmd = strings.ReplaceAll(serveCmd, "{path}", "~/"+slotDir)

		startCmd := fmt.Sprintf(`cd ~/%s && nohup %s > ~/logs/forge-slot-%d.log 2>&1 & echo $!`, slotDir, serveCmd, p.SlotID)
		out, err := sshOutput(t, startCmd)
		if err != nil {
			return fmt.Errorf("start server: %w", err)
		}

		// Parse PID from output
		pidStr := strings.TrimSpace(out)
		if pid, err := strconv.Atoi(pidStr); err == nil {
			p.PID = pid
			f.updatePreviewStatus(p.ID, "running", "", pid)
		}
	}

	return nil
}

// deployLocal deploys a preview locally
func (f *Forge) deployLocal(p *Preview, t *Target, localPath string) error {
	proj, err := f.GetProject(p.Project)
	if err != nil {
		return fmt.Errorf("get project: %w", err)
	}

	// Build locally
	if proj.BuildCmd != "" {
		cmd := exec.Command("sh", "-c", proj.BuildCmd)
		cmd.Dir = localPath
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("build: %w\n%s", err, out)
		}
	}

	// Start serve command
	if proj.ServeCmd != "" {
		serveCmd := proj.ServeCmd
		serveCmd = strings.ReplaceAll(serveCmd, "{port}", strconv.Itoa(p.Port))
		serveCmd = strings.ReplaceAll(serveCmd, "{path}", localPath)

		cmd := exec.Command("sh", "-c", serveCmd)
		cmd.Dir = localPath
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start server: %w", err)
		}

		p.PID = cmd.Process.Pid
		f.updatePreviewStatus(p.ID, "running", "", p.PID)

		// Detach so it survives
		cmd.Process.Release()
	}

	return nil
}

// sshExec runs a command on a remote target
func sshExec(t *Target, command string) error {
	cmd := exec.Command("ssh", fmt.Sprintf("%s@%s", t.User, t.Host), command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", command[:min(50, len(command))], err, out)
	}
	return nil
}

// sshOutput runs a command on a remote target and returns stdout
func sshOutput(t *Target, command string) (string, error) {
	cmd := exec.Command("ssh", fmt.Sprintf("%s@%s", t.User, t.Host), command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, out)
	}
	return string(out), nil
}

// stopRemoteProcess kills a process on a remote target
func stopRemoteProcess(t *Target, pid int) {
	sshExec(t, fmt.Sprintf("kill %d 2>/dev/null || true", pid))
}

// stopLocalProcess kills a local process
func stopLocalProcess(pid int) {
	syscall.Kill(pid, syscall.SIGTERM)
}
