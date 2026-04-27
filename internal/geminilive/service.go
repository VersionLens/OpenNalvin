package geminilive

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/config"
	"google.golang.org/genai"
)

const (
	DefaultModel                    = "gemini-live-2.5-flash-native-audio"
	defaultVertexLiveLocation       = "us-central1"
	legacyPreviewModel              = "gemini-live-2.5-flash-preview-native-audio"
	versionedDeprecatedPreviewModel = "gemini-live-2.5-flash-preview-native-audio-09-2025"
	vertexAPIVersion                = "v1beta1"
)

type TranscriptEvent struct {
	Role  string
	Text  string
	Final bool
}

type StartRequest struct {
	ProviderName      string
	SystemPrompt      string
	VoiceName         string
	SessionResumption bool
	OnStatus          func(string)
	OnTranscript      func(TranscriptEvent)
	OnAudio           func([]byte)
	OnInterrupted     func()
	OnSessionHandle   func(string)
}

type Conversation interface {
	SendPCM16([]byte) error
	EndAudio() error
	Close() error
}

type Service struct {
	cfg    config.Config
	logger *slog.Logger
	dialer dialer
}

type dialer interface {
	Dial(context.Context, dialRequest) (liveSession, error)
}

type dialRequest struct {
	Provider Provider
	Model    string
	Config   *genai.LiveConnectConfig
}

type liveSession interface {
	Receive() (*genai.LiveServerMessage, error)
	SendRealtimeInput(genai.LiveRealtimeInput) error
	Close() error
}

type Provider struct {
	Name              string
	Type              string
	Model             string
	Project           string
	Location          string
	UserAgentOverride string
}

type serviceConversation struct {
	logger logger

	session         liveSession
	onStatus        func(string)
	onTranscript    func(TranscriptEvent)
	onAudio         func([]byte)
	onInterrupted   func()
	onSessionHandle func(string)

	closeOnce sync.Once
	done      chan struct{}
}

type logger interface {
	Debug(string, ...any)
	Warn(string, ...any)
}

func New(cfg config.Config, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		cfg:    cfg,
		logger: logger,
		dialer: liveDialer{logger: logger},
	}
}

func (s *Service) Start(ctx context.Context, req StartRequest) (Conversation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	provider, err := s.loadProvider(req.ProviderName)
	if err != nil {
		return nil, err
	}

	session, err := s.dialer.Dial(ctx, dialRequest{
		Provider: provider,
		Model:    provider.Model,
		Config:   buildLiveConfig(req),
	})
	if err != nil {
		return nil, err
	}

	conv := &serviceConversation{
		logger:          s.logger,
		session:         session,
		onStatus:        req.OnStatus,
		onTranscript:    req.OnTranscript,
		onAudio:         req.OnAudio,
		onInterrupted:   req.OnInterrupted,
		onSessionHandle: req.OnSessionHandle,
		done:            make(chan struct{}),
	}
	go conv.recvLoop()
	return conv, nil
}

func buildLiveConfig(req StartRequest) *genai.LiveConnectConfig {
	prefixPaddingMS := int32(80)
	silenceDurationMS := int32(350)
	cfg := &genai.LiveConnectConfig{
		ResponseModalities:       []genai.Modality{genai.ModalityAudio},
		InputAudioTranscription:  &genai.AudioTranscriptionConfig{},
		OutputAudioTranscription: &genai.AudioTranscriptionConfig{},
		RealtimeInputConfig: &genai.RealtimeInputConfig{
			ActivityHandling: genai.ActivityHandlingStartOfActivityInterrupts,
			TurnCoverage:     genai.TurnCoverageTurnIncludesOnlyActivity,
			AutomaticActivityDetection: &genai.AutomaticActivityDetection{
				StartOfSpeechSensitivity: genai.StartSensitivityHigh,
				EndOfSpeechSensitivity:   genai.EndSensitivityHigh,
				PrefixPaddingMs:          &prefixPaddingMS,
				SilenceDurationMs:        &silenceDurationMS,
			},
		},
		SessionResumption: &genai.SessionResumptionConfig{
			Transparent: true,
		},
	}
	if !req.SessionResumption {
		cfg.SessionResumption = nil
	}
	systemPrompt := strings.TrimSpace(req.SystemPrompt)
	if systemPrompt != "" {
		cfg.SystemInstruction = genai.NewContentFromText(systemPrompt, genai.RoleUser)
	}
	voiceName := strings.TrimSpace(req.VoiceName)
	if voiceName != "" {
		cfg.SpeechConfig = &genai.SpeechConfig{
			VoiceConfig: &genai.VoiceConfig{
				PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{
					VoiceName: voiceName,
				},
			},
		}
	}
	return cfg
}

