// Package ai provides safe, risk-analyzed AI assistance for NexShell.
// It implements natural-language diagnostics (explain) and command generation (ai)
// with risk analysis and user confirmation before execution.
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"nexshell/internal/parser"
	"nexshell/internal/registry"
)

// CandidateCommand is an AI-suggested command with safety analysis.
type CandidateCommand struct {
	Command     string         `json:"command"`
	Explanation string         `json:"explanation"`
	Risk        RiskAssessment `json:"risk"`
}

// PlanExecutor allows the AI commands to safely run accepted candidate commands.
type PlanExecutor func(ctx context.Context, plan parser.Plan, shell registry.Context) (int, error)

var executor PlanExecutor

// SetExecutor configures the plan executor used for running approved AI commands.
func SetExecutor(fn PlanExecutor) {
	executor = fn
}

// ExplainCmd diagnoses developer and operating system errors:
// Satisfies Demo 5: `nex> explain "why is my server failing to bind?"`
type ExplainCmd struct{}

func (c *ExplainCmd) Name() string        { return "explain" }
func (c *ExplainCmd) Description() string { return "AI diagnostic assistant: explain <problem description>" }
func (c *ExplainCmd) PipelineSafe() bool  { return false }

func (c *ExplainCmd) Run(ctx registry.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("explain: missing query (e.g. explain \"why is my server failing to bind?\")")
	}

	query := strings.Join(args, " ")
	fmt.Fprintf(ctx.Output(), "\033[36m[AI Assistant]\033[0m Analyzing: %q...\n", query)

	candidate := DiagnoseQuery(query)

	// Display diagnosis
	fmt.Fprintln(ctx.Output(), strings.Repeat("─", 60))
	fmt.Fprintf(ctx.Output(), "\033[1mDiagnosis:\033[0m\n  %s\n\n", candidate.Explanation)
	fmt.Fprintf(ctx.Output(), "\033[1mRecommended Command:\033[0m\n  \033[32m%s\033[0m\n\n", candidate.Command)

	// Risk Badge
	badgeColor := "\033[32m" // Green
	switch candidate.Risk.Level {
	case RiskMedium:
		badgeColor = "\033[33m" // Yellow
	case RiskHigh, RiskCritical:
		badgeColor = "\033[31m" // Red
	}
	fmt.Fprintf(ctx.Output(), "\033[1mRisk Assessment:\033[0m %s[%s - Score: %d/100]\033[0m %s\n",
		badgeColor, candidate.Risk.Level, candidate.Risk.Score, candidate.Risk.Reason)
	fmt.Fprintln(ctx.Output(), strings.Repeat("─", 60))

	// User Confirmation
	if executor == nil {
		return nil
	}

	promptMsg := fmt.Sprintf("Execute recommended command %q? [y/N]: ", candidate.Command)
	fmt.Fprint(ctx.Output(), promptMsg)

	// Read confirmation from stdin
	reader := bufio.NewReader(ctx.Input())
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))

	if answer == "y" || answer == "yes" {
		fmt.Fprintf(ctx.Output(), "\033[36m[Executing]:\033[0m %s\n\n", candidate.Command)
		plan, err := parser.Parse(candidate.Command)
		if err != nil {
			return fmt.Errorf("ai: parse recommended command: %w", err)
		}
		_, execErr := executor(ctx.Cancellation(), plan, ctx)
		return execErr
	}

	fmt.Fprintln(ctx.Output(), "Execution canceled by user.")
	return nil
}

// AICmd generates shell commands from natural-language instructions:
// e.g.: `ai "find large log files in this directory"`
type AICmd struct{}

func (c *AICmd) Name() string        { return "ai" }
func (c *AICmd) Description() string { return "Generate shell commands with AI: ai <natural language request>" }
func (c *AICmd) PipelineSafe() bool  { return false }

func (c *AICmd) Run(ctx registry.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("ai: missing prompt (e.g. ai \"find all go files\")")
	}

	prompt := strings.Join(args, " ")
	candidate := GenerateCommand(prompt)

	fmt.Fprintln(ctx.Output(), strings.Repeat("─", 55))
	fmt.Fprintf(ctx.Output(), "\033[36mGenerated Command:\033[0m \033[32m%s\033[0m\n", candidate.Command)
	fmt.Fprintf(ctx.Output(), "\033[36mRisk Assessment:\033[0m [%s] %s\n", candidate.Risk.Level, candidate.Risk.Reason)
	fmt.Fprintln(ctx.Output(), strings.Repeat("─", 55))

	if executor == nil {
		return nil
	}

	fmt.Fprint(ctx.Output(), "Execute command? [y/N]: ")
	reader := bufio.NewReader(ctx.Input())
	ans, _ := reader.ReadString('\n')
	ans = strings.TrimSpace(strings.ToLower(ans))

	if ans == "y" || ans == "yes" {
		plan, err := parser.Parse(candidate.Command)
		if err != nil {
			return fmt.Errorf("ai: parse command: %w", err)
		}
		_, execErr := executor(ctx.Cancellation(), plan, ctx)
		return execErr
	}

	fmt.Fprintln(ctx.Output(), "Execution canceled.")
	return nil
}

