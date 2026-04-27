package discordbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	disgovoice "github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
	"github.com/hraban/opus"

	"github.com/versionlens/OpenNalvin/internal/geminilive"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/versionlens/OpenNalvin/internal/liveaudio"
)

const (
	liveVoiceOpenTimeout          = 20 * time.Second
	liveVoiceReadyTimeout         = 5 * time.Second
	playbackRetryInterval         = 40 * time.Millisecond
	playbackNotReadyGrace         = 750 * time.Millisecond
	maxBufferedPlaybackFrameCount = 50
	discordOpusDecodeWarningEvery = 50
	localSpeechPeakThreshold      = 800
	localSpeechRMSThreshold       = 250
	localSpeechStartFrames        = 2
	localSpeechStartWindow        = 100 * time.Millisecond
	localSpeechEndSilence         = 250 * time.Millisecond
	localBargeInPlaybackWindow    = 500 * time.Millisecond
	geminiAudioSuppressWindow     = 750 * time.Millisecond
	endpointSilenceGap            = 40 * time.Millisecond
	endpointMaxSilencePaddingMS   = 350
	endpointMaxSilencePadFrames   = endpointMaxSilencePaddingMS / liveaudio.FrameDurationMS
)

var (
	errVoiceTransportNotReady = errors.New("voice transport not ready")
	errVoiceTransportClosed   = errors.New("voice transport closed")
)

type VoiceTransportState string

const (
	VoiceTransportOpening          VoiceTransportState = "opening"
	VoiceTransportSessionDescribed VoiceTransportState = "session_described"
	VoiceTransportAudioReady       VoiceTransportState = "audio_ready"
	VoiceTransportReconnecting     VoiceTransportState = "reconnecting"
	VoiceTransportClosing          VoiceTransportState = "closing"
	VoiceTransportClosed           VoiceTransportState = "closed"
)

type opusPacket struct {
	SSRC       uint32
	Sequence   uint16
	Timestamp  uint32
	UserID     string
	ReceivedAt time.Time
	Opus       []byte
}

type voiceConnection interface {
	Disconnect() error
	WaitReady(context.Context) error
	TransportState() VoiceTransportState
	StartSpeaking() error
	StopSpeaking() error
	SendOpus([]byte) error
	RecvOpus() <-chan opusPacket
}

type liveVoiceSession struct {
	svc            *Service
	key            string
	guildID        string
	voiceChannelID string
	voice          voiceConnection
	call           *knowledge.DiscordLiveCall
	conversation   geminilive.Conversation
	playback       *voicePlayback
	cancel         context.CancelFunc

	inboundWav  *wavRecorder
	outboundWav *wavRecorder
	trace       *liveTraceRecorder
	captureDone chan struct{}

	bargeMu             sync.Mutex
	localUserSpeaking   bool
	suppressGeminiUntil time.Time

	mu    sync.Mutex
	state liveCallState
}

type liveCallState struct {
	Entries          []liveTranscriptEntry `json:"entries"`
	PartialUser      string                `json:"partial_user,omitempty"`
	PartialAssistant string                `json:"partial_assistant,omitempty"`
	Status           string                `json:"status,omitempty"`
}

type liveTranscriptEntry struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type voicePlayback struct {
	logger  *slog.Logger
	voice   voiceConnection
	encoder *opus.Encoder
	trace   *liveTraceRecorder

	stateMu        sync.Mutex
	pendingSamples int
	speakingState  bool
	lastSentAt     time.Time

	events chan playbackEvent
	done   chan struct{}
}

type playbackEvent struct {
	audio       []byte
	reset       bool
	resetReason string
	close       bool
}

type localSpeechCoordinator struct {
	active          bool
	candidateStart  time.Time
	candidateFrames int
	speechStart     time.Time
	lastSpeechFrame time.Time
}

type localSpeechObservation struct {
	Started    bool
	Ended      bool
	StartAt    time.Time
	EndAt      time.Time
	DurationMS int
}

type liveEndpointAction int

const (
	liveEndpointActionNone liveEndpointAction = iota
	liveEndpointActionPad
	liveEndpointActionEnd
)

type liveInputEndpointState struct {
	open          bool
	ended         bool
	lastRealFrame time.Time
	padFrames     int
}

type daveVoiceConnection struct {
	logger                *slog.Logger
	manager               voiceManager
	guildID               snowflake.ID
	channelID             snowflake.ID
	conn                  disgovoice.Conn
	recv                  chan opusPacket
	done                  chan struct{}
	closeOnce             sync.Once
	speakingMu            sync.Mutex
	speaking              bool
	stateMu               sync.Mutex
	stateChanged          chan struct{}
	state                 VoiceTransportState
	sessionReady          bool
	everAudioReady        bool
	statsMu               sync.Mutex
	inboundFrames         int
	outboundFrames        int
	droppedFrames         int
	droppedOutboundFrames int
	decryptErrors         int
	readErrors            int
	traceMu               sync.Mutex
	trace                 *liveTraceRecorder
}

type discordOpusDecoderState struct {
	decoder   *opus.Decoder
	pcmBuffer []int16
}

func (s *Service) handleGuildVoiceStateUpdate(event *events.GuildVoiceStateUpdate) {
	if event == nil {
		return
	}
	if !s.cfg.Discord.Live.Enabled {
		return
	}

	voiceState := event.VoiceState
	guildID := voiceState.GuildID.String()
	if !s.guildAllowed(guildID) {
		return
	}
	userID := voiceState.UserID.String()
	if s.isBotUser(userID) {
		return
	}
	displayName := strings.TrimSpace(event.Member.EffectiveName())
	if displayName == "" {
		displayName = userID
	}

	channelID := ""
	if voiceState.ChannelID != nil {
		channelID = voiceState.ChannelID.String()
	}
	prevChannelID, prevCount, nextCount := s.recordVoiceOccupant(guildID, userID, channelID)
	s.recordLiveParticipantMovement(guildID, userID, displayName, prevChannelID, channelID)
	if prevChannelID != "" && prevChannelID != channelID && s.liveChannelEnabled(prevChannelID) && prevCount == 0 {
		go s.stopLiveSession(guildID, prevChannelID, "voice channel empty")
	}
	if channelID != "" && s.liveChannelEnabled(channelID) && nextCount > 0 {
		go s.ensureLiveSession(guildID, channelID)
	}
	if channelID == "" && prevChannelID != "" && s.liveChannelEnabled(prevChannelID) && prevCount == 0 {
		go s.stopLiveSession(guildID, prevChannelID, "voice channel empty")
	}
}

func (s *Service) handleVoiceServerUpdate(event *events.VoiceServerUpdate) {
	_ = event
}

