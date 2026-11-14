// Package project inspects the current directory for project ecosystems,
// Git status, configurations, and automation tasks.
package project

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"nexshell/internal/registry"
	"nexshell/internal/task"
)

// ProjectCmd inspects and summarizes the current development project.
// Satisfies Demo 1: `nex> project`
type ProjectCmd struct{}

func (c *ProjectCmd) Name() string        { return "project" }
func (c *ProjectCmd) Description() string { return "Detect project type, Git status, tests and configuration" }
func (c *ProjectCmd) PipelineSafe() bool  { return true }

func (c *ProjectCmd) Run(ctx registry.Context, _ []string) error {
	dir := "."
	if ctx.CWD != nil {
		dir = *ctx.CWD
	}

	tw := tabwriter.NewWriter(ctx.Output(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(ctx.Output(), "\033[36m─── NexShell Project Detection ───────────────────────────────\033[0m")

	// 1. Project Type & Tech Stack
	projType := detectProjectType(dir)
	fmt.Fprintf(tw, "  Type:\t%s\n", projType)
	fmt.Fprintf(tw, "  Path:\t%s\n", dir)

	// 2. Git Status
	gitBranch, gitStatus, gitCommit := detectGitInfo(dir)
	fmt.Fprintf(tw, "  Git Branch:\t%s\n", gitBranch)
	fmt.Fprintf(tw, "  Git Status:\t%s\n", gitStatus)
	if gitCommit != "" {
		fmt.Fprintf(tw, "  Last Commit:\t%s\n", gitCommit)
	}

	// 3. Automation / Tasks in nex.yaml
	nexYamlPath := filepath.Join(dir, "nex.yaml")
	if cfg, err := task.Load(nexYamlPath); err == nil {
		fmt.Fprintf(tw, "  Automation:\tnex.yaml (Project: %s, Version: %s)\n", cfg.Project, cfg.Version)
		var taskList []string
		for tName := range cfg.Tasks {
			taskList = append(taskList, tName)
		}
		if len(taskList) > 0 {
			fmt.Fprintf(tw, "  Tasks:\t%s\n", strings.Join(taskList, ", "))
		}
	} else {
		fmt.Fprintf(tw, "  Automation:\t(no nex.yaml found)\n")
	}

	// 4. Test Framework
	testsDetected := detectTests(dir)
	fmt.Fprintf(tw, "  Tests:\t%s\n", testsDetected)

	_ = tw.Flush()
	fmt.Fprintln(ctx.Output(), "\033[36m──────────────────────────────────────────────────────────────\033[0m")
	return nil
}

func detectProjectType(dir string) string {
	var types []string

	if fileExists(filepath.Join(dir, "go.mod")) {
		modContent, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			lines := strings.Split(string(modContent), "\n")
			for _, l := range lines {
				if strings.HasPrefix(l, "module ") {
					types = append(types, fmt.Sprintf("Go (%s)", strings.TrimSpace(strings.TrimPrefix(l, "module "))))
					break
				}
			}
		}
		if len(types) == 0 {
			types = append(types, "Go")
		}
	}

	if fileExists(filepath.Join(dir, "package.json")) {
		types = append(types, "Node.js / TypeScript")
	}
	if fileExists(filepath.Join(dir, "pyproject.toml")) || fileExists(filepath.Join(dir, "requirements.txt")) {
		types = append(types, "Python")
	}
	if fileExists(filepath.Join(dir, "Cargo.toml")) {
		types = append(types, "Rust")
	}
	if fileExists(filepath.Join(dir, "pom.xml")) || fileExists(filepath.Join(dir, "build.gradle")) {
		types = append(types, "Java")
	}
	if fileExists(filepath.Join(dir, "Dockerfile")) || fileExists(filepath.Join(dir, "docker-compose.yml")) {
		types = append(types, "Docker")
	}

	if len(types) == 0 {
		return "Generic / Unknown"
	}
	return strings.Join(types, ", ")
}

func detectGitInfo(dir string) (branch, status, lastCommit string) {
	dotGit := filepath.Join(dir, ".git")
	if !fileExists(dotGit) {
		return "not a git repository", "-", ""
	}

	// 1. Branch
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	if out, err := cmd.Output(); err == nil {
		branch = strings.TrimSpace(string(out))
	} else {
		// Read directly from .git/HEAD
		headContent, err := os.ReadFile(filepath.Join(dotGit, "HEAD"))
		if err == nil {
			str := strings.TrimSpace(string(headContent))
			if strings.HasPrefix(str, "ref: refs/heads/") {
				branch = strings.TrimPrefix(str, "ref: refs/heads/")
			} else {
				branch = str[:min(8, len(str))]
			}
		} else {
			branch = "unknown"
		}
	}

	// 2. Status
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dir
	if out, err := statusCmd.Output(); err == nil {
		trimmed := strings.TrimSpace(string(out))
		if trimmed == "" {
			status = "clean (up to date)"
		} else {
			lines := strings.Split(trimmed, "\n")
			status = fmt.Sprintf("%d uncommitted change(s)", len(lines))
		}
	} else {
		status = "clean"
	}

	// 3. Last commit
	logCmd := exec.Command("git", "log", "-1", "--format=%h %s (%cr)")
	logCmd.Dir = dir
	if out, err := logCmd.Output(); err == nil {
		lastCommit = strings.TrimSpace(string(out))
	}

	return branch, status, lastCommit
}

func detectTests(dir string) string {
	var tests []string
	if fileExists(filepath.Join(dir, "go.mod")) {
		tests = append(tests, "Go Tests (`go test ./...`)")
	}
	if fileExists(filepath.Join(dir, "package.json")) {
		tests = append(tests, "npm / jest / vitest (`npm test`)")
	}
	if fileExists(filepath.Join(dir, "pytest.ini")) || fileExists(filepath.Join(dir, "tests")) {
		tests = append(tests, "pytest")
	}
	if len(tests) == 0 {
		return "none detected"
	}
	return strings.Join(tests, ", ")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
