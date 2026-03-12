package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kayushkin/forge"
)

func runAPI(f *forge.Forge, args []string) {
	port := "8150"
	for i, a := range args {
		if a == "--port" && i+1 < len(args) {
			port = args[i+1]
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/forge/environments", func(w http.ResponseWriter, r *http.Request) {
		cors(w)
		if r.Method == "OPTIONS" {
			return
		}
		data, err := getEnvironments(f)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	})
	mux.HandleFunc("/api/forge/deploys", func(w http.ResponseWriter, r *http.Request) {
		cors(w)
		if r.Method == "OPTIONS" {
			return
		}
		data, err := getDeploys(f)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	})
	mux.HandleFunc("/api/forge/topology", func(w http.ResponseWriter, r *http.Request) {
		cors(w)
		if r.Method == "OPTIONS" {
			return
		}
		data := getTopology(f)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	})
	mux.HandleFunc("/api/forge/deploy", func(w http.ResponseWriter, r *http.Request) {
		cors(w)
		if r.Method == "OPTIONS" {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "stub", "message": "deploy not implemented yet"})
	})

	fmt.Printf("forge api listening on :%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

func cors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

type envResponse struct {
	ID       int           `json:"id"`
	Name     string        `json:"name"`
	BasePort int           `json:"base_port"`
	Status   string        `json:"status"`
	Change   string        `json:"change"`
	Agents   []string      `json:"agents"`
	Services []serviceInfo `json:"services"`
	Repos    []repoInfo    `json:"repos"`
}

type serviceInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Health string `json:"health"`
	Ports  string `json:"ports"`
}

type repoInfo struct {
	Name          string `json:"name"`
	Branch        string `json:"branch"`
	Commit        string `json:"commit"`
	CommitMessage string `json:"commit_message"`
	Dirty         bool   `json:"dirty"`
	DirtyFiles    int    `json:"dirty_files"`
	Ahead         int    `json:"ahead"`
	Behind        int    `json:"behind"`
}

func getEnvironments(f *forge.Forge) ([]envResponse, error) {
	slots, err := f.ListProjectSlotsV3(defaultProject)
	if err != nil {
		return nil, err
	}

	home, _ := os.UserHomeDir()
	var envs []envResponse

	for _, s := range slots {
		agents, _ := f.SlotAgents(s.ID)
		if agents == nil {
			agents = []string{}
		}

		envDir := filepath.Join(home, "forge", "envs", fmt.Sprintf("env-%d", s.SlotNum))

		// Docker compose status
		services := getDockerServices(envDir)

		// Overall status from services
		status := "stopped"
		if s.Status == "active" {
			status = "active"
			for _, svc := range services {
				if svc.Status == "running" {
					status = "running"
					break
				}
			}
		}

		// Git repos
		repos := getRepoInfos(envDir)

		envs = append(envs, envResponse{
			ID:       s.SlotNum,
			Name:     fmt.Sprintf("env-%d", s.SlotNum),
			BasePort: s.BasePort,
			Status:   status,
			Change:   s.AgentID,
			Agents:   agents,
			Services: services,
			Repos:    repos,
		})
	}

	return envs, nil
}

func getDockerServices(envDir string) []serviceInfo {
	composePath := filepath.Join(envDir, "docker-compose.yml")
	if _, err := os.Stat(composePath); err != nil {
		return []serviceInfo{}
	}

	out, err := exec.Command("docker", "compose", "-f", composePath, "ps", "--format", "json").Output()
	if err != nil {
		return []serviceInfo{}
	}

	var services []serviceInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var c struct {
			Name    string `json:"Name"`
			Service string `json:"Service"`
			State   string `json:"State"`
			Health  string `json:"Health"`
			Ports   string `json:"Ports"`
			Status  string `json:"Status"`
		}
		if json.Unmarshal([]byte(line), &c) != nil {
			continue
		}
		name := c.Service
		if name == "" {
			name = c.Name
		}
		health := c.Health
		if health == "" {
			health = "none"
		}
		services = append(services, serviceInfo{
			Name:   name,
			Status: c.State,
			Health: health,
			Ports:  c.Ports,
		})
	}
	return services
}

