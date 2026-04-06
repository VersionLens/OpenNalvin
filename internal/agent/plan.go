package agent

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var nonAlphaNumPlanSlug = regexp.MustCompile(`[^a-z0-9]+`)
var planCodeSpanPattern = regexp.MustCompile("`([^`\\n]+)`")

type StoredPlanRef struct {
	Path        string `json:"path,omitempty"`
	SourceRunID string `json:"source_run_id,omitempty"`
	Status      string `json:"status,omitempty"`
}

func normalizeRunMode(mode string) string {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case "", RunModeDefault:
		return RunModeDefault
	case RunModePlan:
		return RunModePlan
	default:
		return RunModeDefault
	}
}

func NormalizeRunMode(mode string) string {
	return normalizeRunMode(mode)
}

func normalizePlanStatus(status string) string {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "", PlanStatusActive:
		return PlanStatusActive
	case PlanStatusReady:
		return PlanStatusReady
	case PlanStatusApproved:
		return PlanStatusApproved
	default:
		return PlanStatusActive
	}
}

func normalizePlanRef(ref StoredPlanRef) StoredPlanRef {
	ref.Path = filepath.ToSlash(strings.TrimSpace(ref.Path))
	ref.SourceRunID = strings.TrimSpace(ref.SourceRunID)
	ref.Status = normalizePlanStatus(ref.Status)
	if ref.Path == "" {
		ref.Status = ""
	}
	return ref
}

func NormalizePlanRef(ref StoredPlanRef) StoredPlanRef {
	return normalizePlanRef(ref)
}

func derivePlanPath(now time.Time, title, message string) string {
	slug := slugifyPlanName(chooseFirstNonEmpty(title, message, "plan"))
	timestamp := now.UTC().Format("20060102-150405")
	return filepath.ToSlash(filepath.Join(".plans", fmt.Sprintf("%s-%s.md", timestamp, slug)))
}

func DerivePlanPath(now time.Time, title, message string) string {
	return derivePlanPath(now, title, message)
}

func slugifyPlanName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = nonAlphaNumPlanSlug.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "plan"
	}
	if len(value) > 48 {
		value = strings.Trim(value[:48], "-")
	}
	if value == "" {
		return "plan"
	}
	return value
}

func initialPlanFileContent(title, message string, now time.Time) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = deriveRunTitle("", message)
	}
	message = strings.TrimSpace(message)
	stamp := now.UTC().Format(time.RFC3339)

	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString("Status: planning\n")
	b.WriteString("Updated: ")
	b.WriteString(stamp)
	b.WriteString("\n\n")
	b.WriteString("## Summary\n\n")
	b.WriteString("- TODO\n\n")
	b.WriteString("## Requested Work\n\n")
	b.WriteString(message)
	b.WriteString("\n\n")
	b.WriteString("## Implementation Notes\n\n")
	b.WriteString("- TODO\n\n")
	b.WriteString("## Verification\n\n")
	b.WriteString("- TODO\n")
	return b.String()
}

func InitialPlanFileContent(title, message string, now time.Time) string {
	return initialPlanFileContent(title, message, now)
}

func planModePrompt(planPath string, child bool) string {
	planPath = strings.TrimSpace(planPath)
	if planPath == "" {
		planPath = ".plans/plan.md"
	}
	finishInstruction := "When the plan is ready for approval and handoff, call plan_exit."
	if child {
		finishInstruction = "Do not finalize the plan or call plan_exit from a child run; send your findings back to the parent run instead."
	}
	return fmt.Sprintf(`
PLAN MODE
Plan mode is active for this run.

The canonical plan file for this run is:
- %s

Use that file as the source of truth for the evolving plan. Update it as your
understanding improves.

TOOLS IN PLAN MODE
You may use tools to improve the plan: inspect code, search files, run tests,
validate assumptions, gather evidence, and check behavior when that helps you
produce a better plan.

Do NOT use tools to carry out or implement the requested repository changes
yet. If a tool action would cross the line from improving the plan into doing
the work, stop short and capture that step in the plan instead.

When you need to update the plan:
- first read the canonical file with view if needed
- then update that exact file with write, edit, or multiedit
- do not draft plan content in /tmp, temp files, or shell redirections before
  copying it back
- avoid using shell for plan-file edits when direct file tools will do

If you write anything, prefer updating only the canonical plan file unless the
user explicitly redirects you. Once the canonical plan file is updated and the
plan is ready, call plan_exit immediately.

%s
`, planPath, finishInstruction)
}