func (s *Service) recordVoiceOccupant(guildID, userID, channelID string) (string, int, int) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()

	if s.live.occupants == nil {
		s.live.occupants = map[string]map[string]string{}
	}
	guildOccupants := s.live.occupants[guildID]
	if guildOccupants == nil {
		guildOccupants = map[string]string{}
		s.live.occupants[guildID] = guildOccupants
	}

	prevChannelID := guildOccupants[userID]
	prevCount := 0
	if prevChannelID != "" {
		for existingUserID, current := range guildOccupants {
			if existingUserID == userID {
				continue
			}
			if current == prevChannelID && current != "" && current != channelID {
				prevCount++
			}
		}
	}

	if strings.TrimSpace(channelID) == "" {
		delete(guildOccupants, userID)
		return prevChannelID, prevCount, 0
	}

	guildOccupants[userID] = channelID
	nextCount := 0
	for _, current := range guildOccupants {
		if current == channelID {
			nextCount++
		}
	}
	return prevChannelID, prevCount, nextCount
}

func (s *Service) recordLiveParticipantMovement(guildID, userID, displayName, prevChannelID, channelID string) {
	participant := liveTraceParticipant{UserID: userID, DisplayName: displayName}
	var previousSession, nextSession *liveVoiceSession

	s.liveMu.Lock()
	if s.live.participants == nil {
		s.live.participants = map[string]map[string]liveTraceParticipant{}
	}
	guildParticipants := s.live.participants[guildID]
	if guildParticipants == nil {
		guildParticipants = map[string]liveTraceParticipant{}
		s.live.participants[guildID] = guildParticipants
	}
	guildParticipants[userID] = participant
	if prevChannelID != "" && prevChannelID != channelID {
		previousSession = s.live.sessions[liveSessionKey(guildID, prevChannelID)]
	}
	if channelID != "" {
		nextSession = s.live.sessions[liveSessionKey(guildID, channelID)]
	}
	s.liveMu.Unlock()

	switch {
	case prevChannelID != "" && channelID != "" && prevChannelID != channelID:
		if previousSession != nil && previousSession.trace != nil {
			previousSession.trace.RecordParticipant("participant_move", participant, prevChannelID, channelID)
		}
		if nextSession != nil && nextSession.trace != nil {
			nextSession.trace.RecordParticipant("participant_move", participant, prevChannelID, channelID)
		}
	case prevChannelID == "" && channelID != "":
		if nextSession != nil && nextSession.trace != nil {
			nextSession.trace.RecordParticipant("participant_join", participant, "", channelID)
		}
	case prevChannelID != "" && channelID == "":
		if previousSession != nil && previousSession.trace != nil {
			previousSession.trace.RecordParticipant("participant_leave", participant, prevChannelID, "")
		}
	}
}

func (s *Service) liveParticipantsForChannel(guildID, channelID string) []liveTraceParticipant {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	var participants []liveTraceParticipant
	for userID, currentChannelID := range s.live.occupants[guildID] {
		if currentChannelID != channelID {
			continue
		}
		participant := liveTraceParticipant{UserID: userID, DisplayName: userID}
		if known, ok := s.live.participants[guildID][userID]; ok {
			participant = known
		}
		participants = append(participants, participant)
	}
	sort.Slice(participants, func(i, j int) bool {
		return participants[i].UserID < participants[j].UserID
	})
	return participants
}

func (s *Service) liveChannelEnabled(channelID string) bool {
	for _, allowed := range s.cfg.Discord.Live.AutoJoinVoiceChannelIDs {
		if channelID == allowed {
			return true
		}
	}
	return false
}

func (s *Service) ensureLiveSession(guildID, voiceChannelID string) {
	if s.live.service == nil {
		s.logger.Warn("discord live service unavailable", "guild_id", guildID, "voice_channel_id", voiceChannelID)
		return
	}
	key := liveSessionKey(guildID, voiceChannelID)

	s.liveMu.Lock()
	if _, ok := s.live.sessions[key]; ok {
		s.liveMu.Unlock()
		return
	}
	if _, ok := s.live.starting[key]; ok {
		s.liveMu.Unlock()
		return
	}
	if s.live.maxSessions > 0 && len(s.live.sessions) >= s.live.maxSessions {
		s.liveMu.Unlock()
		s.logger.Warn("discord live max sessions reached", "voice_channel_id", voiceChannelID)
		return
	}
	s.live.starting[key] = struct{}{}
	s.liveMu.Unlock()

	defer func() {
		s.liveMu.Lock()
		delete(s.live.starting, key)
		s.liveMu.Unlock()
	}()

	openVoice := s.openVoiceConnection
	if openVoice == nil {
		openVoice = s.openDisgoVoiceConnection
	}
	voice, err := openVoice(s.context(), guildID, voiceChannelID)
	if err != nil {
		s.logger.Error("join discord voice", "error", err, "voice_channel_id", voiceChannelID)
		return
	}

	readyCtx, readyCancel := context.WithTimeout(s.context(), liveVoiceReadyTimeout)
	err = voice.WaitReady(readyCtx)
	readyCancel()
	if err != nil {
		_ = voice.Disconnect()
		s.logger.Error("wait for discord voice readiness", "error", err, "voice_channel_id", voiceChannelID)
		return
	}

	call, err := s.ensureLiveCallThread(s.context(), guildID, voiceChannelID)
	if err != nil {
		_ = voice.Disconnect()
		s.logger.Error("prepare discord live transcript thread", "error", err, "voice_channel_id", voiceChannelID)
		return
	}

	debugPaths := liveDebugArtifactPaths(guildID, voiceChannelID)
	trace, traceErr := newLiveTraceRecorder(debugPaths, liveTraceMetadata{
		CallID:                    call.ID,
		GuildID:                   guildID,
		VoiceChannelID:            voiceChannelID,
		TranscriptThreadID:        call.TranscriptThreadID,
		TranscriptParentChannelID: call.TranscriptParentChannelID,
		RootMessageID:             call.RootMessageID,
		StatusMessageID:           call.StatusMessageID,
		ProviderName:              s.cfg.Discord.Live.ProviderName,
		Participants:              s.liveParticipantsForChannel(guildID, voiceChannelID),
	})
	if traceErr != nil {
		s.logger.Warn("create live trace recorder", "error", traceErr)
	}
	if tracker, ok := voice.(*daveVoiceConnection); ok {
		tracker.setTrace(trace)
	}

	playback, err := newVoicePlayback(s.logger.With("guild_id", guildID, "voice_channel_id", voiceChannelID), voice, trace)
	if err != nil {
		if trace != nil {
			_, _ = trace.Close("playback create failed")
		}
		_ = voice.Disconnect()
		s.logger.Error("create discord live playback", "error", err)
		return
	}

	inboundWav, inboundErr := newWavRecorder(debugPaths.InboundWav, liveaudio.GeminiInputSampleRate)
	if inboundErr != nil {
		s.logger.Warn("create inbound wav recorder", "error", inboundErr)
	}
	outboundWav, outboundErr := newWavRecorder(debugPaths.OutboundWav, liveaudio.GeminiOutputSampleRate)
	if outboundErr != nil {
		s.logger.Warn("create outbound wav recorder", "error", outboundErr)
	}

	ctx, cancel := context.WithCancel(s.context())
	captureDone := make(chan struct{})
	session := &liveVoiceSession{
		svc:            s,
		key:            key,
		guildID:        guildID,
		voiceChannelID: voiceChannelID,
		voice:          voice,
		call:           call,
		cancel:         cancel,
		playback:       playback,
		inboundWav:     inboundWav,
		outboundWav:    outboundWav,
		trace:          trace,
		captureDone:    captureDone,
		state: liveCallState{
			Status: "connecting",
		},
	}
	session.persistState("connecting")

	conversation, err := s.live.service.Start(ctx, geminilive.StartRequest{
		ProviderName:      s.cfg.Discord.Live.ProviderName,
		SystemPrompt:      s.cfg.Discord.Live.SystemPrompt,
		VoiceName:         s.cfg.Discord.Live.VoiceName,
		SessionResumption: s.cfg.Discord.Live.SessionResumption,
		OnStatus:          session.handleStatus,
		OnTranscript:      session.handleTranscript,
		OnAudio: func(data []byte) {
			if trace != nil {
				trace.RecordGeminiAudioChunk(data)
			}
			if session.shouldSuppressGeminiAudio(data) {
				return
			}
			outboundWav.AppendPCMBytes(data)
			playback.Enqueue(data)
		},
		OnInterrupted: func() {
			if trace != nil {
				trace.RecordGeminiInterrupted()
			}
			playback.Interrupt("gemini interrupted")
		},
		OnSessionHandle: session.handleSessionHandle,
	})
	if err != nil {
		cancel()
		playback.Close()
		if trace != nil {
			_, _ = trace.Close("gemini live start failed")
		}
		_ = voice.Disconnect()
		s.logger.Error("start gemini live session", "error", err)
		session.postNotice(fmt.Sprintf("Failed to start Gemini Live: %v", err))
		return
	}
	session.conversation = conversation

	s.liveMu.Lock()
	s.live.sessions[key] = session
	s.liveMu.Unlock()

	go func() {
		defer close(captureDone)
		session.captureIncomingAudio(ctx)
	}()
}