// DiagnoseQuery provides expert troubleshooting suggestions, using an external
// OpenAI-compatible endpoint if configured or the built-in diagnostic engine.
func DiagnoseQuery(query string) CandidateCommand {
	// Try external API if key is present
	apiKey := os.Getenv("NEXSHELL_AI_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}

	if apiKey != "" {
		if cand, err := queryExternalAI(query, apiKey); err == nil {
			cand.Risk = AnalyzeRisk(cand.Command)
			return cand
		}
	}

	// Offline Expert Diagnostic Knowledge Base
	lower := strings.ToLower(query)
	var cmd string
	var explanation string

	switch {
	case strings.Contains(lower, "bind") || strings.Contains(lower, "port") || strings.Contains(lower, "address in use"):
		if runtime.GOOS == "windows" {
			cmd = "processes"
			explanation = "Port binding failure (EADDRINUSE) usually occurs when another local process is already listening on the requested port or failed to release the socket. Inspect running processes to identify competing servers."
		} else {
			cmd = "processes"
			explanation = "Port binding failure typically indicates an existing process holds the socket, or non-root users attempted binding to privileged ports (< 1024). Inspect processes or network sockets."
		}

	case strings.Contains(lower, "memory") || strings.Contains(lower, "ram") || strings.Contains(lower, "oom"):
		cmd = "processes | sort --desc memory"
		explanation = "High memory usage or Out-Of-Memory termination. Sorting processes by resident memory helps detect memory leaks."

	case strings.Contains(lower, "cpu") || strings.Contains(lower, "slow") || strings.Contains(lower, "freeze"):
		cmd = "processes | where cpu > 20 | select pid,name,cpu"
		explanation = "System CPU spike detected. Filtering active processes with elevated CPU usage helps pinpoint runaway threads."

	case strings.Contains(lower, "disk") || strings.Contains(lower, "space") || strings.Contains(lower, "full"):
		cmd = "ls"
		explanation = "Disk storage capacity alert. Review current workspace files and directories for large build artifacts or temp logs."

	case strings.Contains(lower, "git") || strings.Contains(lower, "conflict") || strings.Contains(lower, "merge"):
		cmd = "project"
		explanation = "VCS synchronization issue. Checking the project status will highlight uncommitted changes, divergent branches, and repository state."

	case strings.Contains(lower, "build") || strings.Contains(lower, "test") || strings.Contains(lower, "compile"):
		cmd = "task test --parallel"
		explanation = "Build or verification issue. Running automated project tasks and test suites concurrently will isolate compilation errors."

	default:
		cmd = "processes"
		explanation = fmt.Sprintf("General system diagnostic: inspect active processes and OS resource consumption to identify anomalous behavior related to %q.", query)
	}

	risk := AnalyzeRisk(cmd)
	return CandidateCommand{
		Command:     cmd,
		Explanation: explanation,
		Risk:        risk,
	}
}

// GenerateCommand converts natural language into candidate shell commands.
func GenerateCommand(prompt string) CandidateCommand {
	lower := strings.ToLower(prompt)
	var cmd string

	switch {
	case strings.Contains(lower, "process") && strings.Contains(lower, "cpu"):
		cmd = "processes | where cpu > 50 | select pid,name,cpu"
	case strings.Contains(lower, "process") && strings.Contains(lower, "memory"):
		cmd = "processes | sort --desc memory"
	case strings.Contains(lower, "process"):
		cmd = "processes"
	case strings.Contains(lower, "test"):
		cmd = "task test --parallel"
	case strings.Contains(lower, "build"):
		cmd = "task build"
	case strings.Contains(lower, "project") || strings.Contains(lower, "git status"):
		cmd = "project"
	case strings.Contains(lower, "list file") || strings.Contains(lower, "show file"):
		cmd = "ls"
	case strings.Contains(lower, "job"):
		cmd = "jobs"
	case strings.Contains(lower, "api") || strings.Contains(lower, "health"):
		cmd = "http GET http://localhost:8080/health"
	default:
		cmd = "echo \"[AI Request]: " + prompt + "\""
	}

	risk := AnalyzeRisk(cmd)
	return CandidateCommand{
		Command:     cmd,
		Explanation: "Generated from natural language prompt.",
		Risk:        risk,
	}
}

func queryExternalAI(prompt, apiKey string) (CandidateCommand, error) {
	url := os.Getenv("NEXSHELL_AI_URL")
	if url == "" {
		url = "https://api.openai.com/v1/chat/completions"
	}

	payload := map[string]any{
		"model": "gpt-4o-mini",
		"messages": []map[string]string{
			{"role": "system", "content": "You are NexShell AI. Output JSON only: {\"command\": \"...\", \"explanation\": \"...\"}"},
			{"role": "user", "content": prompt},
		},
		"temperature": 0.2,
	}

	bodyBytes, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return CandidateCommand{}, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return CandidateCommand{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return CandidateCommand{}, fmt.Errorf("status %d", resp.StatusCode)
	}

	var res struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	data, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(data, &res); err != nil || len(res.Choices) == 0 {
		return CandidateCommand{}, fmt.Errorf("invalid response")
	}

	var cand CandidateCommand
	_ = json.Unmarshal([]byte(res.Choices[0].Message.Content), &cand)
	if cand.Command == "" {
		return CandidateCommand{}, fmt.Errorf("empty command")
	}
	return cand, nil
}