func inferImplementationTargetRoot(planBody string) string {
	planBody = strings.TrimSpace(planBody)
	if planBody == "" {
		return ""
	}

	rootCounts := map[string]int{}
	matches := planCodeSpanPattern.FindAllStringSubmatch(planBody, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		token := filepath.ToSlash(strings.TrimSpace(match[1]))
		if token == "" || strings.HasPrefix(token, ".plans/") || !strings.Contains(token, "/") {
			continue
		}
		parts := strings.Split(token, "/")
		if len(parts) < 2 {
			continue
		}
		root := strings.TrimSpace(parts[0])
		if root == "" || root == "." || root == ".." {
			continue
		}
		rootCounts[root]++
	}

	bestRoot := ""
	bestCount := 0
	for root, count := range rootCounts {
		if count > bestCount {
			bestRoot = root
			bestCount = count
		}
	}
	if bestCount < 2 {
		return ""
	}
	return bestRoot
}

func InferImplementationTargetRoot(planBody string) string {
	return inferImplementationTargetRoot(planBody)
}

func inferImplementationTargetRootFromContext(workspaceDirs []string, texts ...string) string {
	if len(workspaceDirs) == 0 {
		return ""
	}

	type scoredRoot struct {
		name  string
		score int
	}

	seen := make(map[string]struct{}, len(workspaceDirs))
	best := scoredRoot{}
	pathRoot := inferImplementationTargetRoot(strings.Join(texts, "\n"))

	for _, dir := range workspaceDirs {
		name := strings.TrimSpace(filepath.ToSlash(dir))
		if name == "" || name == "." || name == ".." || strings.Contains(name, "/") || strings.HasPrefix(name, ".") {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}

		score := 0
		for i, text := range texts {
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			weight := 1
			if i < 2 {
				weight = 3
			}
			score += weight * 12 * strings.Count(text, "`"+name+"/")
			score += weight * 10 * strings.Count(text, "`"+name+"`")
			score += weight * 8 * strings.Count(text, name+"/")
			score += weight * 6 * strings.Count(text, name)
		}
		if name == pathRoot {
			score += 24
		}
		if score > best.score || (score == best.score && score > 0 && len(name) > len(best.name)) {
			best = scoredRoot{name: name, score: score}
		}
	}
	if best.score == 0 {
		return ""
	}
	return best.name
}

func InferImplementationTargetRootFromContext(workspaceDirs []string, texts ...string) string {
	return inferImplementationTargetRootFromContext(workspaceDirs, texts...)
}

func ImplementPlanMessage(planPath, planBody, targetRoot string) string {
	planPath = strings.TrimSpace(planPath)
	planBody = strings.TrimSpace(planBody)
	targetRoot = strings.TrimSpace(filepath.ToSlash(targetRoot))

	var b strings.Builder
	b.WriteString("Implement the approved plan")
	if planPath != "" {
		b.WriteString(" at ")
		b.WriteString(planPath)
	}
	b.WriteString(".")
	if targetRoot != "" {
		b.WriteString("\n\nTarget workspace-relative root: ")
		b.WriteString(targetRoot)
		b.WriteString("\nCreate and update files under that directory unless the plan explicitly says otherwise.")
		b.WriteString("\nDo not place those files at the workspace root by default.")
	}
	b.WriteString("\n\nPrefer direct file tools (`write`, `edit`, `multiedit`) for creating and updating plan files.")
	b.WriteString("\nAvoid batching file creation in `shell` unless you truly need it: shell writes are rolled back if the script exits non-zero.")
	b.WriteString("\nIf you do use `shell`, separate file writes from follow-up verification or permission commands.")
	if planBody == "" {
		return b.String()
	}
	b.WriteString("\n\nFollow any workspace-relative paths in the plan exactly as written.")
	b.WriteString("\n\nApproved plan:\n\n")
	b.WriteString(planBody)
	b.WriteString("\n")
	return b.String()
}