func (s *Service) stopLiveSession(guildID, voiceChannelID, reason string) {
	key := liveSessionKey(guildID, voiceChannelID)

	s.liveMu.Lock()
	session := s.live.sessions[key]
	if session != nil {
		delete(s.live.sessions, key)
	}
	s.liveMu.Unlock()

	if session == nil {
		return
	}
	session.shutdown(reason)
}

func (s *Service) ensureLiveCallThread(ctx context.Context, guildID, voiceChannelID string) (*knowledge.DiscordLiveCall, error) {
	parentChannelID := strings.TrimSpace(s.cfg.Discord.Live.TranscriptThreadParentChannelID)
	if parentChannelID == "" {
		return nil, fmt.Errorf("discord.live.transcript_thread_parent_channel_id is required: set it to the text channel ID where the bot should post a call message and create the transcript thread")
	}

	channelName := voiceChannelID
	if channel, err := s.api.GetChannel(ctx, voiceChannelID); err == nil && channel != nil && strings.TrimSpace(channel.Name) != "" {
		channelName = channel.Name
	}

	root, err := s.api.CreateMessage(ctx, parentChannelID, createMessageRequest{
		Content: fmt.Sprintf("Gemini Live call started for voice channel <#%s>.", voiceChannelID),
	})
	if err != nil {
		return nil, fmt.Errorf("send discord live root message: %w", err)
	}
	thread, err := s.api.CreateThreadFromMessage(ctx, parentChannelID, root.ID, "voice "+channelName)
	if err != nil {
		return nil, fmt.Errorf("start discord live thread: %w", err)
	}
	statusMessage, err := s.api.CreateMessage(ctx, thread.ID, createMessageRequest{Content: "Status: connecting"})
	if err != nil {
		return nil, fmt.Errorf("send discord live status message: %w", err)
	}

	return s.svcStore().SaveDiscordLiveCall(ctx, knowledge.SaveDiscordLiveCallInput{
		GuildID:                   guildID,
		VoiceChannelID:            voiceChannelID,
		TranscriptParentChannelID: parentChannelID,
		TranscriptThreadID:        thread.ID,
		RootMessageID:             root.ID,
		StatusMessageID:           statusMessage.ID,
		ProviderName:              s.cfg.Discord.Live.ProviderName,
		Status:                    "connecting",
		State:                     json.RawMessage(`{"status":"connecting"}`),
	})
}

func (s *Service) openDisgoVoiceConnection(ctx context.Context, guildID, voiceChannelID string) (voiceConnection, error) {
	if s.voiceManager == nil {
		return nil, fmt.Errorf("voice manager is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	guildSnowflake, err := parseSnowflake(guildID)
	if err != nil {
		return nil, err
	}
	channelSnowflake, err := parseSnowflake(voiceChannelID)
	if err != nil {
		return nil, err
	}

	conn := s.voiceManager.CreateConn(guildSnowflake)
	voice := newDaveVoiceConnection(s.logger.With("guild_id", guildID, "voice_channel_id", voiceChannelID), s.voiceManager, guildSnowflake, channelSnowflake, conn)
	conn.SetEventHandlerFunc(voice.handleGatewayEvent)

	openCtx, cancel := context.WithTimeout(ctx, liveVoiceOpenTimeout)
	defer cancel()
	if err := conn.Open(openCtx, channelSnowflake, false, false); err != nil {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if s.runtime != nil {
			if disconnectErr := s.runtime.UpdateVoiceState(closeCtx, guildSnowflake, nil, false, false); disconnectErr != nil {
				s.logger.Warn("disconnect failed voice open", "guild_id", guildID, "voice_channel_id", voiceChannelID, "error", disconnectErr)
			}
		}
		if gateway := conn.Gateway(); gateway != nil {
			gateway.Close()
		}
		s.voiceManager.RemoveConn(guildSnowflake)
		return nil, fmt.Errorf("open DAVE voice connection: %w", err)
	}
	voice.markSessionDescribed()
	voice.startReceiver()
	voice.startMonitor()
	return voice, nil
}

func newDaveVoiceConnection(logger *slog.Logger, manager voiceManager, guildID, channelID snowflake.ID, conn disgovoice.Conn) *daveVoiceConnection {
	return &daveVoiceConnection{
		logger:       logger,
		manager:      manager,
		guildID:      guildID,
		channelID:    channelID,
		conn:         conn,
		recv:         make(chan opusPacket, 256),
		done:         make(chan struct{}),
		stateChanged: make(chan struct{}),
		state:        VoiceTransportOpening,
	}
}

func (v *daveVoiceConnection) handleGatewayEvent(gateway disgovoice.Gateway, op disgovoice.Opcode, _ int, _ disgovoice.GatewayMessageData) {
	switch op {
	case disgovoice.OpcodeSessionDescription:
		v.markSessionDescribed()
	case disgovoice.OpcodeReady, disgovoice.OpcodeResumed:
		v.syncTransportState(gateway.Status())
	}
}

func (v *daveVoiceConnection) startMonitor() {
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-v.done:
				return
			case <-ticker.C:
				v.syncTransportState(v.conn.Gateway().Status())
			}
		}
	}()
}

