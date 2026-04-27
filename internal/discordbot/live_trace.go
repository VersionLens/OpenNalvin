package discordbot

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"

	"github.com/versionlens/OpenNalvin/internal/liveaudio"
)

const (
	liveTraceSchemaVersion      = 1
	liveTraceDuplexSampleRate   = liveaudio.DiscordSampleRate
	liveTraceSpeechPeak         = 100
	liveTraceMergeGapMS         = 300
	liveTraceTranscriptWindowMS = 2000
)

type liveDebugPaths struct {
	Prefix      string
	InboundWav  string
	OutboundWav string
	TraceDir    string
}

type liveTraceMetadata struct {
	CallID                    string
	GuildID                   string
	VoiceChannelID            string
	TranscriptThreadID        string
	TranscriptParentChannelID string
	RootMessageID             string
	StatusMessageID           string
	ProviderName              string
	Participants              []liveTraceParticipant
}

type liveTraceParticipant struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
}

type liveTraceRecorder struct {
	mu sync.Mutex

	paths liveDebugPaths
	meta  liveTraceMetadata

	start  time.Time
	end    time.Time
	seq    int64
	file   *os.File
	closed bool
	err    error

	events       []map[string]any
	participants map[string]liveTraceParticipant
	segments     []liveTraceAudioSegment
	latencies    bool
}

type liveTraceAudioSegment struct {
	Channel     string
	StartMS     int64
	SampleRate  int
	Samples     []int16
	UserID      string
	SpeakerName string
}

type liveTraceTurn struct {
	Role                  string                      `json:"role"`
	SpeakerName           string                      `json:"speaker_name"`
	UserID                string                      `json:"user_id,omitempty"`
	StartMS               int64                       `json:"start_ms"`
	EndMS                 int64                       `json:"end_ms"`
	TextFinalMS           int64                       `json:"text_final_ms,omitempty"`
	Text                  string                      `json:"text,omitempty"`
	Interrupted           bool                        `json:"interrupted,omitempty"`
	AttributionConfidence float64                     `json:"attribution_confidence,omitempty"`
	CandidateSpeakers     []liveTraceSpeakerCandidate `json:"candidate_speakers,omitempty"`
}

type liveTraceSpeakerCandidate struct {
	UserID      string  `json:"user_id"`
	DisplayName string  `json:"display_name"`
	StartMS     int64   `json:"start_ms"`
	EndMS       int64   `json:"end_ms"`
	Confidence  float64 `json:"confidence"`
}

type liveTraceSpeechWindow struct {
	Role        string
	UserID      string
	SpeakerName string
	StartMS     int64
	EndMS       int64
}

func liveDebugArtifactPaths(guildID, voiceChannelID string) liveDebugPaths {
	timestamp := time.Now().UTC().Format("20060102-150405")
	base := filepath.Join(os.Getenv("HOME"), ".nalvin", "discord-live")
	prefix := fmt.Sprintf("%s-%s-%s", timestamp, guildID, voiceChannelID)
	return liveDebugPaths{
		Prefix:      prefix,
		InboundWav:  filepath.Join(base, prefix+"-inbound-16khz.wav"),
		OutboundWav: filepath.Join(base, prefix+"-outbound-24khz.wav"),
		TraceDir:    filepath.Join(base, prefix+"-trace"),
	}
}

