package agentrun

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

func (s *Service) PrepareQueuedImplementationTurn(ctx context.Context, sourceRunID string, req Request) (*PreparedTurn, error) {
	return s.prepareImplementationTurn(ctx, sourceRunID, req, "queued")
}

func (s *Service) PrepareAttachedImplementationTurn(ctx context.Context, sourceRunID string, req Request) (*PreparedTurn, error) {
	return s.prepareImplementationTurn(ctx, sourceRunID, req, "attached")
}

func (s *Service) prepareImplementationTurn(ctx context.Context, sourceRunID string, req Request, mode string) (*PreparedTurn, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("agent run service is required")
	}
	sourceRunID = strings.TrimSpace(sourceRunID)
	if sourceRunID == "" {
		return nil, fmt.Errorf("source run id is required")
	}

	sourceRun, err := s.store.GetAgentRun(ctx, sourceRunID)
	if err != nil {
		return nil, err
	}
	sourceTrace, err := agentpkg.ParseStoredTrace(sourceRun.Trace)
	if err != nil {
		return nil, err
	}
	if agentpkg.NormalizeRunMode(sourceTrace.Metadata.Mode) != agentpkg.RunModePlan {
		return nil, fmt.Errorf("run %q is not a plan run", sourceRunID)
	}

	planRef := agentpkg.NormalizePlanRef(sourceTrace.Metadata.PlanRef)
	if planRef.Path == "" {
		return nil, fmt.Errorf("plan run %q has no canonical plan file", sourceRunID)
	}
	planBody, err := readWorkspaceFile(ctx, planRef.Path)
	if err != nil {
		return nil, fmt.Errorf("read approved plan: %w", err)
	}
	targetRoot, err := inferImplementationTargetRoot(ctx, sourceRun, planBody)
	if err != nil {
		return nil, fmt.Errorf("infer implementation target root: %w", err)
	}

	req.RunID = ""
	req.Message = agentpkg.ImplementPlanMessage(planRef.Path, planBody, targetRoot)
	req.ParentRunID = ""
	req.RootRunID = ""
	req.TaskName = ""
	req.RunKind = agentpkg.RunKindRoot
	req.Mode = agentpkg.RunModeDefault
	req.PlanRef = agentpkg.StoredPlanRef{
		Path:        planRef.Path,
		SourceRunID: sourceRunID,
		Status:      agentpkg.PlanStatusApproved,
	}

	if strings.TrimSpace(req.Title) == "" {
		req.Title = strings.TrimSpace(sourceRun.Title)
	}
	if strings.TrimSpace(req.ProviderName) == "" {
		req.ProviderName = strings.TrimSpace(sourceRun.Provider)
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = strings.TrimSpace(sourceRun.Model)
	}
	if strings.TrimSpace(req.SystemPrompt) == "" {
		req.SystemPrompt = strings.TrimSpace(sourceTrace.SystemPrompt)
	}

	return s.prepareTurn(ctx, req, mode)
}

func preparePlanArtifact(ctx context.Context, runID, title, message string) (agentpkg.StoredPlanRef, error) {
	now := time.Now().UTC()
	planPath := agentpkg.DerivePlanPath(now, title, message)
	cfg, _ := configpkg.FromContext(ctx)
	_, relativePath, absolutePath, err := workspacepkg.ResolveFilePath(ctx, cfg, planPath, false)
	if err != nil {
		return agentpkg.StoredPlanRef{}, err
	}
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		return agentpkg.StoredPlanRef{}, fmt.Errorf("create .plans directory: %w", err)
	}
	if err := os.WriteFile(absolutePath, []byte(agentpkg.InitialPlanFileContent(title, message, now)), 0o644); err != nil {
		return agentpkg.StoredPlanRef{}, fmt.Errorf("write initial plan file: %w", err)
	}
	return agentpkg.StoredPlanRef{
		Path:        relativePath,
		SourceRunID: strings.TrimSpace(runID),
		Status:      agentpkg.PlanStatusActive,
	}, nil
}

func readWorkspaceFile(ctx context.Context, relativePath string) (string, error) {
	cfg, _ := configpkg.FromContext(ctx)
	_, _, absolutePath, err := workspacepkg.ResolveFilePath(ctx, cfg, relativePath, false)
	if err != nil {
		return "", err
	}
	body, err := os.ReadFile(absolutePath)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func planRefProvided(ref agentpkg.StoredPlanRef) bool {
	return strings.TrimSpace(ref.Path) != "" || strings.TrimSpace(ref.SourceRunID) != "" || strings.TrimSpace(ref.Status) != ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func inferImplementationTargetRoot(ctx context.Context, sourceRun *knowledge.AgentRun, planBody string) (string, error) {
	if sourceRun == nil {
		return "", nil
	}
	cfg, _ := configpkg.FromContext(ctx)
	paths, err := workspacepkg.ActivePaths(ctx, cfg)
	if err != nil {
		return "", err
	}

	workspaceDirs, err := listWorkspaceDirectories(paths.FilesPath)
	if err != nil {
		return "", err
	}

	return agentpkg.InferImplementationTargetRootFromContext(
		workspaceDirs,
		sourceRun.Title,
		sourceRun.Prompt,
		planBody,
	), nil
}

func listWorkspaceDirectories(root string) ([]string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" {
			continue
		}
		dirs = append(dirs, name)
	}
	sort.Strings(dirs)
	return dirs, nil
}
