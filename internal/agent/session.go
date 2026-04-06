package agent

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

type runSessionRegistry struct {
	mu       sync.Mutex
	sessions map[string]*runSessionState
}

type runSessionState struct {
	runID       string
	parentRunID string
	rootRunID   string
	taskName    string
	runKind     string
	mode        string
	planRef     StoredPlanRef
	provider    string

	mu                   sync.Mutex
	cfg                  configpkg.Config
	store                *knowledge.Store
	debug                io.Writer
	pendingNotifications []string
	subagents            *subAgentManager
}

var globalRunSessions = &runSessionRegistry{
	sessions: map[string]*runSessionState{},
}

func (r *runSessionRegistry) getOrCreate(runID string) *runSessionState {
	r.mu.Lock()
	defer r.mu.Unlock()

	if session, ok := r.sessions[runID]; ok {
		return session
	}

	session := &runSessionState{runID: runID}
	r.sessions[runID] = session
	return session
}

func newEphemeralSessionState(ctx context.Context, store *knowledge.Store, runID string) *runSessionState {
	session := &runSessionState{runID: strings.TrimSpace(runID)}
	session.applyContext(ctx, store)
	return session
}

func (s *runSessionState) applyContext(ctx context.Context, store *knowledge.Store) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cfg, ok := configpkg.FromContext(ctx); ok {
		s.cfg = cfg
	}
	if store != nil {
		s.store = store
	}
	if s.subagents != nil {
		s.subagents.store = s.store
		s.subagents.cfg = s.cfg
	}
}

func (s *runSessionState) setDebugWriter(w io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.debug = w
}

func (s *runSessionState) setMetadata(parentRunID, rootRunID, taskName, runKind string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(parentRunID) != "" {
		s.parentRunID = strings.TrimSpace(parentRunID)
	}
	if strings.TrimSpace(rootRunID) != "" {
		s.rootRunID = strings.TrimSpace(rootRunID)
	}
	if strings.TrimSpace(taskName) != "" {
		s.taskName = strings.TrimSpace(taskName)
	}
	if strings.TrimSpace(runKind) != "" {
		s.runKind = strings.TrimSpace(runKind)
	}
	if s.rootRunID == "" {
		s.rootRunID = s.runID
	}
	if s.runKind == "" {
		s.runKind = RunKindRoot
	}
	if s.mode == "" {
		s.mode = RunModeDefault
	}
}

func (s *runSessionState) setMode(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = normalizeRunMode(mode)
}

func (s *runSessionState) setPlanRef(ref StoredPlanRef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.planRef = normalizePlanRef(ref)
}

func (s *runSessionState) setProviderName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.provider = normalizeProviderName(name)
}

func (s *runSessionState) providerName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return normalizeProviderName(s.provider)
}

func (s *runSessionState) metadata() StoredRunMeta {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta := StoredRunMeta{
		ParentRunID: s.parentRunID,
		RootRunID:   s.rootRunID,
		TaskName:    s.taskName,
		RunKind:     s.runKind,
		Mode:        normalizeRunMode(s.mode),
		PlanRef:     normalizePlanRef(s.planRef),
	}
	if meta.RootRunID == "" {
		meta.RootRunID = s.runID
	}
	if meta.RunKind == "" {
		meta.RunKind = RunKindRoot
	}
	if meta.Mode == "" {
		meta.Mode = RunModeDefault
	}
	return meta
}

func (s *runSessionState) enqueueNotification(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingNotifications = append(s.pendingNotifications, message)
}

func (s *runSessionState) consumeNotifications() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.pendingNotifications) == 0 {
		return nil
	}

	out := append([]string(nil), s.pendingNotifications...)
	s.pendingNotifications = nil
	return out
}

func (s *runSessionState) ensureSubAgentManager() (*subAgentManager, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.subagents != nil {
		return s.subagents, nil
	}
	if s.store == nil {
		return nil, fmt.Errorf("sub-agent manager requires a workspace-backed knowledge store")
	}

	s.subagents = newSubAgentManager(s, s.store, s.cfg)
	return s.subagents, nil
}

func (s *runSessionState) currentSubAgentManager() *subAgentManager {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subagents
}

func (s *runSessionState) debugf(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.debug == nil {
		return
	}

	_, _ = fmt.Fprintf(s.debug, "[debug] "+format+"\n", args...)
}

func (s *runSessionState) debugWriter() io.Writer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.debug
}