func getRepoInfos(envDir string) []repoInfo {
	reposDir := filepath.Join(envDir, "repos")
	entries, err := os.ReadDir(reposDir)
	if err != nil {
		return []repoInfo{}
	}

	var repos []repoInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		repoPath := filepath.Join(reposDir, e.Name())
		gitDir := filepath.Join(repoPath, ".git")
		if _, err := os.Stat(gitDir); err != nil {
			continue
		}

		ri := repoInfo{Name: e.Name()}

		// Branch
		if out, err := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
			ri.Branch = strings.TrimSpace(string(out))
		}

		// Commit hash (short)
		if out, err := exec.Command("git", "-C", repoPath, "rev-parse", "--short", "HEAD").Output(); err == nil {
			ri.Commit = strings.TrimSpace(string(out))
		}

		// Commit message
		if out, err := exec.Command("git", "-C", repoPath, "log", "-1", "--format=%s").Output(); err == nil {
			ri.CommitMessage = strings.TrimSpace(string(out))
		}

		// Dirty
		if out, err := exec.Command("git", "-C", repoPath, "status", "--porcelain").Output(); err == nil {
			lines := strings.TrimSpace(string(out))
			if lines != "" {
				ri.Dirty = true
				ri.DirtyFiles = len(strings.Split(lines, "\n"))
			}
		}

		// Ahead/behind
		if out, err := exec.Command("git", "-C", repoPath, "rev-list", "--left-right", "--count", ri.Branch+"...origin/"+ri.Branch).Output(); err == nil {
			parts := strings.Fields(string(out))
			if len(parts) >= 2 {
				fmt.Sscanf(parts[0], "%d", &ri.Ahead)
				fmt.Sscanf(parts[1], "%d", &ri.Behind)
			}
		}

		repos = append(repos, ri)
	}
	return repos
}

type deployEntry struct {
	ID        int    `json:"id"`
	Slot      int    `json:"slot"`
	Agent     string `json:"agent"`
	Action    string `json:"action"`
	Detail    string `json:"detail"`
	Timestamp int64  `json:"timestamp"`
}

// --- Topology ---

type topologyResponse struct {
	Prod     topologyEnv   `json:"prod"`
	Staging  []topologyEnv `json:"staging"`
	External []topoNode    `json:"external"`
}

type topologyEnv struct {
	Name     string     `json:"name"`
	Nodes    []topoNode `json:"nodes"`
	Links    []topoLink `json:"links"`
	PortBase int        `json:"port_base,omitempty"`
}

type topoNode struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Type   string            `json:"type"` // service, external, client
	Port   int               `json:"port,omitempty"`
	Status string            `json:"status,omitempty"` // running, stopped, unknown
	Health string            `json:"health,omitempty"`
	Env    map[string]string `json:"env,omitempty"` // relevant env vars
	URL    string            `json:"url,omitempty"`
}

type topoLink struct {
	From    string `json:"from"`
	To      string `json:"to"`
	EnvVar  string `json:"env_var,omitempty"`
	Value   string `json:"value,omitempty"`
	Proto   string `json:"proto"` // http, ws, cli, sqlite
	Status  string `json:"status,omitempty"` // ok, error, unknown
}

// Service connection definitions — the single source of truth for wiring
type serviceDef struct {
	name     string
	intPort  int    // internal port the service listens on
	connects []connDef
}

type connDef struct {
	target string
	envVar string
	proto  string
}

