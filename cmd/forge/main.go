package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/kayushkin/forge"
)

func main() {
	flag.Parse()

	if flag.NArg() == 0 {
		printUsage()
		os.Exit(1)
	}

	f, err := forge.Open("")
	if err != nil {
		log.Fatalf("open forge: %v", err)
	}
	defer f.Close()

	cmd := flag.Arg(0)
	args := flag.Args()[1:]

	ctx := context.Background()

	switch cmd {
	case "projects":
		listProjects(f)
	case "slots":
		listSlots(f, args)
	case "init":
		initProject(f, args)
	case "build":
		buildSlot(ctx, f, args)
	case "start":
		startSlot(ctx, f, args)
	case "stop":
		stopSlot(ctx, f, args)
	case "exec":
		execSlot(ctx, f, args)
	case "logs":
		logsSlot(ctx, f, args)
	case "shell":
		shellSlot(f, args)
	case "status":
		statusSlot(f, args)
	default:
		log.Fatalf("unknown command: %s", cmd)
	}
}

func printUsage() {
	fmt.Println("Forge - Container-based deployment manager")
	fmt.Println("")
	fmt.Println("Commands:")
	fmt.Println("  projects              List all projects")
	fmt.Println("  slots <project>       List slots for a project")
	fmt.Println("  init <project>        Initialize a project from config")
	fmt.Println("  build <project> <slot>  Build container for slot")
	fmt.Println("  start <project> <slot>  Start container for slot")
	fmt.Println("  stop <project> <slot>   Stop container for slot")
	fmt.Println("  exec <project> <slot> <cmd>  Run command in container")
	fmt.Println("  logs <project> <slot>  Show container logs")
	fmt.Println("  shell <project> <slot>  Interactive shell in container")
	fmt.Println("  status <project> <slot> Show slot status")
}

func listProjects(f *forge.Forge) {
	projects, err := f.ListProjectsV3()
	if err != nil {
		log.Fatalf("list projects: %v", err)
	}

	if len(projects) == 0 {
		fmt.Println("No projects found. Run 'forge init <project>' to create one.")
		return
	}

	fmt.Println("Projects:")
	fmt.Printf("%-20s %-20s %-10s %-10s\n", "ID", "NAME", "SLOTS", "BASE_PORT")
	fmt.Println(strings.Repeat("-", 70))
	for _, p := range projects {
		fmt.Printf("%-20s %-20s %-10d %-10d\n", p.ID, p.Name, p.SlotCount, p.BasePort)
	}
}

func listSlots(f *forge.Forge, args []string) {
	if len(args) == 0 {
		log.Fatal("usage: forge slots <project>")
	}
	projectID := args[0]

	slots, err := f.ListProjectSlotsV3(projectID)
	if err != nil {
		log.Fatalf("list slots: %v", err)
	}

	fmt.Printf("Slots for %s:\n", projectID)
	fmt.Printf("%-5s %-20s %-10s %-10s %-15s\n", "NUM", "CONTAINER", "STATUS", "BASE_PORT", "AGENT")
	fmt.Println(strings.Repeat("-", 70))
	for _, s := range slots {
		agent := s.AgentID
		if agent == "" {
			agent = "-"
		}
		fmt.Printf("%-5d %-20s %-10s %-10d %-15s\n", s.SlotNum, s.ContainerName, s.Status, s.BasePort, agent)
	}
}

func initProject(f *forge.Forge, args []string) {
	if len(args) == 0 {
		log.Fatal("usage: forge init <project-id>")
	}
	projectID := args[0]

	// Check if slots already exist
	slots, err := f.ListProjectSlotsV3(projectID)
	if err == nil && len(slots) > 0 {
		log.Printf("Project %s already has %d slots", projectID, len(slots))
		return
	}

	// Create slots for the project
	if err := f.InitSlotsV3(projectID); err != nil {
		log.Fatalf("init slots: %v", err)
	}

	fmt.Printf("✓ Initialized slots for %s\n", projectID)
}

func buildSlot(ctx context.Context, f *forge.Forge, args []string) {
	if len(args) < 2 {
		log.Fatal("usage: forge build <project> <slot-num>")
	}
	projectID := args[0]
	slotNum, err := strconv.Atoi(args[1])
	if err != nil {
		log.Fatalf("invalid slot number: %v", err)
	}

	// Find the slot
	slots, err := f.ListProjectSlotsV3(projectID)
	if err != nil {
		log.Fatalf("list slots: %v", err)
	}

	var slotID int
	for _, s := range slots {
		if s.SlotNum == slotNum {
			slotID = s.ID
			break
		}
	}
	if slotID == 0 {
		log.Fatalf("slot %d not found for project %s", slotNum, projectID)
	}

	fmt.Printf("Building slot %s-%d...\n", projectID, slotNum)
	if err := f.Containers().BuildSlot(ctx, slotID); err != nil {
		log.Fatalf("build: %v", err)
	}
	fmt.Printf("✓ Built slot %s-%d\n", projectID, slotNum)
}

func startSlot(ctx context.Context, f *forge.Forge, args []string) {
	if len(args) < 2 {
		log.Fatal("usage: forge start <project> <slot-num>")
	}
	projectID := args[0]
	slotNum, err := strconv.Atoi(args[1])
	if err != nil {
		log.Fatalf("invalid slot number: %v", err)
	}

	slots, err := f.ListProjectSlotsV3(projectID)
	if err != nil {
		log.Fatalf("list slots: %v", err)
	}

	var slotID int
	for _, s := range slots {
		if s.SlotNum == slotNum {
			slotID = s.ID
			break
		}
	}
	if slotID == 0 {
		log.Fatalf("slot %d not found", slotNum)
	}

	fmt.Printf("Starting slot %s-%d...\n", projectID, slotNum)
	if err := f.Containers().StartSlot(ctx, slotID); err != nil {
		log.Fatalf("start: %v", err)
	}
	fmt.Printf("✓ Started slot %s-%d\n", projectID, slotNum)
}

