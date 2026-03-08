package forge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func gitPull(path string) error {
	cmd := exec.Command("git", "fetch", "origin", "main")
	cmd.Dir = path
	if out, err := cmd.CombinedOutput(); err != nil {
		return wrapGitErr("fetch", out, err)
	}
	cmd = exec.Command("git", "merge", "origin/main")
	cmd.Dir = path
	if out, err := cmd.CombinedOutput(); err != nil {
		return wrapGitErr("merge", out, err)
	}
	return nil
}

func gitCommit(path, msg string) error {
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = path
	if out, err := cmd.CombinedOutput(); err != nil {
		return wrapGitErr("add", out, err)
	}
	cmd = exec.Command("git", "commit", "-m", msg)
	cmd.Dir = path
	if out, err := cmd.CombinedOutput(); err != nil {
		return wrapGitErr("commit", out, err)
	}
	return nil
}

func gitPush(path, branch string) error {
	cmd := exec.Command("git", "push", "origin", branch)
	cmd.Dir = path
	if out, err := cmd.CombinedOutput(); err != nil {
		return wrapGitErr("push", out, err)
	}
	return nil
}

func gitDiff(path string) (string, error) {
	cmd := exec.Command("git", "diff", "main")
	cmd.Dir = path
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", wrapGitErr("diff", out, err)
	}
	return string(out), nil
}

func gitHead(path string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func resetWorktree(path string) {
	// Best effort reset
	cmd := exec.Command("git", "fetch", "origin", "main")
	cmd.Dir = path
	cmd.CombinedOutput()

	cmd = exec.Command("git", "reset", "--hard", "origin/main")
	cmd.Dir = path
	if _, err := cmd.CombinedOutput(); err != nil {
		cmd = exec.Command("git", "reset", "--hard", "main")
		cmd.Dir = path
		cmd.CombinedOutput()
	}

	cmd = exec.Command("git", "clean", "-fd")
	cmd.Dir = path
	cmd.CombinedOutput()
}

func wrapGitErr(op string, out []byte, err error) error {
	return &gitError{op: op, output: string(out), err: err}
}

type gitError struct {
	op     string
	output string
	err    error
}

func (e *gitError) Error() string {
	return "git " + e.op + ": " + e.err.Error() + "\n" + e.output
}

func expandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}
