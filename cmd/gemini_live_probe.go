package cmd

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/spf13/cobra"
	"google.golang.org/genai"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/liveaudio"
)

var (
	geminiLiveProbeProvider     string
	geminiLiveProbeModel        string
	geminiLiveProbeVoice        string
	geminiLiveProbePrompt       string
	geminiLiveProbeChunkMS      int
	geminiLiveProbeEndWaitS     int
	geminiLiveProbeOutputWav    string
	geminiLiveProbeTailSilenceMS int
	geminiLiveProbeWithText     bool
	geminiLiveProbeTranscribe   bool
)

var geminiLiveProbeCmd = &cobra.Command{
	Use:   "gemini-live-test <wav-file>",
	Short: "Send a WAV file straight into Gemini Live and record the audio reply (bypasses Discord and the geminilive wrapper)",
	Args:  cobra.ExactArgs(1),
	RunE:  runGeminiLiveProbe,
}

func init() {
	geminiLiveProbeCmd.Flags().StringVar(&geminiLiveProbeProvider, "provider", "gemini-live", "Gemini Live provider name from config (used to derive project/location)")
	geminiLiveProbeCmd.Flags().StringVar(&geminiLiveProbeModel, "model", "", "Override model id (default: provider's configured model)")
	geminiLiveProbeCmd.Flags().StringVar(&geminiLiveProbeVoice, "voice", "", "Override voice name (optional)")
	geminiLiveProbeCmd.Flags().StringVar(&geminiLiveProbePrompt, "prompt", "", "Optional system prompt")
	geminiLiveProbeCmd.Flags().IntVar(&geminiLiveProbeChunkMS, "chunk-ms", 20, "Milliseconds per audio chunk sent to Gemini")
	geminiLiveProbeCmd.Flags().IntVar(&geminiLiveProbeEndWaitS, "end-wait-seconds", 30, "Seconds to wait for Gemini to finish after audio is streamed in")
	geminiLiveProbeCmd.Flags().StringVar(&geminiLiveProbeOutputWav, "output", "", "Path for the response WAV (default: alongside input)")
	geminiLiveProbeCmd.Flags().IntVar(&geminiLiveProbeTailSilenceMS, "tail-silence-ms", 1500, "Milliseconds of trailing silence appended before audioStreamEnd so VAD can commit end-of-speech")
	geminiLiveProbeCmd.Flags().BoolVar(&geminiLiveProbeWithText, "with-text", false, "Request TEXT response modality instead of AUDIO (for a sanity test that the model responds at all)")
	geminiLiveProbeCmd.Flags().BoolVar(&geminiLiveProbeTranscribe, "transcribe", false, "Enable input+output audio transcription (known to silently swallow some events with native-audio models)")
	rootCmd.AddCommand(geminiLiveProbeCmd)
}

