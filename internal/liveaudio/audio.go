package liveaudio

import (
	"encoding/binary"
	"math"
)

const (
	DiscordSampleRate        = 48000
	GeminiInputSampleRate    = 16000
	GeminiOutputSampleRate   = 24000
	FrameDurationMS          = 20
	DiscordFrameSamples      = DiscordSampleRate * FrameDurationMS / 1000
	GeminiInputFrameSamples  = GeminiInputSampleRate * FrameDurationMS / 1000
	GeminiOutputFrameSamples = GeminiOutputSampleRate * FrameDurationMS / 1000
)

func PCMBytesToInt16(data []byte) []int16 {
	if len(data) < 2 {
		return nil
	}
	out := make([]int16, len(data)/2)
	for i := 0; i < len(out); i++ {
		out[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}
	return out
}

func Int16ToPCMBytes(samples []int16) []byte {
	if len(samples) == 0 {
		return nil
	}
	out := make([]byte, len(samples)*2)
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(sample))
	}
	return out
}

func StereoToMono(samples []int16) []int16 {
	if len(samples) == 0 {
		return nil
	}
	if len(samples)%2 != 0 {
		samples = samples[:len(samples)-1]
	}
	out := make([]int16, len(samples)/2)
	for i := 0; i < len(out); i++ {
		left := int32(samples[i*2])
		right := int32(samples[i*2+1])
		out[i] = int16((left + right) / 2)
	}
	return out
}

func MonoToStereo(samples []int16) []int16 {
	if len(samples) == 0 {
		return nil
	}
	out := make([]int16, len(samples)*2)
	for i, sample := range samples {
		out[i*2] = sample
		out[i*2+1] = sample
	}
	return out
}

func ResampleLinearInt16(samples []int16, srcRate, dstRate int) []int16 {
	if len(samples) == 0 || srcRate <= 0 || dstRate <= 0 {
		return nil
	}
	if srcRate == dstRate {
		out := make([]int16, len(samples))
		copy(out, samples)
		return out
	}
	outLen := int(math.Round(float64(len(samples)) * float64(dstRate) / float64(srcRate)))
	if outLen <= 0 {
		return nil
	}
	out := make([]int16, outLen)
	if len(samples) == 1 {
		for i := range out {
			out[i] = samples[0]
		}
		return out
	}
	scale := float64(srcRate) / float64(dstRate)
	for i := range out {
		pos := float64(i) * scale
		left := int(pos)
		if left >= len(samples)-1 {
			out[i] = samples[len(samples)-1]
			continue
		}
		frac := pos - float64(left)
		value := float64(samples[left])*(1-frac) + float64(samples[left+1])*frac
		if value > math.MaxInt16 {
			value = math.MaxInt16
		}
		if value < math.MinInt16 {
			value = math.MinInt16
		}
		out[i] = int16(math.Round(value))
	}
	return out
}
