# forge

Workspace and preview management for orchestrated agents.

Forge manages isolated git worktree slots, builds, and preview deployments — tying them to orchestration sessions so you always know which agent built what, where it's running, and how to see it.

## Concepts

| Concept | What it is |
|---------|------------|
| **Project** | A registered git repo with build/serve config |
| **Slot** | An isolated worktree workspace, leased to an agent |
| **Target** | Where previews run (SSH server, localhost, Docker) |
| **Preview** | A running instance of a slot's work on a target |
| **Hook** | Evaluates tool results, triggers builds/previews automatically |

## Database

`~/.config/forge/forge.db`

## Usage

```go
import "github.com/kayushkin/forge"

f, _ := forge.Open("")

// Register a project
f.RegisterProject(forge.Project{
    ID:       "kayushkin",
    BaseRepo: "~/life/repos/kayushkin.com",
    PoolDir:  "~/life/repos/.pools/kayushkin",
    PoolSize: 3,
    BuildCmd: "go build -o server .",
    ServeCmd: "./server -port {port} -build ./build",
    RepoURL:  "git@github.com:kayushkin/kayushkin.com.git",
})

// Register a preview target
f.RegisterTarget(forge.Target{
    ID:          "kayushkin-dev",
    Kind:        "ssh",
    Host:        "kayushkin.com",
    User:        "kayushkincom",
    DeployDir:   "dev",
    BasePort:    9000,
    URLTemplate: "{slot}.dev.kayushkin.com",
})

// Initialize worktree slots
f.InitSlots("kayushkin")

// Agent acquires a slot
slot, _ := f.Acquire("kayushkin", forge.AcquireOpts{
    AgentID:      "brigid",
    SessionID:    "sess-abc",
    Orchestrator: "inber",
})
// slot.Path = ~/life/repos/.pools/kayushkin/slot-1

// Deploy preview
preview, _ := f.StartPreview(forge.PreviewRequest{
    Project:  "kayushkin",
    SlotID:   1,
    TargetID: "kayushkin-dev",
})
// preview.URL = "1.dev.kayushkin.com"

// Check status
slots, _ := f.AllSlots()
previews, _ := f.ListPreviews("", "running")

// Cleanup
f.StopPreview("kayushkin", 1)
f.Release("kayushkin", 1)
```

## Hook Integration

Orchestrators integrate via hooks that evaluate tool results:

```go
// In inber's engine setup:
hook := f.NewHook(forge.HookConfig{
    Project:     "kayushkin",
    SlotID:      slot.ID,
    TargetID:    "kayushkin-dev",
    AutoBuild:   true,
    AutoPreview: true,
})

// In PostToolResult callback:
action := hook.Evaluate(toolName, toolInput, toolOutput, isError)
if action.Kind != "none" {
    hook.Execute(action)
}
```

## Architecture

```
Orchestrator (inber/openclaw)
  → PostToolResult hook
    → forge.Hook.Evaluate()
      → "should we build/preview?"
        → forge.StartPreview()
          → SSH deploy / local start / Docker run
            → N.dev.kayushkin.com live
```

Forge is the execution layer. It doesn't decide when to act — the orchestrator's hook does. Forge just manages slots, runs builds, and deploys previews.