func runGeminiLiveProbe(cmd *cobra.Command, args []string) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, ok := config.FromContext(ctx)
	if !ok {
		return fmt.Errorf("config not found in command context")
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	_ = cfg

	providerCfg, err := agentpkg.LoadProviderConfig(geminiLiveProbeProvider)
	if err != nil {
		return fmt.Errorf("load provider %q: %w", geminiLiveProbeProvider, err)
	}
	if providerCfg.Type != agentpkg.ProviderTypeVertex {
		return fmt.Errorf("provider %q must be a vertex provider", geminiLiveProbeProvider)
	}
	model := strings.TrimSpace(geminiLiveProbeModel)
	if model == "" {
		model = strings.TrimSpace(providerCfg.Model)
	}
	if model == "" {
		return fmt.Errorf("no model configured on provider %q", geminiLiveProbeProvider)
	}
	location := strings.TrimSpace(providerCfg.Location)
	if location == "" {
		location = "us-central1"
	}

	inputPath := args[0]
	samples, inputRate, err := readWavInt16Mono(inputPath)
	if err != nil {
		return fmt.Errorf("read input wav: %w", err)
	}
	if inputRate != liveaudio.GeminiInputSampleRate {
		samples = liveaudio.ResampleLinearInt16(samples, inputRate, liveaudio.GeminiInputSampleRate)
	}
	logger.Info("input loaded",
		"path", inputPath,
		"samples", len(samples),
		"duration_ms", len(samples)*1000/liveaudio.GeminiInputSampleRate,
	)

	if geminiLiveProbeTailSilenceMS > 0 {
		pad := make([]int16, liveaudio.GeminiInputSampleRate*geminiLiveProbeTailSilenceMS/1000)
		samples = append(samples, pad...)
		logger.Info("appended trailing silence", "ms", geminiLiveProbeTailSilenceMS, "total_samples", len(samples))
	}

	outputPath := strings.TrimSpace(geminiLiveProbeOutputWav)
	if outputPath == "" {
		base := strings.TrimSuffix(inputPath, filepath.Ext(inputPath))
		outputPath = base + "-gemini-response-24khz.wav"
	}

	headers := http.Header{}
	if ua := strings.TrimSpace(providerCfg.UserAgentOverride); ua != "" {
		headers.Set("User-Agent", ua)
	}
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  providerCfg.Project,
		Location: location,
		HTTPOptions: genai.HTTPOptions{
			APIVersion: "v1beta1",
			Headers:    headers,
		},
	})
	if err != nil {
		return fmt.Errorf("create genai client: %w", err)
	}

	liveCfg := &genai.LiveConnectConfig{}
	if geminiLiveProbeWithText {
		liveCfg.ResponseModalities = []genai.Modality{genai.ModalityText}
	} else {
		liveCfg.ResponseModalities = []genai.Modality{genai.ModalityAudio}
	}
	if geminiLiveProbeTranscribe {
		liveCfg.InputAudioTranscription = &genai.AudioTranscriptionConfig{}
		liveCfg.OutputAudioTranscription = &genai.AudioTranscriptionConfig{}
	}
	if voice := strings.TrimSpace(geminiLiveProbeVoice); voice != "" {
		liveCfg.SpeechConfig = &genai.SpeechConfig{
			VoiceConfig: &genai.VoiceConfig{
				PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{VoiceName: voice},
			},
		}
	}
	if prompt := strings.TrimSpace(geminiLiveProbePrompt); prompt != "" {
		liveCfg.SystemInstruction = genai.NewContentFromText(prompt, genai.RoleUser)
	}

	logger.Info("connecting to gemini live",
		"backend", "vertex",
		"project", providerCfg.Project,
		"location", location,
		"model", model,
		"response_modalities", fmt.Sprintf("%v", liveCfg.ResponseModalities),
		"transcription", geminiLiveProbeTranscribe,
	)

	session, err := client.Live.Connect(ctx, model, liveCfg)
	if err != nil {
		return fmt.Errorf("live connect: %w", err)
	}
	defer func() { _ = session.Close() }()

	var (
		audioMu    sync.Mutex
		audioBytes []byte
		doneCh     = make(chan struct{})
		firstSetup sync.Once
		readyCh    = make(chan struct{})
	)
	var textParts []string

	go func() {
		defer close(doneCh)
		for {
			msg, rerr := session.Receive()
			if rerr != nil {
				if !errors.Is(rerr, context.Canceled) {
					logger.Warn("receive error", "err", rerr)
				}
				return
			}
			if msg == nil {
				continue
			}
			if msg.SetupComplete != nil {
				firstSetup.Do(func() { close(readyCh) })
				logger.Info("server: setup_complete")
			}
			if msg.GoAway != nil {
				logger.Warn("server: go_away")
			}
			if sc := msg.ServerContent; sc != nil {
				if sc.Interrupted {
					logger.Info("server: interrupted")
				}
				if sc.InputTranscription != nil {
					logger.Info("server: input_transcription",
						"text", sc.InputTranscription.Text,
						"finished", sc.InputTranscription.Finished,
					)
				}
				if sc.OutputTranscription != nil {
					logger.Info("server: output_transcription",
						"text", sc.OutputTranscription.Text,
						"finished", sc.OutputTranscription.Finished,
					)
				}
				if sc.GenerationComplete {
					logger.Info("server: generation_complete")
				}
				if sc.TurnComplete {
					logger.Info("server: turn_complete")
				}
				if sc.ModelTurn != nil {
					for _, part := range sc.ModelTurn.Parts {
						if part == nil {
							continue
						}
						if part.Text != "" {
							logger.Info("server: model_turn text", "text", part.Text)
							textParts = append(textParts, part.Text)
						}
						if part.InlineData != nil && strings.HasPrefix(strings.ToLower(part.InlineData.MIMEType), "audio/pcm") && len(part.InlineData.Data) > 0 {
							audioMu.Lock()
							audioBytes = append(audioBytes, part.InlineData.Data...)
							audioMu.Unlock()
							logger.Info("server: model_turn audio_chunk", "bytes", len(part.InlineData.Data))
						}
					}
				}
			}
		}
	}()

	select {
	case <-readyCh:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(15 * time.Second):
		return fmt.Errorf("did not receive setup_complete")
	}

	chunkSamples := liveaudio.GeminiInputSampleRate * geminiLiveProbeChunkMS / 1000
	if chunkSamples <= 0 {
		chunkSamples = liveaudio.GeminiInputFrameSamples
	}
	chunkInterval := time.Duration(geminiLiveProbeChunkMS) * time.Millisecond
	logger.Info("streaming audio",
		"chunk_samples", chunkSamples,
		"chunk_interval_ms", geminiLiveProbeChunkMS,
	)
	for offset := 0; offset < len(samples); offset += chunkSamples {
		end := offset + chunkSamples
		if end > len(samples) {
			end = len(samples)
		}
		pcm := liveaudio.Int16ToPCMBytes(samples[offset:end])
		if err := session.SendRealtimeInput(genai.LiveRealtimeInput{
			Audio: &genai.Blob{
				Data:     pcm,
				MIMEType: "audio/pcm;rate=16000",
			},
		}); err != nil {
			return fmt.Errorf("send audio chunk: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(chunkInterval):
		}
	}
	if err := session.SendRealtimeInput(genai.LiveRealtimeInput{AudioStreamEnd: true}); err != nil {
		logger.Warn("send audioStreamEnd", "error", err)
	}
	logger.Info("audio sent, waiting for reply", "end_wait_seconds", geminiLiveProbeEndWaitS)

	waitCtx, waitCancel := context.WithTimeout(ctx, time.Duration(geminiLiveProbeEndWaitS)*time.Second)
	defer waitCancel()
	select {
	case <-waitCtx.Done():
	case <-doneCh:
	}

	audioMu.Lock()
	out := append([]byte(nil), audioBytes...)
	audioMu.Unlock()

	logger.Info("probe complete",
		"response_audio_bytes", len(out),
		"response_audio_duration_ms", func() int {
			if len(out) == 0 {
				return 0
			}
			return len(out) * 1000 / (liveaudio.GeminiOutputSampleRate * 2)
		}(),
		"response_text_parts", len(textParts),
	)
	if len(textParts) > 0 {
		fmt.Fprintln(os.Stdout, strings.Join(textParts, ""))
	}
	if len(out) == 0 {
		logger.Warn("no audio response; skipping output wav")
		return nil
	}
	if err := writeWavInt16Mono(outputPath, out, liveaudio.GeminiOutputSampleRate); err != nil {
		return fmt.Errorf("write response wav: %w", err)
	}
	logger.Info("response wav saved", "path", outputPath)
	return nil
}