// Prod services — only things that run on the Linode server
var prodServiceDefs = []serviceDef{
	{name: "nginx", intPort: 443, connects: []connDef{
		{target: "kayushkin", envVar: "", proto: "http"},
	}},
	{name: "kayushkin", intPort: 8080, connects: []connDef{
		{target: "si", envVar: "SI_WS_URL", proto: "ws"},
		{target: "logstack", envVar: "LOGSTACK_URL", proto: "http"},
		{target: "bus", envVar: "BUS_URL", proto: "http"},
		{target: "forge-api", envVar: "FORGE_API_URL", proto: "http"},
	}},
	{name: "bus", intPort: 8100, connects: []connDef{}},
	{name: "si", intPort: 8120, connects: []connDef{
		{target: "bus", envVar: "SI_BUS_URL", proto: "http"},
	}},
	{name: "logstack", intPort: 8088, connects: []connDef{}},
	{name: "forge-api", intPort: 8150, connects: []connDef{}},
}

// Staging services — run inside Docker Compose per env
var stagingServiceDefs = []serviceDef{
	{name: "kayushkin", intPort: 8080, connects: []connDef{
		{target: "si", envVar: "SI_WS_URL", proto: "ws"},
		{target: "logstack", envVar: "LOGSTACK_URL", proto: "http"},
		{target: "bus", envVar: "BUS_URL", proto: "http"},
	}},
	{name: "bus", intPort: 8100, connects: []connDef{}},
	{name: "si", intPort: 8120, connects: []connDef{
		{target: "bus", envVar: "SI_BUS_URL", proto: "http"},
	}},
	{name: "logstack", intPort: 8088, connects: []connDef{}},
	{name: "inber", intPort: 0, connects: []connDef{
		{target: "bus", envVar: "BUS_URL", proto: "http"},
	}},
}

func getTopology(f *forge.Forge) topologyResponse {
	home, _ := os.UserHomeDir()

	// --- Prod topology ---
	prod := topologyEnv{Name: "prod"}
	for _, sd := range prodServiceDefs {
		nodeType := "service"
		if sd.name == "nginx" {
			nodeType = "external"
		}
		node := topoNode{
			ID:   "prod-" + sd.name,
			Name: sd.name,
			Type: nodeType,
			Port: sd.intPort,
		}
		if sd.intPort > 0 {
			node.Status = checkPortStatus(sd.intPort)
		}
		prod.Nodes = append(prod.Nodes, node)

		for _, c := range sd.connects {
			link := topoLink{
				From:   "prod-" + sd.name,
				To:     "prod-" + c.target,
				EnvVar: c.envVar,
				Proto:  c.proto,
			}
			switch c.envVar {
			case "SI_WS_URL":
				link.Value = "ws://127.0.0.1:8090/ws"
			case "LOGSTACK_URL":
				link.Value = "http://127.0.0.1:8088"
			case "BUS_URL", "SI_BUS_URL":
				link.Value = "http://127.0.0.1:8100"
			case "FORGE_API_URL":
				link.Value = "http://127.0.0.1:8150"
			}
			prod.Links = append(prod.Links, link)
		}
	}

	// --- Staging topology (per env) ---
	slots, _ := f.ListProjectSlotsV3(defaultProject)
	var staging []topologyEnv

	for _, s := range slots {
		envDir := filepath.Join(home, "forge", "envs", fmt.Sprintf("env-%d", s.SlotNum))
		composePath := filepath.Join(envDir, "docker-compose.yml")
		portBase := 9000 + s.SlotNum*100

		env := topologyEnv{
			Name:     fmt.Sprintf("env-%d", s.SlotNum),
			PortBase: portBase,
		}

		// Get docker container status for this env
		containerStatus := map[string]serviceInfo{}
		for _, svc := range getDockerServices(envDir) {
			containerStatus[svc.Name] = svc
		}

		// Get env vars from compose if available
		composeEnv := parseComposeEnvVars(composePath)

		envPrefix := fmt.Sprintf("env%d-", s.SlotNum)
		for _, sd := range stagingServiceDefs {
			cs, ok := containerStatus[sd.name]
			// Map internal ports to external env-specific ports
			extPort := 0
			switch sd.name {
			case "kayushkin":
				extPort = portBase
			case "bus":
				extPort = portBase + 10
			case "si":
				extPort = portBase + 20
			case "logstack":
				extPort = portBase + 30
			}
			node := topoNode{
				ID:   envPrefix + sd.name,
				Name: sd.name,
				Type: "service",
				Port: extPort,
			}
			if ok {
				node.Status = cs.Status
				node.Health = cs.Health
			} else {
				node.Status = "stopped"
			}
			// Attach env vars from compose
			if envVars, ok := composeEnv[sd.name]; ok {
				node.Env = envVars
			}
			env.Nodes = append(env.Nodes, node)

			for _, c := range sd.connects {
				link := topoLink{
					From:   envPrefix + sd.name,
					To:     envPrefix + c.target,
					EnvVar: c.envVar,
					Proto:  c.proto,
				}
				// Docker internal addressing
				switch c.envVar {
				case "SI_WS_URL":
					link.Value = "ws://si:8120/ws"
				case "LOGSTACK_URL":
					link.Value = "http://logstack:8088"
				case "BUS_URL", "SI_BUS_URL":
					link.Value = "http://bus:8100"
				}
				env.Links = append(env.Links, link)
			}
		}

		// Nginx → kayushkin for this env
		env.Links = append(env.Links, topoLink{
			From: "nginx", To: envPrefix + "kayushkin",
			Proto: "http", Value: fmt.Sprintf("proxy_pass :%d", portBase),
		})

		staging = append(staging, env)
	}

	// External nodes
	external := []topoNode{
		{ID: "anthropic", Name: "Anthropic API", Type: "external", URL: "https://api.anthropic.com"},
		{ID: "github", Name: "GitHub", Type: "external", URL: "https://github.com"},
		{ID: "wsl-openclaw", Name: "OpenClaw (WSL)", Type: "external", URL: "ws://localhost:8100"},
	}

	return topologyResponse{
		Prod:     prod,
		Staging:  staging,
		External: external,
	}
}