func stopSlot(ctx context.Context, f *forge.Forge, args []string) {
	if len(args) < 2 {
		log.Fatal("usage: forge stop <project> <slot-num>")
	}
	projectID := args[0]
	slotNum, err := strconv.Atoi(args[1])
	if err != nil {
		log.Fatalf("invalid slot number: %v", err)
	}

	slots, err := f.ListProjectSlotsV3(projectID)
	if err != nil {
		log.Fatalf("list slots: %v", err)
	}

	var slotID int
	for _, s := range slots {
		if s.SlotNum == slotNum {
			slotID = s.ID
			break
		}
	}
	if slotID == 0 {
		log.Fatalf("slot %d not found", slotNum)
	}

	fmt.Printf("Stopping slot %s-%d...\n", projectID, slotNum)
	if err := f.Containers().StopSlot(ctx, slotID); err != nil {
		log.Fatalf("stop: %v", err)
	}
	fmt.Printf("✓ Stopped slot %s-%d\n", projectID, slotNum)
}

func execSlot(ctx context.Context, f *forge.Forge, args []string) {
	if len(args) < 3 {
		log.Fatal("usage: forge exec <project> <slot-num> <command>")
	}
	projectID := args[0]
	slotNum, err := strconv.Atoi(args[1])
	if err != nil {
		log.Fatalf("invalid slot number: %v", err)
	}
	cmd := args[2:]
	cmdStr := strings.Join(cmd, " ")

	slots, err := f.ListProjectSlotsV3(projectID)
	if err != nil {
		log.Fatalf("list slots: %v", err)
	}

	var slotID int
	for _, s := range slots {
		if s.SlotNum == slotNum {
			slotID = s.ID
			break
		}
	}
	if slotID == 0 {
		log.Fatalf("slot %d not found", slotNum)
	}

	// Run command in shell
	output, err := f.Containers().Exec(ctx, slotID, "bash", "-c", cmdStr)
	if err != nil {
		log.Fatalf("exec: %v", err)
	}
	fmt.Print(output)
}

func logsSlot(ctx context.Context, f *forge.Forge, args []string) {
	if len(args) < 2 {
		log.Fatal("usage: forge logs <project> <slot-num>")
	}
	projectID := args[0]
	slotNum, err := strconv.Atoi(args[1])
	if err != nil {
		log.Fatalf("invalid slot number: %v", err)
	}

	slots, err := f.ListProjectSlotsV3(projectID)
	if err != nil {
		log.Fatalf("list slots: %v", err)
	}

	var slotID int
	for _, s := range slots {
		if s.SlotNum == slotNum {
			slotID = s.ID
			break
		}
	}
	if slotID == 0 {
		log.Fatalf("slot %d not found", slotNum)
	}

	output, err := f.Containers().Logs(ctx, slotID, 100)
	if err != nil {
		log.Fatalf("logs: %v", err)
	}
	fmt.Print(output)
}

func shellSlot(f *forge.Forge, args []string) {
	if len(args) < 2 {
		log.Fatal("usage: forge shell <project> <slot-num>")
	}
	projectID := args[0]
	slotNum, err := strconv.Atoi(args[1])
	if err != nil {
		log.Fatalf("invalid slot number: %v", err)
	}

	slots, err := f.ListProjectSlotsV3(projectID)
	if err != nil {
		log.Fatalf("list slots: %v", err)
	}

	var slotID int
	for _, s := range slots {
		if s.SlotNum == slotNum {
			slotID = s.ID
			break
		}
	}
	if slotID == 0 {
		log.Fatalf("slot %d not found", slotNum)
	}

	// Get container name
	slot, err := f.GetSlotV3(slotID)
	if err != nil {
		log.Fatalf("get slot: %v", err)
	}

	// Exec interactively (need to use exec.Command directly for tty)
	// This requires the parent process to have a tty
	log.Fatalf("Interactive shell requires tty. Use: docker exec -it %s bash", slot.ContainerName)
}

func statusSlot(f *forge.Forge, args []string) {
	if len(args) < 2 {
		log.Fatal("usage: forge status <project> <slot-num>")
	}
	projectID := args[0]
	slotNum, err := strconv.Atoi(args[1])
	if err != nil {
		log.Fatalf("invalid slot number: %v", err)
	}

	slots, err := f.ListProjectSlotsV3(projectID)
	if err != nil {
		log.Fatalf("list slots: %v", err)
	}

	for _, s := range slots {
		if s.SlotNum == slotNum {
			fmt.Printf("Slot %s-%d:\n", projectID, slotNum)
			fmt.Printf("  Container: %s\n", s.ContainerName)
			fmt.Printf("  Status:    %s\n", s.Status)
			fmt.Printf("  Base Port: %d\n", s.BasePort)
			fmt.Printf("  Agent:     %s\n", s.AgentID)
			if s.ContainerID != "" {
				fmt.Printf("  Container ID: %s\n", s.ContainerID[:12])
			}
			return
		}
	}
	log.Fatalf("slot %d not found", slotNum)
}