func (s *Service) loadProvider(name string) (Provider, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "default"
	}
	cfg, err := agentpkg.LoadProviderConfig(name)
	if err != nil {
		return Provider{}, err
	}
	if cfg.Type != agentpkg.ProviderTypeVertex {
		return Provider{}, fmt.Errorf("provider %q must use type %q for Gemini Live", name, agentpkg.ProviderTypeVertex)
	}
	provider := Provider{
		Name:              name,
		Type:              cfg.Type,
		Model:             cfg.Model,
		Project:           cfg.Project,
		Location:          cfg.Location,
		UserAgentOverride: cfg.UserAgentOverride,
	}
	provider.Model = strings.TrimSpace(provider.Model)
	if provider.Model == "" {
		provider.Model = DefaultModel
	}
	switch provider.Model {
	case legacyPreviewModel, versionedDeprecatedPreviewModel:
		s.logger.Warn("gemini live provider configured with deprecated preview model; using stable native-audio model instead",
			"provider_name", provider.Name,
			"configured_model", provider.Model,
			"effective_model", DefaultModel,
		)
		provider.Model = DefaultModel
	}
	if !isLikelyLiveModel(provider.Model) {
		return Provider{}, fmt.Errorf("provider %q model %q is not a Gemini Live model", name, provider.Model)
	}
	provider.Location = strings.TrimSpace(provider.Location)
	if provider.Location == "" || strings.EqualFold(provider.Location, "global") {
		s.logger.Warn("gemini live provider configured without a supported regional Vertex location; using fallback region",
			"provider_name", provider.Name,
			"configured_location", provider.Location,
			"effective_location", defaultVertexLiveLocation,
		)
		provider.Location = defaultVertexLiveLocation
	}
	return provider, nil
}

func isLikelyLiveModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "gemini-live-") || strings.Contains(model, "-live-")
}

func (c *serviceConversation) SendPCM16(pcm []byte) error {
	if len(pcm) == 0 {
		return nil
	}
	return c.session.SendRealtimeInput(genai.LiveRealtimeInput{
		Audio: &genai.Blob{
			Data:     append([]byte(nil), pcm...),
			MIMEType: "audio/pcm;rate=16000",
		},
	})
}

func (c *serviceConversation) EndAudio() error {
	return c.session.SendRealtimeInput(genai.LiveRealtimeInput{AudioStreamEnd: true})
}

func (c *serviceConversation) Close() error {
	var err error
	c.closeOnce.Do(func() {
		err = c.session.Close()
	})
	<-c.done
	return err
}

func (c *serviceConversation) recvLoop() {
	defer close(c.done)
	for {
		msg, err := c.session.Receive()
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				c.setStatus("failed")
				c.logger.Warn("gemini live receive failed", "error", err)
			}
			return
		}
		c.handleMessage(msg)
	}
}

func (c *serviceConversation) handleMessage(msg *genai.LiveServerMessage) {
	if msg == nil {
		return
	}
	if msg.SetupComplete != nil {
		c.setStatus("ready")
	}
	if msg.GoAway != nil {
		c.setStatus("go_away")
	}
	if msg.SessionResumptionUpdate != nil && msg.SessionResumptionUpdate.Resumable && strings.TrimSpace(msg.SessionResumptionUpdate.NewHandle) != "" {
		if c.onSessionHandle != nil {
			c.onSessionHandle(strings.TrimSpace(msg.SessionResumptionUpdate.NewHandle))
		}
	}
	if msg.ServerContent == nil {
		return
	}
	content := msg.ServerContent
	if content.Interrupted {
		c.setStatus("interrupted")
		if c.onInterrupted != nil {
			c.onInterrupted()
		}
	}
	if content.InputTranscription != nil {
		c.emitTranscript("user", content.InputTranscription.Text, content.InputTranscription.Finished)
	}
	if content.OutputTranscription != nil {
		c.emitTranscript("assistant", content.OutputTranscription.Text, content.OutputTranscription.Finished)
	}
	if content.WaitingForInput {
		c.setStatus("waiting_for_input")
	}
	if content.GenerationComplete {
		c.setStatus("generation_complete")
	}
	if content.TurnComplete {
		c.setStatus("turn_complete")
	}
	if content.ModelTurn == nil {
		return
	}
	for _, part := range content.ModelTurn.Parts {
		if part == nil || part.InlineData == nil || len(part.InlineData.Data) == 0 {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(part.InlineData.MIMEType)), "audio/pcm") {
			continue
		}
		if c.onAudio != nil {
			c.onAudio(append([]byte(nil), part.InlineData.Data...))
		}
	}
}

func (c *serviceConversation) emitTranscript(role, text string, final bool) {
	if c.onTranscript == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	c.onTranscript(TranscriptEvent{
		Role:  role,
		Text:  text,
		Final: final,
	})
}

func (c *serviceConversation) setStatus(status string) {
	if c.onStatus != nil {
		c.onStatus(status)
	}
}

type liveDialer struct {
	logger *slog.Logger
}

func (d liveDialer) Dial(ctx context.Context, req dialRequest) (liveSession, error) {
	headers := http.Header{}
	if ua := strings.TrimSpace(req.Provider.UserAgentOverride); ua != "" {
		headers.Set("User-Agent", ua)
	}
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  req.Provider.Project,
		Location: req.Provider.Location,
		HTTPOptions: genai.HTTPOptions{
			APIVersion: vertexAPIVersion,
			Headers:    headers,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create genai client: %w", err)
	}
	session, err := client.Live.Connect(ctx, req.Model, req.Config)
	if err != nil {
		return nil, fmt.Errorf("connect live session: %w", err)
	}
	return session, nil
}

var _ Conversation = (*serviceConversation)(nil)