// checkPortStatus checks if a TCP port is accepting connections locally.
func checkPortStatus(port int) string {
	out, err := exec.Command("ss", "-tlnH", fmt.Sprintf("sport = :%d", port)).Output()
	if err != nil {
		return "unknown"
	}
	if strings.TrimSpace(string(out)) != "" {
		return "running"
	}
	return "stopped"
}

// parseComposeEnvVars reads a docker-compose.yml and extracts environment vars per service.
// Simple parser — looks for `environment:` blocks under service names.
func parseComposeEnvVars(composePath string) map[string]map[string]string {
	result := make(map[string]map[string]string)
	data, err := os.ReadFile(composePath)
	if err != nil {
		return result
	}

	lines := strings.Split(string(data), "\n")
	var currentService string
	inEnv := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Top-level service detection (2-space indent, ends with :)
		if len(line) > 2 && line[0] == ' ' && line[1] == ' ' && line[2] != ' ' && strings.HasSuffix(trimmed, ":") {
			name := strings.TrimSuffix(trimmed, ":")
			if name != "volumes" && name != "name" {
				currentService = name
				inEnv = false
			}
		}

		// Environment block
		if currentService != "" && trimmed == "environment:" {
			inEnv = true
			continue
		}

		// Env var line (- KEY=VALUE)
		if inEnv && strings.HasPrefix(trimmed, "- ") {
			kv := strings.TrimPrefix(trimmed, "- ")
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) == 2 {
				if result[currentService] == nil {
					result[currentService] = make(map[string]string)
				}
				result[currentService][parts[0]] = parts[1]
			}
			continue
		}

		// End of environment block
		if inEnv && trimmed != "" && !strings.HasPrefix(trimmed, "- ") && !strings.HasPrefix(trimmed, "#") {
			inEnv = false
		}
	}
	return result
}

func getDeploys(f *forge.Forge) ([]deployEntry, error) {
	entries, err := f.SlotLog(0, 50)
	if err != nil {
		return nil, err
	}

	result := make([]deployEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, deployEntry{
			ID:        e.ID,
			Slot:      e.SlotNum,
			Agent:     e.AgentName,
			Action:    e.Action,
			Detail:    e.Detail,
			Timestamp: e.CreatedAt,
		})
	}
	return result, nil
}
