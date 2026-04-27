package discordbot

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

// wavRecorder is a simple 16-bit mono PCM WAV writer used for debugging the
// live voice pipeline. Samples are buffered in memory and flushed on Close —
// sessions are short enough that this is fine.
type wavRecorder struct {
	path       string
	sampleRate int

	mu      sync.Mutex
	samples []int16
	closed  bool
}

func newWavRecorder(path string, sampleRate int) (*wavRecorder, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create wav dir: %w", err)
	}
	return &wavRecorder{path: path, sampleRate: sampleRate}, nil
}

func (r *wavRecorder) AppendInt16(samples []int16) {
	if r == nil || len(samples) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.samples = append(r.samples, samples...)
}

func (r *wavRecorder) AppendPCMBytes(data []byte) {
	if r == nil || len(data) < 2 {
		return
	}
	samples := make([]int16, len(data)/2)
	for i := range samples {
		samples[i] = int16(uint16(data[i*2]) | uint16(data[i*2+1])<<8)
	}
	r.AppendInt16(samples)
}

func (r *wavRecorder) Close() (string, int, error) {
	if r == nil {
		return "", 0, nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return r.path, len(r.samples), nil
	}
	r.closed = true
	samples := r.samples
	r.samples = nil
	r.mu.Unlock()

	if len(samples) == 0 {
		return r.path, 0, nil
	}
	f, err := os.Create(r.path)
	if err != nil {
		return r.path, 0, fmt.Errorf("create wav file: %w", err)
	}
	enc := wav.NewEncoder(f, r.sampleRate, 16, 1, 1)
	buf := &audio.IntBuffer{
		Format:         &audio.Format{NumChannels: 1, SampleRate: r.sampleRate},
		Data:           make([]int, len(samples)),
		SourceBitDepth: 16,
	}
	for i, s := range samples {
		buf.Data[i] = int(s)
	}
	if err := enc.Write(buf); err != nil {
		_ = enc.Close()
		_ = f.Close()
		return r.path, len(samples), fmt.Errorf("write wav: %w", err)
	}
	if err := enc.Close(); err != nil {
		_ = f.Close()
		return r.path, len(samples), fmt.Errorf("close wav encoder: %w", err)
	}
	if err := f.Close(); err != nil {
		return r.path, len(samples), fmt.Errorf("close wav file: %w", err)
	}
	return r.path, len(samples), nil
}

func liveDebugWavPaths(guildID, voiceChannelID string) (inbound, outbound string) {
	paths := liveDebugArtifactPaths(guildID, voiceChannelID)
	return paths.InboundWav, paths.OutboundWav
}
