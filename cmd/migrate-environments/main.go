package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/kayushkin/forge"
)

var defaultProjects = []struct {
	id        string
	baseRepo  string
	isPrimary bool
	portOff   int
}{
	{"inber", "~/life/repos/inber", true, -1},
	{"bus", "~/life/repos/bus", false, 10},
	{"si", "~/life/repos/si", false, 20},
	{"kayushkin", "~/life/repos/kayushkin.com", false, 0},
	{"agent-store", "~/life/repos/agent-store", false, -1},
	{"forge", "~/life/repos/forge", false, -1},
	{"model-store", "~/life/repos/model-store", false, -1},
}

func expandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[1:])
	}
	return path
}

func main() {
	envCount := flag.Int("envs", 3, "number of environments to create")
	dryRun := flag.Bool("dry-run", false, "show what would happen without making changes")
	flag.Parse()

	f, err := forge.Open("")
	if err != nil {
		log.Fatalf("open forge: %v", err)
	}
	defer f.Close()

	// Step 1: Register projects
	log.Println("=== Registering projects ===")
	for _, p := range defaultProjects {
		baseRepo := expandPath(p.baseRepo)
		
		// Check repo exists
		if _, err := os.Stat(filepath.Join(baseRepo, ".git")); err != nil {
			log.Printf("  SKIP %s: %s is not a git repo", p.id, baseRepo)
			continue
		}

		if *dryRun {
			log.Printf("  WOULD register %s -> %s (primary=%v, port_offset=%d)", p.id, baseRepo, p.isPrimary, p.portOff)
			continue
		}

		err := f.RegisterProject(forge.Project{
			ID:            p.id,
			BaseRepo:      baseRepo,
			IsPrimary:     p.isPrimary,
			PortOffset:    p.portOff,
			DefaultBranch: "main",
		})
		if err != nil {
			log.Printf("  ERROR registering %s: %v", p.id, err)
		} else {
			log.Printf("  ✓ %s -> %s", p.id, baseRepo)
		}
	}

	if *dryRun {
		log.Println("\n=== Would create environments ===")
		for i := 0; i < *envCount; i++ {
			name := fmt.Sprintf("env-%d", i)
			basePort := 9000 + (i * 100)
			log.Printf("  %s/ (base_port: %d)", name, basePort)
			for _, p := range defaultProjects {
				log.Printf("    %s/", p.id)
			}
		}
		return
	}

	// Step 2: Create environments
	log.Println("\n=== Creating environments ===")
	envs, _ := f.AllEnvironments()
	if len(envs) > 0 {
		log.Printf("  Found %d existing environments", len(envs))
	} else {
		if err := f.InitEnvironments(*envCount); err != nil {
			log.Fatalf("init environments: %v", err)
		}
		log.Printf("  ✓ Created %d environments", *envCount)
	}

	// Step 3: Show status
	log.Println("\n=== Environment Status ===")
	envs, err = f.AllEnvironments()
	if err != nil {
		log.Fatalf("list environments: %v", err)
	}

	for _, env := range envs {
		log.Printf("\n%s (status: %s, base_port: %d)", env.Name, env.Status, env.BasePort)
		
		repos, err := f.GetEnvironmentRepos(env.ID)
		if err != nil {
			log.Printf("  error loading repos: %v", err)
			continue
		}
		
		if len(repos) == 0 {
			log.Println("  (no repos initialized)")
		} else {
			for _, repo := range repos {
				dirty := ""
				if repo.Dirty {
					dirty = " [dirty]"
				}
				commit := repo.CommitHash
				if len(commit) > 7 {
					commit = commit[:7]
				}
				log.Printf("  - %s @ %s%s", repo.ProjectID, commit, dirty)
			}
		}
	}

	log.Println("\n=== Port Allocations ===")
	for _, env := range envs {
		log.Printf("%s:", env.Name)
		log.Printf("  kayushkin: %d (primary web)", env.BasePort+0)
		log.Printf("  bus:       %d", env.BasePort+10)
		log.Printf("  si:        %d", env.BasePort+20)
		log.Printf("  logstack:  %d", env.BasePort+30)
	}

	log.Println("\n✅ Migration complete!")
}
