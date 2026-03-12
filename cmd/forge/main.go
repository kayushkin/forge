package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/kayushkin/forge"
)

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
		prodDeploy(args)
	case "init":
		initProject(f)
	case "status":
		slotStatus(f)
	default:
		log.Fatalf("unknown command: %s", cmd)
	}
}

func printUsage() {
	fmt.Println(`forge — deployment management

Slot commands (staging environments):
  forge slot                             Show slot status
  forge slot open <change> <agent>       Claim a slot for a change (prints slot number)
  forge slot join <N> <agent>            Join an existing slot
  forge slot leave <N> <agent>           Leave a slot
  forge slot close <N>                   Release a slot
  forge slot deploy <N> <agent> [repo:branch...]  Deploy to a slot
  forge slot log [N]                     Show activity log

Production:
  forge deploy <service|all>             Deploy to production

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
		os.ExpandEnv("$HOME/life/repos/forge/deploy/forge-staging"),
		"deploy-only", fmt.Sprint(slotNum), agentName,
	}
	cmdArgs = append(cmdArgs, repoBranches...)
	cmd := exec.Command("bash", cmdArgs...)
	if len(repoBranches) > 0 {
		cmd.Args = append(cmd.Args, repoBranches...)
	}
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

func prodDeploy(args []string) {
	if len(args) < 1 {
		log.Fatal("usage: forge deploy <service|all>")
	}
	cmd := exec.Command("bash",
		os.ExpandEnv("$HOME/life/repos/forge/deploy/forge-deploy-prod"),
		args[0])
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("deploy failed: %v", err)
	}
}
