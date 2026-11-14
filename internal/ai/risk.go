package ai

import (
	"regexp"
	"strings"
)

// RiskLevel defines the danger level of a shell command.
type RiskLevel string

const (
	RiskSafe     RiskLevel = "SAFE"
	RiskLow      RiskLevel = "LOW"
	RiskMedium   RiskLevel = "MEDIUM"
	RiskHigh     RiskLevel = "HIGH"
	RiskCritical RiskLevel = "CRITICAL"
)

// RiskAssessment holds safety evaluation results for a command.
type RiskAssessment struct {
	Level       RiskLevel `json:"level"`
	Score       int       `json:"score"` // 0 to 100
	Reason      string    `json:"reason"`
	RequiresAck bool      `json:"requires_ack"`
}

// Critical patterns that could destroy the operating system or all files
var criticalPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+(-[a-z]*r[a-z]*f|-[a-z]*f[a-z]*r)\s+[/~*]`),
	regexp.MustCompile(`(?i)\bformat\s+[a-z]:`),
	regexp.MustCompile(`(?i)\bdel\s+/[s|q|f]*\s+([c-z]:\\|/|\*)`),
	regexp.MustCompile(`(?i)\bmkfs\b`),
	regexp.MustCompile(`(?i)\bdd\s+if=.*of=/dev/`),
	regexp.MustCompile(`(?i)\bchmod\s+(-R\s+)?777\s+/`),
	regexp.MustCompile(`(?i)\bkill\s+-9\s+1\b`),
}

// High risk patterns that modify system state or delete specific directories/files
var highRiskPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+-[a-z]*r`),
	regexp.MustCompile(`(?i)\bdel\s+/[s|q]`),
	regexp.MustCompile(`(?i)\bkill\s+-9\b`),
	regexp.MustCompile(`(?i)\bgit\s+reset\s+--hard\b`),
	regexp.MustCompile(`(?i)\bgit\s+clean\s+-[a-z]*f`),
	regexp.MustCompile(`(?i)\bdrop\s+database\b`),
	regexp.MustCompile(`(?i)\bshutdown\b`),
	regexp.MustCompile(`(?i)\breboot\b`),
}

// Medium risk patterns that mutate files, terminate processes, or alter configuration
var mediumRiskPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bkill\b`),
	regexp.MustCompile(`(?i)\brm\b`),
	regexp.MustCompile(`(?i)\bdel\b`),
	regexp.MustCompile(`(?i)\bmv\b`),
	regexp.MustCompile(`(?i)\bcp\s+-r\b`),
	regexp.MustCompile(`(?i)\bgit\s+push\b`),
	regexp.MustCompile(`(?i)\bchmod\b`),
	regexp.MustCompile(`(?i)\bchown\b`),
	regexp.MustCompile(`(?i)\btask\s+clean\b`),
}

// AnalyzeRisk evaluates the command safety score and risk category.
func AnalyzeRisk(command string) RiskAssessment {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return RiskAssessment{Level: RiskSafe, Score: 0, Reason: "Empty command", RequiresAck: false}
	}

	// 1. Check Critical
	for _, p := range criticalPatterns {
		if p.MatchString(trimmed) {
			return RiskAssessment{
				Level:       RiskCritical,
				Score:       95,
				Reason:      "Potentially catastrophic system or disk modification",
				RequiresAck: true,
			}
		}
	}

	// 2. Check High Risk
	for _, p := range highRiskPatterns {
		if p.MatchString(trimmed) {
			return RiskAssessment{
				Level:       RiskHigh,
				Score:       75,
				Reason:      "Destructive command (hard reset, recursive delete, or forced termination)",
				RequiresAck: true,
			}
		}
	}

	// 3. Check Medium Risk
	for _, p := range mediumRiskPatterns {
		if p.MatchString(trimmed) {
			return RiskAssessment{
				Level:       RiskMedium,
				Score:       45,
				Reason:      "Command modifies files, kills processes, or changes permissions",
				RequiresAck: false,
			}
		}
	}

	// 4. Read-only / Low Risk
	return RiskAssessment{
		Level:       RiskLow,
		Score:       10,
		Reason:      "Standard inspection or non-destructive execution",
		RequiresAck: false,
	}
}