func readWavInt16Mono(path string) ([]int16, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	dec := wav.NewDecoder(f)
	buf, err := dec.FullPCMBuffer()
	if err != nil {
		return nil, 0, err
	}
	if buf == nil || buf.Format == nil {
		return nil, 0, fmt.Errorf("no PCM data decoded from %s", path)
	}
	rate := buf.Format.SampleRate
	channels := buf.Format.NumChannels
	if channels <= 0 {
		channels = 1
	}
	sampleCount := len(buf.Data) / channels
	samples := make([]int16, sampleCount)
	for i := 0; i < sampleCount; i++ {
		if channels == 1 {
			samples[i] = clampInt16(buf.Data[i])
			continue
		}
		var sum int64
		for c := 0; c < channels; c++ {
			sum += int64(buf.Data[i*channels+c])
		}
		samples[i] = clampInt16(int(sum / int64(channels)))
	}
	return samples, rate, nil
}

func writeWavInt16Mono(path string, pcm []byte, sampleRate int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	samples := make([]int, len(pcm)/2)
	for i := range samples {
		samples[i] = int(int16(binary.LittleEndian.Uint16(pcm[i*2:])))
	}
	enc := wav.NewEncoder(f, sampleRate, 16, 1, 1)
	buf := &audio.IntBuffer{
		Format:         &audio.Format{NumChannels: 1, SampleRate: sampleRate},
		Data:           samples,
		SourceBitDepth: 16,
	}
	if err := enc.Write(buf); err != nil {
		_ = enc.Close()
		return err
	}
	return enc.Close()
}

func clampInt16(v int) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}