func newLiveTraceRecorder(paths liveDebugPaths, meta liveTraceMetadata) (*liveTraceRecorder, error) {
	if err := os.MkdirAll(paths.TraceDir, 0o755); err != nil {
		return nil, fmt.Errorf("create live trace dir: %w", err)
	}
	eventsPath := filepath.Join(paths.TraceDir, "events.jsonl")
	file, err := os.Create(eventsPath)
	if err != nil {
		return nil, fmt.Errorf("create live trace events: %w", err)
	}
	r := &liveTraceRecorder{
		paths:        paths,
		meta:         meta,
		start:        time.Now(),
		file:         file,
		participants: map[string]liveTraceParticipant{},
	}
	for _, participant := range meta.Participants {
		r.setParticipantLocked(participant)
	}
	for _, participant := range meta.Participants {
		r.RecordParticipant("participant_join", participant, "", meta.VoiceChannelID)
	}
	if err := r.writeManifest(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return r, nil
}

func (r *liveTraceRecorder) RecordParticipant(kind string, participant liveTraceParticipant, fromChannelID, toChannelID string) {
	if r == nil || strings.TrimSpace(participant.UserID) == "" {
		return
	}
	fields := map[string]any{
		"user_id":      participant.UserID,
		"display_name": traceDisplayName(participant),
	}
	if fromChannelID != "" {
		fields["from_channel_id"] = fromChannelID
	}
	if toChannelID != "" {
		fields["to_channel_id"] = toChannelID
	}
	r.mu.Lock()
	r.setParticipantLocked(participant)
	r.mu.Unlock()
	r.record(kind, fields)
}

func (r *liveTraceRecorder) ParticipantName(userID string) string {
	if r == nil || strings.TrimSpace(userID) == "" {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return traceDisplayName(r.participants[userID])
}

func (r *liveTraceRecorder) RecordTransportState(state VoiceTransportState) {
	if r == nil {
		return
	}
	r.record("transport_state", map[string]any{"state": string(state)})
}

func (r *liveTraceRecorder) RecordPlaybackTransportRetry(state VoiceTransportState, queuedSamples int) {
	if r == nil {
		return
	}
	r.record("transport_state", map[string]any{
		"state":          string(state),
		"source":         "playback_retry",
		"queued_ms":      queuedSamples * 1000 / liveaudio.GeminiOutputSampleRate,
		"queued_samples": queuedSamples,
	})
}

func (r *liveTraceRecorder) RecordInboundAudioFrame(packet opusPacket, samples []int16, peak, rms int, sentToGemini bool) {
	if r == nil || len(samples) == 0 {
		return
	}
	start := packet.ReceivedAt
	if start.IsZero() {
		start = time.Now()
	}
	durationMS := len(samples) * 1000 / liveaudio.GeminiInputSampleRate
	userID := strings.TrimSpace(packet.UserID)
	speakerName := r.ParticipantName(userID)
	if speakerName == "" && userID != "" {
		speakerName = userID
	}
	quiet := peak < liveTraceSpeechPeak
	fields := map[string]any{
		"ssrc":           packet.SSRC,
		"sequence":       packet.Sequence,
		"timestamp":      packet.Timestamp,
		"user_id":        userID,
		"speaker_name":   speakerName,
		"samples":        len(samples),
		"duration_ms":    durationMS,
		"peak":           peak,
		"rms":            rms,
		"quiet":          quiet,
		"sent_to_gemini": sentToGemini,
	}
	r.recordAt("inbound_audio_frame", start, fields)
	r.addAudioSegment(liveTraceAudioSegment{
		Channel:     "inbound",
		StartMS:     r.msSinceStart(start),
		SampleRate:  liveaudio.GeminiInputSampleRate,
		Samples:     append([]int16(nil), samples...),
		UserID:      userID,
		SpeakerName: speakerName,
	})
}

func (r *liveTraceRecorder) RecordSilencePad(durationMS int) {
	if r == nil {
		return
	}
	r.record("silence_pad_sent", map[string]any{"duration_ms": durationMS})
}

func (r *liveTraceRecorder) RecordGeminiInputSendError(err error) {
	if r == nil || err == nil {
		return
	}
	r.record("gemini_input_send_error", map[string]any{"error": err.Error()})
}

func (r *liveTraceRecorder) RecordDecodeError(ssrc uint32, frameLen int, err error) {
	if r == nil || err == nil {
		return
	}
	r.record("decode_error", map[string]any{
		"ssrc":      ssrc,
		"frame_len": frameLen,
		"error":     err.Error(),
	})
}

func (r *liveTraceRecorder) RecordGeminiStatus(status string) {
	if r == nil || strings.TrimSpace(status) == "" {
		return
	}
	r.record("gemini_status", map[string]any{"status": strings.TrimSpace(status)})
}

func (r *liveTraceRecorder) RecordGeminiInterrupted() {
	if r == nil {
		return
	}
	r.record("gemini_interrupted", nil)
}

func (r *liveTraceRecorder) RecordLocalSpeechStart(at time.Time, packet opusPacket, peak, rms int) {
	if r == nil {
		return
	}
	userID := strings.TrimSpace(packet.UserID)
	speakerName := r.ParticipantName(userID)
	if speakerName == "" && userID != "" {
		speakerName = userID
	}
	r.recordAt("local_speech_start", at, map[string]any{
		"ssrc":         packet.SSRC,
		"user_id":      userID,
		"speaker_name": speakerName,
		"peak":         peak,
		"rms":          rms,
	})
}

func (r *liveTraceRecorder) RecordLocalSpeechEnd(at time.Time, durationMS int) {
	if r == nil {
		return
	}
	r.recordAt("local_speech_end", at, map[string]any{"duration_ms": durationMS})
}

func (r *liveTraceRecorder) RecordLocalBargeIn(at time.Time, pendingMS int, activeRecently bool) {
	if r == nil {
		return
	}
	r.recordAt("local_barge_in", at, map[string]any{
		"pending_ms":      pendingMS,
		"active_recently": activeRecently,
	})
}

func (r *liveTraceRecorder) RecordAudioStreamEndSent(paddedMS int) {
	if r == nil {
		return
	}
	r.record("audio_stream_end_sent", map[string]any{"silence_padded_ms": paddedMS})
}

func (r *liveTraceRecorder) RecordTranscript(role, text string, final bool) {
	if r == nil || strings.TrimSpace(text) == "" {
		return
	}
	kind := "transcript_partial"
	if final {
		kind = "transcript_final"
	}
	r.record(kind, map[string]any{
		"role": role,
		"text": strings.TrimSpace(text),
	})
}

func (r *liveTraceRecorder) RecordGeminiAudioChunk(data []byte) {
	if r == nil || len(data) < 2 {
		return
	}
	samples := len(data) / 2
	r.record("gemini_audio_chunk", map[string]any{
		"bytes":       len(data),
		"samples":     samples,
		"duration_ms": samples * 1000 / liveaudio.GeminiOutputSampleRate,
	})
}

func (r *liveTraceRecorder) RecordGeminiAudioSuppressed(data []byte) {
	if r == nil || len(data) < 2 {
		return
	}
	samples := len(data) / 2
	r.record("gemini_audio_suppressed", map[string]any{
		"bytes":       len(data),
		"samples":     samples,
		"duration_ms": samples * 1000 / liveaudio.GeminiOutputSampleRate,
	})
}

func (r *liveTraceRecorder) RecordSessionHandle(handle string) {
	if r == nil || strings.TrimSpace(handle) == "" {
		return
	}
	r.record("session_handle", map[string]any{"has_handle": true})
}

func (r *liveTraceRecorder) RecordPlaybackEnqueue(audioBytes, queuedSamples int) {
	if r == nil {
		return
	}
	r.record("playback_enqueue", map[string]any{
		"bytes":          audioBytes,
		"queued_ms":      queuedSamples * 1000 / liveaudio.GeminiOutputSampleRate,
		"queued_samples": queuedSamples,
	})
}

func (r *liveTraceRecorder) RecordPlaybackInterruptClear(reason string, queuedSamples int) {
	if r == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "playback interrupted"
	}
	r.record("playback_interrupt_clear", map[string]any{
		"reason":            reason,
		"queued_ms_dropped": queuedSamples * 1000 / liveaudio.GeminiOutputSampleRate,
		"queued_samples":    queuedSamples,
	})
}

func (r *liveTraceRecorder) RecordPlaybackDrop(reason string, queuedSamples int) {
	if r == nil {
		return
	}
	r.record("playback_drop", map[string]any{
		"reason":            reason,
		"queued_ms_dropped": queuedSamples * 1000 / liveaudio.GeminiOutputSampleRate,
		"queued_samples":    queuedSamples,
	})
}

func (r *liveTraceRecorder) RecordSpeakingStart() {
	if r == nil {
		return
	}
	r.record("speaking_start", map[string]any{"speaker_name": "gemini"})
}

func (r *liveTraceRecorder) RecordSpeakingStop(silenceFrames int) {
	if r == nil {
		return
	}
	r.record("speaking_stop", map[string]any{
		"speaker_name":   "gemini",
		"silence_frames": silenceFrames,
	})
}

func (r *liveTraceRecorder) RecordPlaybackFrameSent(samples48 []int16, opusBytes, queuedBeforeSamples, queuedAfterSamples int) {
	if r == nil || len(samples48) == 0 {
		return
	}
	now := time.Now()
	durationMS := len(samples48) * 1000 / liveTraceDuplexSampleRate
	r.recordAt("playback_frame_sent", now, map[string]any{
		"speaker_name":          "gemini",
		"samples":               len(samples48),
		"duration_ms":           durationMS,
		"opus_bytes":            opusBytes,
		"queued_ms_before_send": queuedBeforeSamples * 1000 / liveaudio.GeminiOutputSampleRate,
		"queued_ms_after_send":  queuedAfterSamples * 1000 / liveaudio.GeminiOutputSampleRate,
	})
	r.addAudioSegment(liveTraceAudioSegment{
		Channel:     "assistant",
		StartMS:     r.msSinceStart(now),
		SampleRate:  liveTraceDuplexSampleRate,
		Samples:     append([]int16(nil), samples48...),
		SpeakerName: "gemini",
	})
}

func (r *liveTraceRecorder) RecordSendError(phase string, err error) {
	if r == nil || err == nil {
		return
	}
	r.record("send_error", map[string]any{
		"phase": phase,
		"error": err.Error(),
	})
}

func (r *liveTraceRecorder) Close(reason string) (string, error) {
	if r == nil {
		return "", nil
	}
	if strings.TrimSpace(reason) == "" {
		reason = "ended"
	}
	r.recordLatencySummaries()
	r.record("trace_closed", map[string]any{"reason": reason})

	r.mu.Lock()
	if r.closed {
		dir := r.paths.TraceDir
		err := r.err
		r.mu.Unlock()
		return dir, err
	}
	r.closed = true
	r.end = time.Now()
	if r.file != nil {
		if err := r.file.Close(); err != nil && r.err == nil {
			r.err = err
		}
		r.file = nil
	}
	events := cloneTraceEvents(r.events)
	participants := cloneTraceParticipants(r.participants)
	segments := cloneTraceSegments(r.segments)
	r.mu.Unlock()

	turns := synthesizeLiveTraceTurns(events)
	if err := writeJSONFile(filepath.Join(r.paths.TraceDir, "turns.json"), turns); err != nil && r.err == nil {
		r.err = err
	}
	if err := os.WriteFile(filepath.Join(r.paths.TraceDir, "timeline.md"), []byte(renderLiveTraceTimeline(turns, events)), 0o644); err != nil && r.err == nil {
		r.err = err
	}
	if err := os.WriteFile(filepath.Join(r.paths.TraceDir, "timeline-labels.txt"), []byte(renderLiveTraceLabels(turns, events)), 0o644); err != nil && r.err == nil {
		r.err = err
	}
	if err := renderLiveTraceDuplexWAV(filepath.Join(r.paths.TraceDir, "duplex-48khz.wav"), segments); err != nil && r.err == nil {
		r.err = err
	}
	if err := r.writeManifestWithParticipants(participants); err != nil && r.err == nil {
		r.err = err
	}
	return r.paths.TraceDir, r.err
}

func (r *liveTraceRecorder) record(kind string, fields map[string]any) {
	r.recordAt(kind, time.Now(), fields)
}

func (r *liveTraceRecorder) recordLatencySummaries() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed || r.latencies {
		r.mu.Unlock()
		return
	}
	r.latencies = true
	events := cloneTraceEvents(r.events)
	r.mu.Unlock()

	for _, event := range events {
		if traceString(event, "kind") != "local_speech_end" {
			continue
		}
		endMS := traceInt64(event, "t_ms")
		fields := map[string]any{
			"speech_end_ms": endMS,
		}
		if next, ok := nextEventMS(events, endMS, "gemini_audio_chunk", ""); ok {
			fields["speech_end_to_gemini_audio_ms"] = next - endMS
		}
		if next, ok := nextEventMS(events, endMS, "playback_frame_sent", ""); ok {
			fields["speech_end_to_playback_ms"] = next - endMS
		}
		if next, ok := nextEventMS(events, endMS, "transcript_final", "user"); ok {
			fields["speech_end_to_user_transcript_final_ms"] = next - endMS
		}
		if len(fields) > 1 {
			r.record("latency_summary", fields)
		}
	}
}

