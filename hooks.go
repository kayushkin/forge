package forge

import (
	"path/filepath"
	"strings"
)

// Hook is the interface orchestrators implement to decide when forge acts.
// The orchestrator calls Evaluate() after tool results, and forge tells it
// what action (if any) to take.
type Hook struct {
	forge     *Forge
	project   string
	slotID    int
	targetID  string // which target to deploy to
	autoBuild bool   // auto-build on file changes
	autoPreview bool // auto-deploy preview on successful build
	
	// File patterns that trigger builds
	buildPatterns []string // e.g. ["*.go", "*.ts", "*.tsx"]
	// File patterns that trigger preview refresh
	previewPatterns []string // e.g. ["*.go", "*.html", "*.css"]
}

// HookConfig configures a forge hook for an orchestrator
type HookConfig struct {
	Project         string
	SlotID          int
	TargetID        string
	AutoBuild       bool
	AutoPreview     bool
	BuildPatterns   []string
	PreviewPatterns []string
}

// NewHook creates a hook tied to a specific project/slot
func (f *Forge) NewHook(cfg HookConfig) *Hook {
	if len(cfg.BuildPatterns) == 0 {
		cfg.BuildPatterns = []string{"*.go", "*.ts", "*.tsx", "*.js", "*.jsx", "*.css", "*.html"}
	}
	if len(cfg.PreviewPatterns) == 0 {
		cfg.PreviewPatterns = cfg.BuildPatterns
	}
	return &Hook{
		forge:           f,
		project:         cfg.Project,
		slotID:          cfg.SlotID,
		targetID:        cfg.TargetID,
		autoBuild:       cfg.AutoBuild,
		autoPreview:     cfg.AutoPreview,
		buildPatterns:   cfg.BuildPatterns,
		previewPatterns: cfg.PreviewPatterns,
	}
}

// Action represents what forge recommends after evaluating a tool result
type Action struct {
	Kind    string // "none", "build", "preview", "refresh"
	Reason  string // human-readable explanation
}

// Evaluate checks a tool result and returns what action forge recommends.
// Called by the orchestrator's PostToolResult hook.
//
// toolName: the tool that just ran (e.g. "write_file", "edit_file", "shell")
// toolInput: the tool's input (contains file paths, commands, etc.)
// toolOutput: the tool's output
// isError: whether the tool returned an error
func (h *Hook) Evaluate(toolName, toolInput, toolOutput string, isError bool) Action {
	if isError {
		return Action{Kind: "none"}
	}

	switch toolName {
	case "write_file", "edit_file":
		// Check if the file matches build patterns
		filePath := extractFilePath(toolInput)
		if filePath == "" {
			return Action{Kind: "none"}
		}

		if h.matchesPatterns(filePath, h.buildPatterns) {
			if h.autoBuild {
				return Action{
					Kind:   "build",
					Reason: "file changed: " + filePath,
				}
			}
		}

		if h.matchesPatterns(filePath, h.previewPatterns) && h.autoPreview {
			return Action{
				Kind:   "preview",
				Reason: "previewable file changed: " + filePath,
			}
		}

	case "shell":
		// Check if the command was a build/test command
		if containsBuildCommand(toolInput) && !isError {
			if h.autoPreview {
				return Action{
					Kind:   "refresh",
					Reason: "build/test succeeded",
				}
			}
		}
	}

	return Action{Kind: "none"}
}

// Execute runs the recommended action
func (h *Hook) Execute(action Action) error {
	switch action.Kind {
	case "build":
		return h.forge.SlotCommit(h.project, h.slotID, "auto: "+action.Reason)
	case "preview", "refresh":
		_, err := h.forge.StartPreview(PreviewRequest{
			Project:  h.project,
			SlotID:   h.slotID,
			TargetID: h.targetID,
		})
		return err
	}
	return nil
}

// matchesPatterns checks if a file path matches any of the given glob patterns
func (h *Hook) matchesPatterns(path string, patterns []string) bool {
	base := filepath.Base(path)
	for _, p := range patterns {
		if matched, _ := filepath.Match(p, base); matched {
			return true
		}
	}
	return false
}

// extractFilePath tries to extract a file path from tool input JSON
func extractFilePath(input string) string {
	// Simple extraction — look for "path" or "file_path" in JSON
	for _, key := range []string{`"path"`, `"file_path"`} {
		idx := strings.Index(input, key)
		if idx < 0 {
			continue
		}
		rest := input[idx+len(key):]
		// Skip `: "`
		colonIdx := strings.Index(rest, `"`)
		if colonIdx < 0 {
			continue
		}
		rest = rest[colonIdx+1:]
		endIdx := strings.Index(rest, `"`)
		if endIdx < 0 {
			continue
		}
		return rest[:endIdx]
	}
	return ""
}

// containsBuildCommand checks if a shell command looks like a build/test
func containsBuildCommand(input string) bool {
	buildKeywords := []string{
		"go build", "go test", "go run",
		"npm run build", "npm test", "npx",
		"make", "cargo build", "cargo test",
	}
	lower := strings.ToLower(input)
	for _, kw := range buildKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
