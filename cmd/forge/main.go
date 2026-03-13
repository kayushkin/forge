package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kayushkin/forge"
)

// forgeScript resolves the path to a deploy script.
// Checks: 1) next to the forge binary, 2) ~/repos/forge/deploy/, 3) ~/life/repos/forge/deploy/
func forgeScript(name string) string {
	// Next to the binary itself
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		// Check sibling deploy/ dir (if binary is in repo's cmd output)
		candidate := filepath.Join(dir, "..", "repos", "forge", "deploy", name)
		if abs, err := filepath.Abs(candidate); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	// ~/repos/forge/deploy/ (server)
	if p := os.ExpandEnv("$HOME/repos/forge/deploy/" + name); fileExists(p) {
		return p
	}
	// ~/life/repos/forge/deploy/ (WSL)
	return os.ExpandEnv("$HOME/life/repos/forge/deploy/" + name)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

const defaultProject = "kayushkin-stack"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	f, err := forge.Open("")
	if err != nil {
		log.Fatalf("open forge: %v", err)
	}
	defer f.Close()

	// Ensure slot schema exists
	if err := f.InitSlotSchema(); err != nil {
		log.Fatalf("init slot schema: %v", err)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "slot":
		if len(args) == 0 {
			slotStatus(f)
			return
		}
		switch args[0] {
		case "open":
			slotOpen(f, args[1:])
		case "join":
			slotJoin(f, args[1:])
		case "leave":
			slotLeave(f, args[1:])
		case "close":
			slotClose(f, args[1:])
		case "status":
			slotStatus(f)
		case "deploy":
			slotDeploy(f, args[1:])
		case "log":
			slotLog(f, args[1:])
		default:
			log.Fatalf("unknown slot command: %s", args[0])
		}
	case "deploy":
		if len(args) == 0 {
			autoDeploy(f)
		} else if args[0] == "prod" {
			prodDeploy(args[1:])
		} else {
			// Legacy: forge deploy <service> → prod deploy
			prodDeploy(args)
		}
	case "init":
		initProject(f)
	case "api":
		runAPI(f, args)
	case "status":
		slotStatus(f)
	default:
		log.Fatalf("unknown command: %s", cmd)
	}
}

func printUsage() {
	fmt.Println(`forge — deployment management

Deploy (auto-detect from cwd):
  forge deploy                           Auto-detect slot from cwd, deploy current repo

Slot commands (staging environments):
  forge slot                             Show slot status
  forge slot open <change> <agent>       Claim a slot for a change (prints slot number)
  forge slot join <N> <agent>            Join an existing slot
  forge slot leave <N> <agent>           Leave a slot
  forge slot close <N>                   Release a slot
  forge slot deploy <N> <agent> [repo:branch...]  Deploy to a slot (explicit)
  forge slot log [N]                     Show activity log

Production:
  forge deploy prod <service|all>        Deploy to production

Setup:
  forge init                             Initialize project and slots in DB`)
}

// --- Init ---

func initProject(f *forge.Forge) {
	err := f.CreateProjectV3(forge.CreateProjectV3Opts{
		ID:        defaultProject,
		Name:      "Kayushkin Stack",
		SlotCount: 3,
		BasePort:  9000,
		PortCount: 30,
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			fmt.Println("Project already exists")
		} else {
			log.Fatalf("create project: %v", err)
		}
	} else {
		fmt.Println("✓ Created project:", defaultProject)
	}

	// Init slots
	if err := f.InitSlotsV3(defaultProject); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			fmt.Println("Slots already exist")
		} else {
			log.Fatalf("init slots: %v", err)
		}
	} else {
		fmt.Println("✓ Initialized 3 slots")
	}

	slotStatus(f)
}

// --- Slot commands ---

func slotOpen(f *forge.Forge, args []string) {
	if len(args) < 2 {
		log.Fatal("usage: forge slot open <change-name> <agent-name>")
	}
	changeName, agentName := args[0], args[1]

	info, err := f.OpenSlot(defaultProject, changeName, agentName)
	if err != nil {
		log.Fatalf("open slot: %v", err)
	}

	fmt.Println(info.SlotNum)
}