func (v *daveVoiceConnection) startReceiver() {
	go func() {
		defer close(v.done)
		defer close(v.recv)
		for {
			packet, err := v.conn.UDP().ReadPacket()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				if isDecryptError(err) {
					v.bumpDecryptError(err)
					continue
				}
				v.bumpReadError(err)
				continue
			}
			if packet == nil || len(packet.Opus) == 0 {
				continue
			}
			v.statsMu.Lock()
			v.inboundFrames++
			v.statsMu.Unlock()

			userID := ""
			if snowflakeID := v.conn.UserIDBySSRC(packet.SSRC); snowflakeID != 0 {
				userID = snowflakeID.String()
			}
			frame := opusPacket{
				SSRC:       packet.SSRC,
				Sequence:   packet.Sequence,
				Timestamp:  packet.Timestamp,
				UserID:     userID,
				ReceivedAt: time.Now(),
				Opus:       append([]byte(nil), packet.Opus...),
			}
			select {
			case v.recv <- frame:
			default:
				v.statsMu.Lock()
				v.droppedFrames++
				v.statsMu.Unlock()
			}
		}
	}()
}

func (v *daveVoiceConnection) RecvOpus() <-chan opusPacket {
	return v.recv
}

func (v *daveVoiceConnection) Disconnect() error {
	var err error
	v.closeOnce.Do(func() {
		v.setTransportState(VoiceTransportClosing)
		_ = v.StopSpeaking()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		v.conn.Close(ctx)
		if v.manager != nil {
			v.manager.RemoveConn(v.guildID)
		}
		<-v.done
		v.setTransportState(VoiceTransportClosed)
		v.logSummary()
	})
	return err
}

