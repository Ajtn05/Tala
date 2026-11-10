package transcriber

import (
	"context"
	"io"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
)

func TestDecodeTaglishLanguageGate(t *testing.T) {
	for _, tc := range []struct {
		mode, detected string
		wantDecode     bool
	}{
		{"taglish", "en", true},
		{"taglish", "tl", true},
		{"taglish", "es", false},
		{"taglish", "", false},
		{"auto", "es", true},
	} {
		t.Run(tc.mode+"/"+tc.detected, func(t *testing.T) {
			decoder := &languageGateDecoder{detected: tc.detected}
			tr := &Transcriber{model: languageGateModel{decoder: decoder}, options: Config{Language: tc.mode}}
			_, err := tr.decode(t.Context(), speechChunk{samples: make([]float32, SampleRate)}, nil, false)
			if err != nil || decoder.language != "auto" || decoder.decoded != tc.wantDecode {
				t.Fatalf("configured %q, decoded %v, error %v", decoder.language, decoder.decoded, err)
			}
		})
	}
}

type languageGateModel struct {
	whisper.Model
	decoder whisper.Context
}

func (m languageGateModel) NewContext() (whisper.Context, error) { return m.decoder, nil }

type languageGateDecoder struct {
	whisper.Context
	detected, language string
	decoded            bool
}

func (d *languageGateDecoder) SetLanguage(lang string) error { d.language = lang; return nil }
func (d *languageGateDecoder) DetectedLanguage() string      { return d.detected }
func (d *languageGateDecoder) SetThreads(uint)               {}
func (d *languageGateDecoder) SetTranslate(bool)             {}
func (d *languageGateDecoder) SetInitialPrompt(string)       {}
func (d *languageGateDecoder) NextSegment() (whisper.Segment, error) {
	return whisper.Segment{}, io.EOF
}
func (d *languageGateDecoder) Process(_ []float32, begin whisper.EncoderBeginCallback, _ whisper.SegmentCallback, _ whisper.ProgressCallback) error {
	d.decoded = begin()
	return nil
}

func TestTranscribeSkipsNoiseAboveVolumeFloor(t *testing.T) {
	detector := newSpeechDetector()
	defer detector.close()
	if detector.ctx == nil {
		t.Fatal("could not load bundled speech detector")
	}
	rng := rand.New(rand.NewPCG(25, 3))
	samples := make([]float32, 12*SampleRate)
	var noise float32
	for i := range samples {
		noise = 0.8*noise + 0.2*(2*rng.Float32()-1)
		samples[i] = noise * 0.04
	}
	if !hasAudibleSignal(samples) {
		t.Fatal("fixture must cross the volume floor")
	}
	// A speech-free microphone window should never need the transcription model.
	parts, err := (&Transcriber{speech: detector}).Transcribe(t.Context(), samples)
	if err != nil || len(parts) != 0 {
		t.Fatalf("background noise returned %v, %v", parts, err)
	}
}

func TestSpeechChunksPreserveTheOriginalClock(t *testing.T) {
	samples := make([]float32, 12*SampleRate)
	spans := []speechSpan{
		{2 * time.Second, 3 * time.Second},
		{3500 * time.Millisecond, 4 * time.Second},
		{8 * time.Second, 9 * time.Second},
	}
	chunks := speechChunks(samples, spans)
	if len(chunks) != 2 ||
		chunks[0].offset != 1680*time.Millisecond || len(chunks[0].samples) != 2320*SampleRate/1000 ||
		chunks[1].offset != 7680*time.Millisecond || len(chunks[1].samples) != 1320*SampleRate/1000 {
		t.Fatalf("wrong speech chunk offsets or lengths: %d chunks", len(chunks))
	}
	if speechOverlaps(spans, 5*time.Second, 6*time.Second) {
		t.Fatal("a caption wholly inside the pause passed the speech check")
	}
}

func TestTranscribeSkipsLowLevelNoise(t *testing.T) {
	samples := make([]float32, 12*SampleRate)
	for i := range samples {
		samples[i] = 0.0005
	}
	samples[SampleRate] = 0.01 // A single peak should not turn noise into speech.

	// No model is needed because the audio should be skipped before decoding.
	parts, err := (&Transcriber{}).Transcribe(context.Background(), samples)
	if err != nil || len(parts) != 0 {
		t.Fatalf("low-level noise returned %v, %v", parts, err)
	}
}

func TestSegmentHasSignal(t *testing.T) {
	samples := make([]float32, 2*SampleRate)
	for i := range samples {
		samples[i] = 0.0005
	}
	for i := SampleRate / 2; i < 3*SampleRate/5; i++ {
		samples[i] = 0.002
	}

	if !hasAudibleSignal(samples) {
		t.Fatal("brief, quiet speech was missed in the full window")
	}
	if !segmentHasSignal(samples, 650*time.Millisecond, 800*time.Millisecond) {
		t.Fatal("speech just outside the estimated timestamps was missed")
	}
	if segmentHasSignal(samples, time.Second, 2*time.Second) {
		t.Fatal("segment over the quiet tail was kept")
	}
}