func slotJoin(f *forge.Forge, args []string) {
	if len(args) < 2 {
		log.Fatal("usage: forge slot join <N> <agent-name>")
	}
	slotNum, _ := strconv.Atoi(args[0])
	agentName := args[1]

	slot, err := f.GetSlotByNum(defaultProject, slotNum)
	if err != nil {
		log.Fatalf("slot %d not found: %v", slotNum, err)
	}

	if err := f.JoinSlot(slot.ID, agentName); err != nil {
		log.Fatalf("join: %v", err)
	}
	fmt.Printf("%s joined slot-%d\n", agentName, slotNum)
}

func slotLeave(f *forge.Forge, args []string) {
	if len(args) < 2 {
		log.Fatal("usage: forge slot leave <N> <agent-name>")
	}
	slotNum, _ := strconv.Atoi(args[0])
	agentName := args[1]

	slot, err := f.GetSlotByNum(defaultProject, slotNum)
	if err != nil {
		log.Fatalf("slot %d not found: %v", slotNum, err)
	}

	if err := f.LeaveSlot(slot.ID, agentName); err != nil {
		log.Fatalf("leave: %v", err)
	}
	fmt.Printf("%s left slot-%d\n", agentName, slotNum)
}

func slotClose(f *forge.Forge, args []string) {
	if len(args) < 1 {
		log.Fatal("usage: forge slot close <N> [--force]")
	}
	slotNum, _ := strconv.Atoi(args[0])
	force := len(args) > 1 && args[1] == "--force"

	slot, err := f.GetSlotByNum(defaultProject, slotNum)
	if err != nil {
		log.Fatalf("slot %d not found: %v", slotNum, err)
	}

	if force {
		if err := f.ForceCloseSlot(slot.ID); err != nil {
			log.Fatalf("force close: %v", err)
		}
	} else {
		if err := f.CloseSlot(slot.ID); err != nil {
			log.Fatalf("close: %v", err)
		}
	}
	fmt.Printf("slot-%d closed\n", slotNum)
}

func slotStatus(f *forge.Forge) {
	slots, err := f.ListProjectSlotsV3(defaultProject)
	if err != nil {
		log.Fatalf("list slots: %v", err)
	}

	if len(slots) == 0 {
		fmt.Println("No slots. Run 'forge init' first.")
		return
	}

	for _, s := range slots {
		if s.Status == "idle" {
			fmt.Printf("slot-%d: available\n", s.SlotNum)
		} else {
			agents, _ := f.SlotAgents(s.ID)
			agentStr := strings.Join(agents, ", ")
			if agentStr == "" {
				agentStr = "none"
			}
			// agent_id holds the change name
			fmt.Printf("slot-%d: \"%s\" | agents: [%s] | http://%d.dev.kayushkin.com\n",
				s.SlotNum, s.AgentID, agentStr, s.SlotNum)
		}
	}
}

func slotDeploy(f *forge.Forge, args []string) {
	if len(args) < 2 {
		log.Fatal("usage: forge slot deploy <N> <agent> [repo:branch...]")
	}
	slotNum, _ := strconv.Atoi(args[0])
	agentName := args[1]
	repoBranches := args[2:]

	slot, err := f.GetSlotByNum(defaultProject, slotNum)
	if err != nil {
		log.Fatalf("slot %d not found: %v", slotNum, err)
	}

	// Check membership
	isMember, err := f.IsSlotMember(slot.ID, agentName)
	if err != nil {
		log.Fatalf("check membership: %v", err)
	}
	if !isMember {
		log.Fatalf("%s is not on slot-%d. Run 'forge slot join %d %s' first.", agentName, slotNum, slotNum, agentName)
	}

	// Call the shell deploy script which handles SSH
	cmdArgs := []string{
		forgeScript("forge-staging"),
		"deploy-only", fmt.Sprint(slotNum), agentName,
	}
	cmdArgs = append(cmdArgs, repoBranches...)
	cmd := exec.Command("bash", cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("deploy failed: %v", err)
	}

	// Log the deploy
	detail := "repos=" + strings.Join(repoBranches, ",")
	if len(repoBranches) == 0 {
		detail = "repos=main"
	}
	f.LogSlotDeploy(slot.ID, agentName, detail)
}