func (v *daveVoiceConnection) WaitReady(ctx context.Context) error {
	for {
		state, changed := v.snapshotTransportState()
		switch state {
		case VoiceTransportSessionDescribed, VoiceTransportAudioReady:
			return nil
		case VoiceTransportClosing, VoiceTransportClosed:
			return errVoiceTransportClosed
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (v *daveVoiceConnection) TransportState() VoiceTransportState {
	state, _ := v.snapshotTransportState()
	return state
}

func (v *daveVoiceConnection) StartSpeaking() error {
	v.speakingMu.Lock()
	defer v.speakingMu.Unlock()
	if v.speaking {
		return nil
	}
	if v.TransportState() != VoiceTransportAudioReady {
		return errVoiceTransportNotReady
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := v.conn.SetSpeaking(ctx, disgovoice.SpeakingFlagMicrophone); err != nil {
		if errors.Is(err, disgovoice.ErrGatewayNotConnected) || errors.Is(err, discord.ErrShardNotReady) {
			v.setTransportState(VoiceTransportReconnecting)
			return errVoiceTransportNotReady
		}
		return err
	}
	v.speaking = true
	return nil
}

func (v *daveVoiceConnection) StopSpeaking() error {
	v.speakingMu.Lock()
	defer v.speakingMu.Unlock()
	if !v.speaking {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := v.conn.SetSpeaking(ctx, disgovoice.SpeakingFlagNone); err != nil {
		if errors.Is(err, disgovoice.ErrGatewayNotConnected) || errors.Is(err, discord.ErrShardNotReady) {
			v.setTransportState(VoiceTransportReconnecting)
			v.speaking = false
			return errVoiceTransportNotReady
		}
		return err
	}
	v.speaking = false
	return nil
}

func (v *daveVoiceConnection) SendOpus(frame []byte) error {
	if len(frame) == 0 {
		return nil
	}
	if v.TransportState() != VoiceTransportAudioReady {
		return errVoiceTransportNotReady
	}
	if _, err := v.conn.UDP().Write(frame); err != nil {
		if errors.Is(err, disgovoice.ErrGatewayNotConnected) || errors.Is(err, discord.ErrShardNotReady) {
			v.setTransportState(VoiceTransportReconnecting)
			return errVoiceTransportNotReady
		}
		return err
	}
	v.statsMu.Lock()
	v.outboundFrames++
	v.statsMu.Unlock()
	return nil
}

func (v *daveVoiceConnection) snapshotTransportState() (VoiceTransportState, <-chan struct{}) {
	v.stateMu.Lock()
	defer v.stateMu.Unlock()
	return v.state, v.stateChanged
}

func (v *daveVoiceConnection) setTrace(trace *liveTraceRecorder) {
	v.traceMu.Lock()
	v.trace = trace
	v.traceMu.Unlock()
	if trace != nil {
		trace.RecordTransportState(v.TransportState())
	}
}

func (v *daveVoiceConnection) traceTransportState(state VoiceTransportState) {
	v.traceMu.Lock()
	trace := v.trace
	v.traceMu.Unlock()
	if trace != nil {
		trace.RecordTransportState(state)
	}
}

func (v *daveVoiceConnection) setTransportState(next VoiceTransportState) {
	v.stateMu.Lock()
	if v.state == next {
		v.stateMu.Unlock()
		return
	}
	close(v.stateChanged)
	v.stateChanged = make(chan struct{})
	v.state = next
	v.stateMu.Unlock()
	v.traceTransportState(next)
}

func (v *daveVoiceConnection) markSessionDescribed() {
	v.stateMu.Lock()
	v.sessionReady = true
	v.stateMu.Unlock()
	v.syncTransportState(v.conn.Gateway().Status())
}

func (v *daveVoiceConnection) syncTransportState(status disgovoice.Status) {
	v.stateMu.Lock()
	if v.state == VoiceTransportClosing || v.state == VoiceTransportClosed {
		v.stateMu.Unlock()
		return
	}

	next := VoiceTransportOpening
	switch {
	case !v.sessionReady:
		next = VoiceTransportOpening
	case status == disgovoice.StatusReady:
		next = VoiceTransportAudioReady
		v.everAudioReady = true
	case v.everAudioReady:
		next = VoiceTransportReconnecting
	default:
		next = VoiceTransportSessionDescribed
	}

	if v.state == next {
		v.stateMu.Unlock()
		return
	}
	close(v.stateChanged)
	v.stateChanged = make(chan struct{})
	v.state = next
	v.stateMu.Unlock()
	v.traceTransportState(next)
}

func (v *daveVoiceConnection) bumpDecryptError(err error) {
	v.statsMu.Lock()
	count := v.decryptErrors + 1
	v.decryptErrors = count
	inbound := v.inboundFrames
	v.statsMu.Unlock()

	state := v.TransportState()
	if count <= 5 {
		v.logger.Info("discord live decrypt failure",
			"decrypt_errors", count,
			"inbound_frames", inbound,
			"transport_state", string(state),
			"error", err,
		)
		return
	}
	if count%50 == 0 {
		v.logger.Warn("discord live decrypt failures continuing",
			"decrypt_errors", count,
			"inbound_frames", inbound,
			"transport_state", string(state),
		)
	}
}

// isDecryptError recognises packet-read failures that actually represent a
// decryption problem, whether from disgo's AEAD layer or from the DAVE layer.
// disgo wraps DAVE errors as `fmt.Errorf("failed to DAVE decrypt packet: %w", err)`
// where the inner error is a libdave sentinel, not disgovoice.ErrDecryptionFailed,
// so errors.Is alone misses them.
func isDecryptError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, disgovoice.ErrDecryptionFailed) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "DAVE decrypt") || strings.Contains(msg, "decrypt packet")
}

func (v *daveVoiceConnection) bumpReadError(err error) {
	v.statsMu.Lock()
	defer v.statsMu.Unlock()
	v.readErrors++
	if v.readErrors%25 == 0 {
		v.logger.Warn("discord live read failures continuing", "read_errors", v.readErrors, "error", err)
	}
}

func (v *daveVoiceConnection) logSummary() {
	v.statsMu.Lock()
	defer v.statsMu.Unlock()
	v.logger.Info("discord live voice summary",
		"inbound_frames", v.inboundFrames,
		"outbound_frames", v.outboundFrames,
		"dropped_frames", v.droppedFrames,
		"dropped_outbound_frames", v.droppedOutboundFrames,
		"decrypt_errors", v.decryptErrors,
		"read_errors", v.readErrors,
	)
}

func (v *daveVoiceConnection) recordDroppedOutboundFrames(count int) {
	if count <= 0 {
		return
	}
	v.statsMu.Lock()
	v.droppedOutboundFrames += count
	v.statsMu.Unlock()
}

func decoderStateForSSRC(states map[uint32]*discordOpusDecoderState, ssrc uint32) (*discordOpusDecoderState, error) {
	if state, ok := states[ssrc]; ok {
		return state, nil
	}
	decoder, err := opus.NewDecoder(liveaudio.DiscordSampleRate, 2)
	if err != nil {
		return nil, err
	}
	// Opus frames can be up to 120ms; allocate for the worst case so
	// longer frames don't get rejected with "corrupted stream" when the
	// PCM output would overflow a smaller buffer.
	const maxOpusFrameSamplesPerChannel = liveaudio.DiscordSampleRate * 120 / 1000
	state := &discordOpusDecoderState{
		decoder:   decoder,
		pcmBuffer: make([]int16, maxOpusFrameSamplesPerChannel*2),
	}
	states[ssrc] = state
	return state, nil
}

func isLocalSpeechFrame(peak, rms int) bool {
	return peak >= localSpeechPeakThreshold || rms >= localSpeechRMSThreshold
}

func (c *localSpeechCoordinator) Observe(at time.Time, speech bool) localSpeechObservation {
	if at.IsZero() {
		at = time.Now()
	}
	var obs localSpeechObservation
	if speech {
		if c.active {
			c.lastSpeechFrame = at
			return obs
		}
		if c.candidateFrames == 0 || at.Sub(c.candidateStart) > localSpeechStartWindow {
			c.candidateStart = at
			c.candidateFrames = 1
		} else {
			c.candidateFrames++
		}
		if c.candidateFrames >= localSpeechStartFrames {
			c.active = true
			c.speechStart = c.candidateStart
			c.lastSpeechFrame = at
			c.candidateFrames = 0
			obs.Started = true
			obs.StartAt = c.speechStart
		}
		return obs
	}
	if c.active {
		if at.Sub(c.lastSpeechFrame) >= localSpeechEndSilence {
			endAt := c.lastSpeechFrame.Add(liveaudio.FrameDurationMS * time.Millisecond)
			c.active = false
			obs.Ended = true
			obs.EndAt = endAt
			obs.DurationMS = int(endAt.Sub(c.speechStart).Milliseconds())
		}
		return obs
	}
	if c.candidateFrames > 0 && at.Sub(c.candidateStart) > localSpeechStartWindow {
		c.candidateFrames = 0
		c.candidateStart = time.Time{}
	}
	return obs
}

func (s *liveInputEndpointState) MarkRealFrame(at time.Time) {
	if at.IsZero() {
		at = time.Now()
	}
	s.open = true
	s.ended = false
	s.lastRealFrame = at
	s.padFrames = 0
}

func (s *liveInputEndpointState) Observe(now time.Time) liveEndpointAction {
	if now.IsZero() {
		now = time.Now()
	}
	if !s.open || s.ended || now.Sub(s.lastRealFrame) <= endpointSilenceGap {
		return liveEndpointActionNone
	}
	if s.padFrames < endpointMaxSilencePadFrames {
		s.padFrames++
		return liveEndpointActionPad
	}
	s.ended = true
	return liveEndpointActionEnd
}

func (s *liveInputEndpointState) PaddedMS() int {
	return s.padFrames * liveaudio.FrameDurationMS
}

func (s *liveVoiceSession) handleLocalSpeechStart(at time.Time, packet opusPacket, peak, rms int) {
	if s.trace != nil {
		s.trace.RecordLocalSpeechStart(at, packet, peak, rms)
	}
	s.bargeMu.Lock()
	s.localUserSpeaking = true
	suppressUntil := at.Add(geminiAudioSuppressWindow)
	if suppressUntil.After(s.suppressGeminiUntil) {
		s.suppressGeminiUntil = suppressUntil
	}
	s.bargeMu.Unlock()

	if s.playback == nil {
		return
	}
	pendingMS := s.playback.PendingMS()
	active := s.playback.ActiveRecently(localBargeInPlaybackWindow)
	if !active && pendingMS <= 0 {
		return
	}
	if s.trace != nil {
		s.trace.RecordLocalBargeIn(at, pendingMS, active)
	}
	s.playback.Interrupt("local barge-in")
}

func (s *liveVoiceSession) handleLocalSpeechEnd(at time.Time, durationMS int) {
	if s.trace != nil {
		s.trace.RecordLocalSpeechEnd(at, durationMS)
	}
	s.bargeMu.Lock()
	s.localUserSpeaking = false
	s.bargeMu.Unlock()
}

func (s *liveVoiceSession) shouldSuppressGeminiAudio(data []byte) bool {
	now := time.Now()
	s.bargeMu.Lock()
	suppress := s.localUserSpeaking || now.Before(s.suppressGeminiUntil)
	s.bargeMu.Unlock()
	if suppress && s.trace != nil {
		s.trace.RecordGeminiAudioSuppressed(data)
	}
	return suppress
}

func (s *liveVoiceSession) captureIncomingAudio(ctx context.Context) {
	decoders := map[uint32]*discordOpusDecoderState{}
	var decodeErrors, sentFrames, silentFrames, paddingFrames int
	var peakSample int
	defer func() {
		s.svc.logger.Info("discord live capture summary",
			"guild_id", s.guildID,
			"voice_channel_id", s.voiceChannelID,
			"decode_errors", decodeErrors,
			"pcm_sent_to_gemini", sentFrames,
			"silent_pad_frames", paddingFrames,
			"quiet_voice_frames", silentFrames,
			"peak_sample", peakSample,
		)
	}()

	// Discord's voice transport only sends packets during voice activity
	// (voice-activated). When the user stops speaking, the stream simply
	// pauses. Gemini's server-side VAD needs actual silence *samples* to
	// commit end-of-speech and produce a response, but unbounded silence keeps
	// the stream alive after the call ends. Send bounded padding, then explicitly
	// end the current audio stream until Discord sends another real frame.
	silencePCM := liveaudio.Int16ToPCMBytes(make([]int16, liveaudio.GeminiInputFrameSamples))
	padTicker := time.NewTicker(liveaudio.FrameDurationMS * time.Millisecond)
	defer padTicker.Stop()
	var endpoint liveInputEndpointState
	var speech localSpeechCoordinator

	for {
		select {
		case <-ctx.Done():
			return
		case <-padTicker.C:
			now := time.Now()
			if obs := speech.Observe(now, false); obs.Ended {
				s.handleLocalSpeechEnd(obs.EndAt, obs.DurationMS)
			}
			switch endpoint.Observe(now) {
			case liveEndpointActionPad:
				if err := s.conversation.SendPCM16(silencePCM); err != nil {
					s.svc.logger.Warn("send gemini live silence pad", "error", err)
					if s.trace != nil {
						s.trace.RecordGeminiInputSendError(err)
					}
					continue
				}
				paddingFrames++
				if s.trace != nil {
					s.trace.RecordSilencePad(liveaudio.FrameDurationMS)
				}
			case liveEndpointActionEnd:
				if err := s.conversation.EndAudio(); err != nil {
					s.svc.logger.Warn("send gemini live audio stream end", "error", err)
					if s.trace != nil {
						s.trace.RecordGeminiInputSendError(err)
					}
					continue
				}
				if s.trace != nil {
					s.trace.RecordAudioStreamEndSent(endpoint.PaddedMS())
				}
			}
		case packet, ok := <-s.voice.RecvOpus():
			if !ok {
				return
			}
			if len(packet.Opus) == 0 || len(packet.Opus) > disgovoice.MaxOpusFrameSize || bytes.Equal(packet.Opus, disgovoice.SilenceAudioFrame) {
				continue
			}
			decoderState, err := decoderStateForSSRC(decoders, packet.SSRC)
			if err != nil {
				s.svc.logger.Error("create opus decoder", "error", err, "ssrc", packet.SSRC)
				go s.shutdown("decoder error")
				return
			}
			n, err := decoderState.decoder.Decode(packet.Opus, decoderState.pcmBuffer)
			if err != nil {
				decodeErrors++
				if s.trace != nil {
					s.trace.RecordDecodeError(packet.SSRC, len(packet.Opus), err)
				}
				if decodeErrors%discordOpusDecodeWarningEvery == 0 {
					s.svc.logger.Warn("decode discord opus", "guild_id", s.guildID, "voice_channel_id", s.voiceChannelID, "errors", decodeErrors, "frame_len", len(packet.Opus), "ssrc", packet.SSRC, "error", err)
				}
				continue
			}
			mono := liveaudio.StereoToMono(decoderState.pcmBuffer[:n*2])
			resampled := liveaudio.ResampleLinearInt16(mono, liveaudio.DiscordSampleRate, liveaudio.GeminiInputSampleRate)
			if len(resampled) == 0 {
				continue
			}
			peak, rms := liveTracePeakRMS(resampled)
			if peak > peakSample {
				peakSample = peak
			}
			if peak < liveTraceSpeechPeak {
				silentFrames++
			}
			frameAt := packet.ReceivedAt
			if frameAt.IsZero() {
				frameAt = time.Now()
			}
			if obs := speech.Observe(frameAt, isLocalSpeechFrame(peak, rms)); obs.Started {
				s.handleLocalSpeechStart(obs.StartAt, packet, peak, rms)
			} else if obs.Ended {
				s.handleLocalSpeechEnd(obs.EndAt, obs.DurationMS)
			}
			endpoint.MarkRealFrame(frameAt)
			s.inboundWav.AppendInt16(resampled)
			if err := s.conversation.SendPCM16(liveaudio.Int16ToPCMBytes(resampled)); err != nil {
				s.svc.logger.Warn("send gemini live audio", "error", err)
				if s.trace != nil {
					s.trace.RecordInboundAudioFrame(packet, resampled, peak, rms, false)
					s.trace.RecordGeminiInputSendError(err)
				}
				continue
			}
			if s.trace != nil {
				s.trace.RecordInboundAudioFrame(packet, resampled, peak, rms, true)
			}
			sentFrames++
		}
	}
}

func (s *liveVoiceSession) handleStatus(status string) {
	status = strings.TrimSpace(status)
	if status == "" {
		return
	}
	s.svc.logger.Info("discord live status update", "guild_id", s.guildID, "voice_channel_id", s.voiceChannelID, "status", status)
	if s.trace != nil {
		s.trace.RecordGeminiStatus(status)
	}
	s.persistState(status)
}

func (s *liveVoiceSession) handleSessionHandle(handle string) {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return
	}
	s.svc.logger.Info("discord live session handle received", "guild_id", s.guildID, "voice_channel_id", s.voiceChannelID, "has_handle", true)
	if s.trace != nil {
		s.trace.RecordSessionHandle(handle)
	}
	s.call.SessionHandle = handle
	s.persistState("")
}

func (s *liveVoiceSession) handleTranscript(evt geminilive.TranscriptEvent) {
	text := strings.TrimSpace(evt.Text)
	if text == "" {
		return
	}
	if s.trace != nil {
		s.trace.RecordTranscript(evt.Role, text, evt.Final)
	}

	s.mu.Lock()
	switch evt.Role {
	case "assistant":
		if evt.Final {
			s.state.PartialAssistant = ""
		} else {
			s.state.PartialAssistant = text
		}
	default:
		if evt.Final {
			s.state.PartialUser = ""
		} else {
			s.state.PartialUser = text
		}
	}
	shouldPost := evt.Final
	if evt.Final {
		entry := liveTranscriptEntry{Role: evt.Role, Text: text}
		if len(s.state.Entries) == 0 || s.state.Entries[len(s.state.Entries)-1] != entry {
			s.state.Entries = append(s.state.Entries, entry)
		}
	}
	state := s.state
	s.mu.Unlock()

	if shouldPost {
		s.svc.logger.Info("discord live final transcript", "guild_id", s.guildID, "voice_channel_id", s.voiceChannelID, "role", evt.Role, "text", text)
		prefix := "**You:** "
		if evt.Role == "assistant" {
			prefix = "**Gemini:** "
		}
		_, _ = s.svc.api.CreateMessage(s.svc.context(), s.call.TranscriptThreadID, createMessageRequest{
			Content: prefix + text,
		})
	}
	s.updateStatusMessage(state)
	s.persistState("")
}

func (s *liveVoiceSession) updateStatusMessage(state liveCallState) {
	content := "Status: " + strings.TrimSpace(state.Status)
	if state.PartialUser != "" {
		content += "\nListening: " + state.PartialUser
	}
	if state.PartialAssistant != "" {
		content += "\nGemini: " + state.PartialAssistant
	}
	_ = s.svc.api.UpdateMessage(s.svc.context(), s.call.TranscriptThreadID, s.call.StatusMessageID, updateMessageRequest{
		Content: content,
	})
}

func (s *liveVoiceSession) persistState(status string) {
	s.mu.Lock()
	if status != "" {
		s.state.Status = status
	}
	payload, _ := json.Marshal(s.state)
	state := s.state
	s.mu.Unlock()

	if status != "" {
		s.call.Status = status
	}
	s.call.State = payload
	saved, err := s.svc.store.SaveDiscordLiveCall(s.svc.context(), knowledge.SaveDiscordLiveCallInput{
		ID:                        s.call.ID,
		GuildID:                   s.call.GuildID,
		VoiceChannelID:            s.call.VoiceChannelID,
		TranscriptParentChannelID: s.call.TranscriptParentChannelID,
		TranscriptThreadID:        s.call.TranscriptThreadID,
		RootMessageID:             s.call.RootMessageID,
		StatusMessageID:           s.call.StatusMessageID,
		ProviderName:              s.call.ProviderName,
		SessionHandle:             s.call.SessionHandle,
		Status:                    s.call.Status,
		State:                     payload,
	})
	if err == nil && saved != nil {
		s.call = saved
	}
	s.updateStatusMessage(state)
}

func (s *liveVoiceSession) shutdown(reason string) {
	if reason == "" {
		reason = "ended"
	}
	s.svc.logger.Info("discord live session shutting down", "guild_id", s.guildID, "voice_channel_id", s.voiceChannelID, "reason", reason)
	if s.cancel != nil {
		s.cancel()
	}
	if s.captureDone != nil {
		select {
		case <-s.captureDone:
		case <-time.After(500 * time.Millisecond):
			s.svc.logger.Warn("timed out waiting for discord live capture shutdown", "guild_id", s.guildID, "voice_channel_id", s.voiceChannelID)
		}
	}
	if s.conversation != nil {
		_ = s.conversation.EndAudio()
		_ = s.conversation.Close()
	}
	if s.playback != nil {
		s.playback.Close()
	}
	if s.voice != nil {
		_ = s.voice.Disconnect()
	}
	if path, samples, err := s.inboundWav.Close(); err != nil {
		s.svc.logger.Warn("close inbound wav", "error", err, "path", path)
	} else if samples > 0 {
		s.svc.logger.Info("discord live inbound wav saved",
			"path", path,
			"samples", samples,
			"duration_ms", samples*1000/liveaudio.GeminiInputSampleRate,
		)
	}
	if path, samples, err := s.outboundWav.Close(); err != nil {
		s.svc.logger.Warn("close outbound wav", "error", err, "path", path)
	} else if samples > 0 {
		s.svc.logger.Info("discord live outbound wav saved",
			"path", path,
			"samples", samples,
			"duration_ms", samples*1000/liveaudio.GeminiOutputSampleRate,
		)
	}
	if s.trace != nil {
		if path, err := s.trace.Close(reason); err != nil {
			s.svc.logger.Warn("close discord live trace", "error", err, "path", path)
		} else if path != "" {
			s.svc.logger.Info("discord live trace saved", "path", path)
		}
	}
	s.persistState(reason)
	_, _ = s.svc.api.CreateMessage(s.svc.context(), s.call.TranscriptThreadID, createMessageRequest{
		Content: "Gemini Live call ended: " + reason,
	})
}

func (s *liveVoiceSession) postNotice(message string) {
	if s.call == nil || strings.TrimSpace(s.call.TranscriptThreadID) == "" {
		return
	}
	_, _ = s.svc.api.CreateMessage(s.svc.context(), s.call.TranscriptThreadID, createMessageRequest{Content: message})
}

func newVoicePlayback(logger *slog.Logger, voice voiceConnection, trace *liveTraceRecorder) (*voicePlayback, error) {
	encoder, err := opus.NewEncoder(liveaudio.DiscordSampleRate, 2, opus.AppVoIP)
	if err != nil {
		return nil, err
	}
	playback := &voicePlayback{
		logger:  logger,
		voice:   voice,
		encoder: encoder,
		trace:   trace,
		events:  make(chan playbackEvent, 32),
		done:    make(chan struct{}),
	}
	go playback.loop()
	return playback, nil
}

func (p *voicePlayback) Enqueue(data []byte) {
	if len(data) == 0 {
		return
	}
	p.events <- playbackEvent{audio: append([]byte(nil), data...)}
}

func (p *voicePlayback) Interrupt(reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "playback interrupted"
	}
	select {
	case p.events <- playbackEvent{reset: true, resetReason: reason}:
	default:
	}
}