func (r *liveTraceRecorder) recordAt(kind string, at time.Time, fields map[string]any) {
	if r == nil || strings.TrimSpace(kind) == "" {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}
	event := map[string]any{
		"seq":          int64(0),
		"t_ms":         r.msSinceStart(at),
		"at_unix_nano": at.UnixNano(),
		"kind":         kind,
	}
	for key, value := range fields {
		event[key] = value
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.seq++
	event["seq"] = r.seq
	r.events = append(r.events, event)
	if r.file != nil {
		line, err := json.Marshal(event)
		if err == nil {
			_, err = r.file.Write(append(line, '\n'))
		}
		if err != nil && r.err == nil {
			r.err = err
		}
	}
}

func (r *liveTraceRecorder) addAudioSegment(segment liveTraceAudioSegment) {
	if r == nil || len(segment.Samples) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.segments = append(r.segments, segment)
}

func (r *liveTraceRecorder) msSinceStart(at time.Time) int64 {
	if r == nil || r.start.IsZero() || at.IsZero() {
		return 0
	}
	ms := at.Sub(r.start).Milliseconds()
	if ms < 0 {
		return 0
	}
	return ms
}

func (r *liveTraceRecorder) setParticipantLocked(participant liveTraceParticipant) {
	participant.UserID = strings.TrimSpace(participant.UserID)
	if participant.UserID == "" {
		return
	}
	participant.DisplayName = strings.TrimSpace(participant.DisplayName)
	if participant.DisplayName == "" {
		participant.DisplayName = participant.UserID
	}
	r.participants[participant.UserID] = participant
}

func (r *liveTraceRecorder) writeManifest() error {
	r.mu.Lock()
	participants := cloneTraceParticipants(r.participants)
	r.mu.Unlock()
	return r.writeManifestWithParticipants(participants)
}

func (r *liveTraceRecorder) writeManifestWithParticipants(participants map[string]liveTraceParticipant) error {
	if r == nil {
		return nil
	}
	endedAt := ""
	if !r.end.IsZero() {
		endedAt = r.end.UTC().Format(time.RFC3339Nano)
	}
	manifest := map[string]any{
		"schema_version": liveTraceSchemaVersion,
		"started_at":     r.start.UTC().Format(time.RFC3339Nano),
		"ended_at":       endedAt,
		"call": map[string]any{
			"id":                           r.meta.CallID,
			"guild_id":                     r.meta.GuildID,
			"voice_channel_id":             r.meta.VoiceChannelID,
			"transcript_thread_id":         r.meta.TranscriptThreadID,
			"transcript_parent_channel_id": r.meta.TranscriptParentChannelID,
			"root_message_id":              r.meta.RootMessageID,
			"status_message_id":            r.meta.StatusMessageID,
			"provider_name":                r.meta.ProviderName,
		},
		"sample_rates": map[string]int{
			"inbound_wav":  liveaudio.GeminiInputSampleRate,
			"outbound_wav": liveaudio.GeminiOutputSampleRate,
			"duplex_wav":   liveTraceDuplexSampleRate,
		},
		"files": map[string]string{
			"events":       filepath.Join(r.paths.TraceDir, "events.jsonl"),
			"turns":        filepath.Join(r.paths.TraceDir, "turns.json"),
			"timeline":     filepath.Join(r.paths.TraceDir, "timeline.md"),
			"labels":       filepath.Join(r.paths.TraceDir, "timeline-labels.txt"),
			"duplex_wav":   filepath.Join(r.paths.TraceDir, "duplex-48khz.wav"),
			"inbound_wav":  r.paths.InboundWav,
			"outbound_wav": r.paths.OutboundWav,
		},
		"participants": participants,
	}
	return writeJSONFile(filepath.Join(r.paths.TraceDir, "manifest.json"), manifest)
}

func synthesizeLiveTraceTurns(events []map[string]any) []liveTraceTurn {
	humanWindows := synthesizeSpeechWindows(events, "inbound_audio_frame", "user")
	assistantWindows := synthesizeSpeechWindows(events, "playback_frame_sent", "assistant")
	usedHumanWindows := make([]bool, len(humanWindows))
	usedAssistantWindows := make([]bool, len(assistantWindows))
	var turns []liveTraceTurn
	for _, event := range events {
		if traceString(event, "kind") != "transcript_final" {
			continue
		}
		role := traceString(event, "role")
		text := traceString(event, "text")
		tms := traceInt64(event, "t_ms")
		switch role {
		case "assistant":
			window, ok := nearestWindowByDistance(assistantWindows, tms, 5000)
			turn := liveTraceTurn{
				Role:        "assistant",
				SpeakerName: "gemini",
				StartMS:     tms,
				EndMS:       tms,
				TextFinalMS: tms,
				Text:        text,
			}
			if ok {
				markSpeechWindowUsed(assistantWindows, usedAssistantWindows, window)
				turn.StartMS = window.StartMS
				turn.EndMS = window.EndMS
				startMS := minInt64(window.StartMS, tms)
				endMS := maxInt64(window.EndMS, tms)
				turn.Interrupted = hasEventBetween(events, "gemini_interrupted", startMS, endMS) ||
					hasEventBetween(events, "playback_interrupt_clear", startMS, endMS)
			}
			turns = append(turns, turn)
		default:
			turn := liveTraceTurn{
				Role:        "user",
				SpeakerName: "unknown",
				StartMS:     tms,
				EndMS:       tms,
				TextFinalMS: tms,
				Text:        text,
			}
			candidates := candidateWindows(humanWindows, tms, liveTraceTranscriptWindowMS)
			if len(candidates) == 1 {
				candidate := candidates[0]
				markSpeechWindowUsed(humanWindows, usedHumanWindows, candidate)
				turn.UserID = candidate.UserID
				turn.SpeakerName = candidate.SpeakerName
				turn.StartMS = candidate.StartMS
				turn.EndMS = candidate.EndMS
				turn.AttributionConfidence = 0.9
			} else if len(candidates) > 1 {
				turn.SpeakerName = "unknown/multiple"
				turn.CandidateSpeakers = make([]liveTraceSpeakerCandidate, 0, len(candidates))
				for _, candidate := range candidates {
					markSpeechWindowUsed(humanWindows, usedHumanWindows, candidate)
					turn.CandidateSpeakers = append(turn.CandidateSpeakers, liveTraceSpeakerCandidate{
						UserID:      candidate.UserID,
						DisplayName: candidate.SpeakerName,
						StartMS:     candidate.StartMS,
						EndMS:       candidate.EndMS,
						Confidence:  0.4,
					})
				}
				turn.StartMS = candidates[0].StartMS
				turn.EndMS = candidates[0].EndMS
				for _, candidate := range candidates[1:] {
					if candidate.StartMS < turn.StartMS {
						turn.StartMS = candidate.StartMS
					}
					if candidate.EndMS > turn.EndMS {
						turn.EndMS = candidate.EndMS
					}
				}
				turn.AttributionConfidence = 0.4
			}
			turns = append(turns, turn)
		}
	}
	for i, window := range humanWindows {
		if usedHumanWindows[i] {
			continue
		}
		turns = append(turns, liveTraceTurn{
			Role:        "user",
			UserID:      window.UserID,
			SpeakerName: window.SpeakerName,
			StartMS:     window.StartMS,
			EndMS:       window.EndMS,
		})
	}
	for i, window := range assistantWindows {
		if usedAssistantWindows[i] {
			continue
		}
		turns = append(turns, liveTraceTurn{
			Role:        "assistant",
			SpeakerName: "gemini",
			StartMS:     window.StartMS,
			EndMS:       window.EndMS,
			Interrupted: hasEventBetween(events, "gemini_interrupted", window.StartMS, window.EndMS) ||
				hasEventBetween(events, "playback_interrupt_clear", window.StartMS, window.EndMS),
		})
	}
	sort.SliceStable(turns, func(i, j int) bool {
		if turns[i].StartMS == turns[j].StartMS {
			return turns[i].EndMS < turns[j].EndMS
		}
		return turns[i].StartMS < turns[j].StartMS
	})
	return turns
}

func synthesizeSpeechWindows(events []map[string]any, kind, role string) []liveTraceSpeechWindow {
	var windows []liveTraceSpeechWindow
	for _, event := range events {
		if traceString(event, "kind") != kind {
			continue
		}
		if kind == "inbound_audio_frame" && traceBool(event, "quiet") {
			continue
		}
		start := traceInt64(event, "t_ms")
		duration := traceInt64(event, "duration_ms")
		if duration <= 0 {
			duration = int64(liveaudio.FrameDurationMS)
		}
		userID := traceString(event, "user_id")
		speakerName := traceString(event, "speaker_name")
		if speakerName == "" {
			if role == "assistant" {
				speakerName = "gemini"
			} else if userID != "" {
				speakerName = userID
			} else {
				speakerName = "unknown"
			}
		}
		current := liveTraceSpeechWindow{
			Role:        role,
			UserID:      userID,
			SpeakerName: speakerName,
			StartMS:     start,
			EndMS:       start + duration,
		}
		if len(windows) > 0 {
			last := &windows[len(windows)-1]
			if last.Role == current.Role && last.UserID == current.UserID && last.SpeakerName == current.SpeakerName && current.StartMS-last.EndMS <= liveTraceMergeGapMS {
				if current.EndMS > last.EndMS {
					last.EndMS = current.EndMS
				}
				continue
			}
		}
		windows = append(windows, current)
	}
	return windows
}

func candidateWindows(windows []liveTraceSpeechWindow, transcriptMS, maxGapMS int64) []liveTraceSpeechWindow {
	var out []liveTraceSpeechWindow
	for _, window := range windows {
		if window.StartMS <= transcriptMS && transcriptMS <= window.EndMS+maxGapMS {
			out = append(out, window)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		di := candidateDistance(out[i], transcriptMS)
		dj := candidateDistance(out[j], transcriptMS)
		return di < dj
	})
	if len(out) <= 1 {
		return out
	}
	best := out[0]
	bestDistance := candidateDistance(best, transcriptMS)
	filtered := []liveTraceSpeechWindow{best}
	for _, candidate := range out[1:] {
		if windowsOverlap(best, candidate) || candidateDistance(candidate, transcriptMS)-bestDistance <= liveTraceMergeGapMS {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func nearestWindow(windows []liveTraceSpeechWindow, tms, maxGapMS int64) (liveTraceSpeechWindow, bool) {
	candidates := candidateWindows(windows, tms, maxGapMS)
	if len(candidates) == 0 {
		return liveTraceSpeechWindow{}, false
	}
	return candidates[0], true
}

func nearestWindowByDistance(windows []liveTraceSpeechWindow, tms, maxGapMS int64) (liveTraceSpeechWindow, bool) {
	var best liveTraceSpeechWindow
	var bestDistance int64
	found := false
	for _, window := range windows {
		distance := intervalDistance(window, tms)
		if distance > maxGapMS {
			continue
		}
		if !found || distance < bestDistance {
			best = window
			bestDistance = distance
			found = true
		}
	}
	if !found {
		return liveTraceSpeechWindow{}, false
	}
	return best, true
}

func markSpeechWindowUsed(windows []liveTraceSpeechWindow, used []bool, target liveTraceSpeechWindow) {
	for i, window := range windows {
		if used[i] || window != target {
			continue
		}
		used[i] = true
		return
	}
}

func renderLiveTraceTimeline(turns []liveTraceTurn, events []map[string]any) string {
	var b strings.Builder
	b.WriteString("# Discord Gemini Live Trace\n\n")
	if len(turns) > 0 {
		b.WriteString("## Turns\n\n")
		for _, turn := range turns {
			label := turn.SpeakerName
			if label == "" {
				label = turn.Role
			}
			fmt.Fprintf(&b, "- %s - %s  %s", formatTraceMS(turn.StartMS), formatTraceMS(turn.EndMS), label)
			if turn.Text != "" {
				fmt.Fprintf(&b, ": %q", turn.Text)
			}
			if turn.Interrupted {
				b.WriteString(" [interrupted]")
			}
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	b.WriteString("## Events\n\n")
	for _, event := range events {
		kind := traceString(event, "kind")
		switch kind {
		case "gemini_interrupted", "local_speech_start", "local_speech_end", "local_barge_in", "audio_stream_end_sent", "gemini_audio_suppressed", "latency_summary", "playback_interrupt_clear", "playback_drop", "speaking_start", "speaking_stop", "transcript_final", "transport_state":
			fmt.Fprintf(&b, "- %s  %s", formatTraceMS(traceInt64(event, "t_ms")), kind)
			if role := traceString(event, "role"); role != "" {
				fmt.Fprintf(&b, " role=%s", role)
			}
			if text := traceString(event, "text"); text != "" {
				fmt.Fprintf(&b, " text=%q", text)
			}
			if status := traceString(event, "state"); status != "" {
				fmt.Fprintf(&b, " state=%s", status)
			}
			if reason := traceString(event, "reason"); reason != "" {
				fmt.Fprintf(&b, " reason=%q", reason)
			}
			if pending := traceInt64(event, "pending_ms"); pending > 0 {
				fmt.Fprintf(&b, " pending_ms=%d", pending)
			}
			if latency := traceInt64(event, "speech_end_to_playback_ms"); latency > 0 {
				fmt.Fprintf(&b, " speech_end_to_playback_ms=%d", latency)
			}
			if latency := traceInt64(event, "speech_end_to_gemini_audio_ms"); latency > 0 {
				fmt.Fprintf(&b, " speech_end_to_gemini_audio_ms=%d", latency)
			}
			if latency := traceInt64(event, "speech_end_to_user_transcript_final_ms"); latency > 0 {
				fmt.Fprintf(&b, " speech_end_to_user_transcript_final_ms=%d", latency)
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func renderLiveTraceLabels(turns []liveTraceTurn, events []map[string]any) string {
	var b strings.Builder
	for _, turn := range turns {
		label := turn.SpeakerName
		if label == "" {
			label = turn.Role
		}
		if turn.Text != "" {
			label += ": " + strings.ReplaceAll(turn.Text, "\t", " ")
		}
		fmt.Fprintf(&b, "%.6f\t%.6f\t%s\n", float64(turn.StartMS)/1000, float64(turn.EndMS)/1000, label)
		if turn.TextFinalMS > 0 {
			fmt.Fprintf(&b, "%.6f\t%.6f\t%s final transcript\n", float64(turn.TextFinalMS)/1000, float64(turn.TextFinalMS)/1000, turn.Role)
		}
	}
	for _, event := range events {
		kind := traceString(event, "kind")
		if kind != "gemini_interrupted" && kind != "local_barge_in" && kind != "playback_interrupt_clear" && kind != "playback_drop" {
			continue
		}
		t := float64(traceInt64(event, "t_ms")) / 1000
		fmt.Fprintf(&b, "%.6f\t%.6f\t%s\n", t, t, kind)
	}
	return b.String()
}

func renderLiveTraceDuplexWAV(path string, segments []liveTraceAudioSegment) error {
	maxSamples := 0
	type renderedSegment struct {
		channel string
		offset  int
		samples []int16
	}
	rendered := make([]renderedSegment, 0, len(segments))
	for _, segment := range segments {
		samples := segment.Samples
		if segment.SampleRate != liveTraceDuplexSampleRate {
			samples = liveaudio.ResampleLinearInt16(samples, segment.SampleRate, liveTraceDuplexSampleRate)
		} else {
			samples = append([]int16(nil), samples...)
		}
		offset := int(math.Round(float64(segment.StartMS) * float64(liveTraceDuplexSampleRate) / 1000))
		if offset < 0 {
			offset = 0
		}
		if offset+len(samples) > maxSamples {
			maxSamples = offset + len(samples)
		}
		rendered = append(rendered, renderedSegment{channel: segment.Channel, offset: offset, samples: samples})
	}
	if maxSamples == 0 {
		maxSamples = 1
	}
	left := make([]int32, maxSamples)
	right := make([]int32, maxSamples)
	for _, segment := range rendered {
		target := left
		if segment.channel == "assistant" {
			target = right
		}
		for i, sample := range segment.samples {
			idx := segment.offset + i
			if idx >= len(target) {
				break
			}
			target[idx] += int32(sample)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := wav.NewEncoder(f, liveTraceDuplexSampleRate, 16, 2, 1)
	data := make([]int, maxSamples*2)
	for i := 0; i < maxSamples; i++ {
		data[i*2] = int(clampInt32ToInt16(left[i]))
		data[i*2+1] = int(clampInt32ToInt16(right[i]))
	}
	buf := &audio.IntBuffer{
		Format:         &audio.Format{NumChannels: 2, SampleRate: liveTraceDuplexSampleRate},
		Data:           data,
		SourceBitDepth: 16,
	}
	if err := enc.Write(buf); err != nil {
		_ = enc.Close()
		_ = f.Close()
		return err
	}
	if err := enc.Close(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func liveTracePeakRMS(samples []int16) (int, int) {
	if len(samples) == 0 {
		return 0, 0
	}
	var peak int
	var sumSquares float64
	for _, sample := range samples {
		value := int(sample)
		if value < 0 {
			value = -value
		}
		if value > peak {
			peak = value
		}
		sumSquares += float64(sample) * float64(sample)
	}
	return peak, int(math.Round(math.Sqrt(sumSquares / float64(len(samples)))))
}

func traceDisplayName(participant liveTraceParticipant) string {
	name := strings.TrimSpace(participant.DisplayName)
	if name != "" {
		return name
	}
	return strings.TrimSpace(participant.UserID)
}

func cloneTraceEvents(events []map[string]any) []map[string]any {
	out := make([]map[string]any, len(events))
	for i, event := range events {
		copyEvent := make(map[string]any, len(event))
		for key, value := range event {
			copyEvent[key] = value
		}
		out[i] = copyEvent
	}
	return out
}

func cloneTraceParticipants(participants map[string]liveTraceParticipant) map[string]liveTraceParticipant {
	out := make(map[string]liveTraceParticipant, len(participants))
	for key, value := range participants {
		out[key] = value
	}
	return out
}

func cloneTraceSegments(segments []liveTraceAudioSegment) []liveTraceAudioSegment {
	out := make([]liveTraceAudioSegment, len(segments))
	for i, segment := range segments {
		out[i] = segment
		out[i].Samples = append([]int16(nil), segment.Samples...)
	}
	return out
}

func traceString(event map[string]any, key string) string {
	if value, ok := event[key].(string); ok {
		return value
	}
	return ""
}

func traceBool(event map[string]any, key string) bool {
	if value, ok := event[key].(bool); ok {
		return value
	}
	return false
}

func traceInt64(event map[string]any, key string) int64 {
	switch value := event[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		n, _ := value.Int64()
		return n
	default:
		return 0
	}
}

func hasEventBetween(events []map[string]any, kind string, startMS, endMS int64) bool {
	for _, event := range events {
		if traceString(event, "kind") != kind {
			continue
		}
		tms := traceInt64(event, "t_ms")
		if startMS <= tms && tms <= endMS {
			return true
		}
	}
	return false
}

func nextEventMS(events []map[string]any, afterMS int64, kind, role string) (int64, bool) {
	var best int64
	var found bool
	for _, event := range events {
		if traceString(event, "kind") != kind {
			continue
		}
		if role != "" && traceString(event, "role") != role {
			continue
		}
		tms := traceInt64(event, "t_ms")
		if tms < afterMS {
			continue
		}
		if !found || tms < best {
			best = tms
			found = true
		}
	}
	return best, found
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func formatTraceMS(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	minutes := ms / 60000
	seconds := (ms % 60000) / 1000
	millis := ms % 1000
	return fmt.Sprintf("%02d:%02d.%03d", minutes, seconds, millis)
}

func clampInt32ToInt16(value int32) int16 {
	if value > math.MaxInt16 {
		return math.MaxInt16
	}
	if value < math.MinInt16 {
		return math.MinInt16
	}
	return int16(value)
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func candidateDistance(window liveTraceSpeechWindow, transcriptMS int64) int64 {
	if window.StartMS <= transcriptMS && transcriptMS <= window.EndMS {
		return 0
	}
	return absInt64(transcriptMS - window.EndMS)
}

func intervalDistance(window liveTraceSpeechWindow, tms int64) int64 {
	switch {
	case tms < window.StartMS:
		return window.StartMS - tms
	case tms > window.EndMS:
		return tms - window.EndMS
	default:
		return 0
	}
}

func windowsOverlap(a, b liveTraceSpeechWindow) bool {
	return a.StartMS <= b.EndMS && b.StartMS <= a.EndMS
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