func slotLog(f *forge.Forge, args []string) {
	var slotID int
	if len(args) > 0 {
		slotNum, _ := strconv.Atoi(args[0])
		slot, err := f.GetSlotByNum(defaultProject, slotNum)
		if err != nil {
			log.Fatalf("slot %d not found: %v", slotNum, err)
		}
		slotID = slot.ID
	}

	entries, err := f.SlotLog(slotID, 30)
	if err != nil {
		log.Fatalf("get log: %v", err)
	}

	if len(entries) == 0 {
		fmt.Println("No log entries.")
		return
	}

	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		t := time.Unix(e.CreatedAt, 0).Format("2006-01-02 15:04:05")
		detail := ""
		if e.Detail != "" {
			detail = " " + e.Detail
		}
		fmt.Printf("%s slot-%d %-8s %s%s\n", t, e.SlotNum, e.Action, e.AgentName, detail)
	}
}

// autoDeploy detects the slot and repo from the current working directory.
// Expects cwd to be inside ~/forge/envs/env-N/repos/<repo>/
func autoDeploy(f *forge.Forge) {
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}

	// Extract env number from path: .../envs/env-N/...
	slotNum, repo := parseEnvPath(cwd)
	if slotNum < 0 {
		log.Fatalf("not inside a slot repo. Expected path like ~/forge/envs/env-N/repos/<repo>/\ncwd: %s", cwd)
	}

	// Agent name for logging — use system user
	agentName := os.Getenv("USER")
	if agentName == "" {
		agentName = "agent"
	}

	slot, err := f.GetSlotByNum(defaultProject, slotNum)
	if err != nil {
		log.Fatalf("slot %d not found: %v", slotNum, err)
	}

	if slot.Status != "active" {
		log.Fatalf("slot-%d is not active. Run 'forge slot open <change> <agent>' first.", slotNum)
	}

	// Get current branch of the repo
	branch := ""
	if repo != "" {
		repoDir := cwd
		// Navigate to repo root if we're in a subdirectory
		if idx := strings.Index(cwd, "/repos/"+repo); idx >= 0 {
			repoDir = cwd[:idx+len("/repos/")+len(repo)]
		}
		branchCmd := exec.Command("git", "-C", repoDir, "rev-parse", "--abbrev-ref", "HEAD")
		out, err := branchCmd.Output()
		if err == nil {
			branch = strings.TrimSpace(string(out))
		}
	}

	fmt.Printf("Deploying: slot-%d, repo=%s, branch=%s\n", slotNum, repo, branch)

	// Build repo:branch arg for the deploy script
	var repoBranches []string
	if repo != "" && branch != "" {
		repoBranches = []string{repo + ":" + branch}
	}

	// Call the shell deploy script
	cmdArgs := []string{
		forgeScript("forge-staging"),
		"deploy-only", fmt.Sprint(slotNum), agentName,
	}
	cmdArgs = append(cmdArgs, repoBranches...)
	cmd := exec.Command("bash", cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("deploy failed: %v", err)
	}

	detail := "repo=" + repo
	if branch != "" {
		detail += " branch=" + branch
	}
	f.LogSlotDeploy(slot.ID, agentName, detail)
	fmt.Printf("\n✅ slot-%d deployed: http://%d.dev.kayushkin.com\n", slotNum, slotNum)
}

// parseEnvPath extracts the env number and repo name from a path like
// .../envs/env-2/repos/kayushkin.com/src/...
// Returns (-1, "") if not in an env path.
func parseEnvPath(path string) (int, string) {
	// Find "envs/env-N" or "env-N/repos"
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if !strings.HasPrefix(p, "env-") {
			continue
		}
		numStr := strings.TrimPrefix(p, "env-")
		num, err := strconv.Atoi(numStr)
		if err != nil {
			continue
		}
		// Look for repos/<name> after env-N
		repo := ""
		if i+2 < len(parts) && parts[i+1] == "repos" {
			repo = parts[i+2]
		}
		return num, repo
	}
	return -1, ""
}

func prodDeploy(args []string) {
	if len(args) < 1 {
		log.Fatal("usage: forge deploy <service|all>")
	}
	cmd := exec.Command("bash",
		forgeScript("forge-deploy-prod"),
		args[0])
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("deploy failed: %v", err)
	}
}