func (p *voicePlayback) Close() {
	p.events <- playbackEvent{close: true}
	<-p.done
}

func (p *voicePlayback) ActiveRecently(window time.Duration) bool {
	if p == nil {
		return false
	}
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	if p.speakingState || p.pendingSamples > 0 {
		return true
	}
	return !p.lastSentAt.IsZero() && time.Since(p.lastSentAt) <= window
}

func (p *voicePlayback) PendingMS() int {
	if p == nil {
		return 0
	}
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return p.pendingSamples * 1000 / liveaudio.GeminiOutputSampleRate
}

func (p *voicePlayback) setState(pendingSamples int, speaking bool, sentAt time.Time) {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	p.pendingSamples = pendingSamples
	p.speakingState = speaking
	if !sentAt.IsZero() {
		p.lastSentAt = sentAt
	}
}

func (p *voicePlayback) loop() {
	defer close(p.done)

	var pending []int16
	var pendingSince time.Time
	var speaking bool
	packetBuffer := make([]byte, 4000)
	playbackTimer := time.NewTimer(time.Hour)
	if !playbackTimer.Stop() {
		<-playbackTimer.C
	}
	idleTimer := time.NewTimer(time.Hour)
	if !idleTimer.Stop() {
		<-idleTimer.C
	}
	retryTimer := time.NewTimer(time.Hour)
	if !retryTimer.Stop() {
		<-retryTimer.C
	}
	var playbackC <-chan time.Time
	var idleC <-chan time.Time
	var retryC <-chan time.Time

	stopTimer := func(timer *time.Timer) {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
	armPlaybackTimer := func(delay time.Duration) {
		stopTimer(playbackTimer)
		playbackTimer.Reset(delay)
		playbackC = playbackTimer.C
	}
	clearPlaybackTimer := func() {
		stopTimer(playbackTimer)
		playbackC = nil
	}
	resetIdleTimer := func() {
		stopTimer(idleTimer)
		idleTimer.Reset(120 * time.Millisecond)
		idleC = idleTimer.C
	}
	resetRetryTimer := func() {
		stopTimer(retryTimer)
		retryTimer.Reset(playbackRetryInterval)
		retryC = retryTimer.C
	}
	clearRetryTimer := func() {
		stopTimer(retryTimer)
		retryC = nil
	}
	stopSpeaking := func() {
		if !speaking {
			return
		}
		silenceFrames := 5
		for i := 0; i < 5; i++ {
			_ = p.voice.SendOpus(disgovoice.SilenceAudioFrame)
		}
		_ = p.voice.StopSpeaking()
		if p.trace != nil {
			p.trace.RecordSpeakingStop(silenceFrames)
		}
		speaking = false
		idleC = nil
		p.setState(len(pending), speaking, time.Time{})
	}
	clearPending := func(reason string) {
		if len(pending) == 0 {
			return
		}
		queuedSamples := len(pending)
		dropped := queuedSamples / liveaudio.GeminiOutputFrameSamples
		pending = nil
		pendingSince = time.Time{}
		clearPlaybackTimer()
		clearRetryTimer()
		if tracker, ok := p.voice.(*daveVoiceConnection); ok {
			tracker.recordDroppedOutboundFrames(dropped)
		}
		p.logger.Warn("drop buffered discord live audio", "reason", reason, "dropped_frames", dropped)
		if p.trace != nil {
			switch reason {
			case "playback closed", "voice transport not ready":
				p.trace.RecordPlaybackDrop(reason, queuedSamples)
			default:
				p.trace.RecordPlaybackInterruptClear(reason, queuedSamples)
			}
		}
		p.setState(len(pending), speaking, time.Time{})
	}
	scheduleNextPlaybackFrame := func(delay time.Duration) {
		if len(pending) >= liveaudio.GeminiOutputFrameSamples && playbackC == nil && retryC == nil {
			armPlaybackTimer(delay)
		}
	}
	sendOnePendingFrame := func(now time.Time) bool {
		if len(pending) < liveaudio.GeminiOutputFrameSamples {
			if len(pending) == 0 {
				pendingSince = time.Time{}
				clearRetryTimer()
			}
			return false
		}
		if state := p.voice.TransportState(); state != VoiceTransportAudioReady {
			if p.trace != nil {
				p.trace.RecordPlaybackTransportRetry(state, len(pending))
			}
			if pendingSince.IsZero() {
				pendingSince = now
			}
			maxBufferedSamples := maxBufferedPlaybackFrameCount * liveaudio.GeminiOutputFrameSamples
			if len(pending) > maxBufferedSamples || now.Sub(pendingSince) > playbackNotReadyGrace {
				clearPending("voice transport not ready")
				stopSpeaking()
				return false
			}
			clearPlaybackTimer()
			resetRetryTimer()
			return false
		}

		frame24 := append([]int16(nil), pending[:liveaudio.GeminiOutputFrameSamples]...)
		frame48 := liveaudio.ResampleLinearInt16(frame24, liveaudio.GeminiOutputSampleRate, liveaudio.DiscordSampleRate)
		stereo := liveaudio.MonoToStereo(frame48)
		n, err := p.encoder.Encode(stereo, packetBuffer)
		if err != nil {
			if p.trace != nil {
				p.trace.RecordSendError("encode_discord_opus", err)
			}
			p.logger.Warn("encode discord opus", "error", err)
			pending = pending[liveaudio.GeminiOutputFrameSamples:]
			p.setState(len(pending), speaking, time.Time{})
			return true
		}
		if !speaking {
			if err := p.voice.StartSpeaking(); err != nil {
				if errors.Is(err, errVoiceTransportNotReady) {
					if p.trace != nil {
						p.trace.RecordPlaybackTransportRetry(p.voice.TransportState(), len(pending))
					}
					if pendingSince.IsZero() {
						pendingSince = now
					}
					clearPlaybackTimer()
					resetRetryTimer()
					return false
				}
				if p.trace != nil {
					p.trace.RecordSendError("start_speaking", err)
				}
				p.logger.Warn("start discord speaking", "error", err)
				pending = pending[liveaudio.GeminiOutputFrameSamples:]
				p.setState(len(pending), speaking, time.Time{})
				return true
			}
			speaking = true
			if p.trace != nil {
				p.trace.RecordSpeakingStart()
			}
			p.setState(len(pending), speaking, time.Time{})
		}
		queuedBefore := len(pending)
		sentAt := time.Now()
		if err := p.voice.SendOpus(packetBuffer[:n]); err != nil {
			if errors.Is(err, errVoiceTransportNotReady) {
				if p.trace != nil {
					p.trace.RecordPlaybackTransportRetry(p.voice.TransportState(), len(pending))
				}
				if pendingSince.IsZero() {
					pendingSince = now
				}
				clearPlaybackTimer()
				resetRetryTimer()
				return false
			}
			if p.trace != nil {
				p.trace.RecordSendError("send_opus", err)
			}
			pending = pending[liveaudio.GeminiOutputFrameSamples:]
			p.setState(len(pending), speaking, time.Time{})
			p.logger.Warn("send discord opus", "error", err)
			return true
		}
		pending = pending[liveaudio.GeminiOutputFrameSamples:]
		p.setState(len(pending), speaking, sentAt)
		if p.trace != nil {
			p.trace.RecordPlaybackFrameSent(frame48, n, queuedBefore, len(pending))
		}
		if len(pending) == 0 {
			pendingSince = time.Time{}
			clearRetryTimer()
		}
		resetIdleTimer()
		return true
	}

	for {
		select {
		case <-playbackC:
			playbackC = nil
			if sendOnePendingFrame(time.Now()) {
				scheduleNextPlaybackFrame(liveaudio.FrameDurationMS * time.Millisecond)
			}
		case <-idleC:
			stopSpeaking()
		case <-retryC:
			retryC = nil
			if sendOnePendingFrame(time.Now()) {
				scheduleNextPlaybackFrame(liveaudio.FrameDurationMS * time.Millisecond)
			}
		case evt := <-p.events:
			if evt.close {
				clearPending("playback closed")
				stopSpeaking()
				return
			}
			if evt.reset {
				queuedSamples := len(pending)
				reason := strings.TrimSpace(evt.resetReason)
				if reason == "" {
					reason = "playback interrupted"
				}
				clearPending(reason)
				if queuedSamples == 0 && p.trace != nil {
					p.trace.RecordPlaybackInterruptClear(reason, 0)
				}
				stopSpeaking()
				continue
			}
			pending = append(pending, liveaudio.PCMBytesToInt16(evt.audio)...)
			p.setState(len(pending), speaking, time.Time{})
			if p.trace != nil {
				p.trace.RecordPlaybackEnqueue(len(evt.audio), len(pending))
			}
			scheduleNextPlaybackFrame(0)
		}
	}
}

func liveSessionKey(guildID, voiceChannelID string) string {
	return guildID + ":" + voiceChannelID
}

func (s *Service) svcStore() *knowledge.Store {
	return s.store
}
