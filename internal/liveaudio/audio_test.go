package liveaudio

import "testing"

func TestStereoToMonoAndMonoToStereo(t *testing.T) {
	t.Parallel()

	mono := StereoToMono([]int16{10, 14, -4, -8})
	if len(mono) != 2 || mono[0] != 12 || mono[1] != -6 {
		t.Fatalf("unexpected mono samples: %#v", mono)
	}

	stereo := MonoToStereo([]int16{7, -3})
	if len(stereo) != 4 || stereo[0] != 7 || stereo[1] != 7 || stereo[2] != -3 || stereo[3] != -3 {
		t.Fatalf("unexpected stereo samples: %#v", stereo)
	}
}

func TestResampleLinearInt16PreservesExpectedFrameLengths(t *testing.T) {
	t.Parallel()

	in48 := make([]int16, DiscordFrameSamples)
	for i := range in48 {
		in48[i] = int16(i)
	}
	got16 := ResampleLinearInt16(in48, DiscordSampleRate, GeminiInputSampleRate)
	if len(got16) != GeminiInputFrameSamples {
		t.Fatalf("expected %d samples, got %d", GeminiInputFrameSamples, len(got16))
	}

	in24 := make([]int16, GeminiOutputFrameSamples)
	for i := range in24 {
		in24[i] = int16(i)
	}
	got48 := ResampleLinearInt16(in24, GeminiOutputSampleRate, DiscordSampleRate)
	if len(got48) != DiscordFrameSamples {
		t.Fatalf("expected %d samples, got %d", DiscordFrameSamples, len(got48))
	}
}

func TestPCMBytesRoundTrip(t *testing.T) {
	t.Parallel()

	input := []int16{-10, 0, 20, 300}
	roundTrip := PCMBytesToInt16(Int16ToPCMBytes(input))
	if len(roundTrip) != len(input) {
		t.Fatalf("unexpected round trip length: got %d want %d", len(roundTrip), len(input))
	}
	for i := range input {
		if roundTrip[i] != input[i] {
			t.Fatalf("unexpected round trip sample at %d: got %d want %d", i, roundTrip[i], input[i])
		}
	}
}
