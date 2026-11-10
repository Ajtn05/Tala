package transcriber

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
)

// SampleRate is the PCM rate expected by Whisper.
const SampleRate = 16000

type Segment struct {
	Source      string
	Start, End  time.Duration
	Text        string
	TokenScores []TokenScore
}

// TokenScore locates a text token by byte offsets in Segment.Text.
type TokenScore struct {
	Start, End  int
	Probability float32
}

type Config struct {
	ModelPath string
	Language  string
	Threads   int
	Prompt    string
}

// Transcriber owns a loaded model. The controller serializes its use.
type Transcriber struct {
	model   whisper.Model
	options Config
	speech  speechDetector
}

func New(options Config) (*Transcriber, error) {
	model, err := whisper.New(options.ModelPath)
	if err != nil {
		return nil, fmt.Errorf("open Whisper model: %w", err)
	}
	if options.Threads <= 0 {
		options.Threads = min(4, runtime.GOMAXPROCS(0))
	}
	return &Transcriber{model: model, options: options, speech: newSpeechDetector()}, nil
}

func (t *Transcriber) SetOptions(language, prompt string) {
	t.options.Language = language
	t.options.Prompt = prompt
}

func (t *Transcriber) Close() error {
	t.speech.close()
	t.speech = speechDetector{}
	return t.model.Close()
}

func (t *Transcriber) Transcribe(ctx context.Context, samples []float32) ([]Segment, error) {
	return t.transcribe(ctx, samples, false)
}

func (t *Transcriber) TranscribeWithScores(ctx context.Context, samples []float32) ([]Segment, error) {
	return t.transcribe(ctx, samples, true)
}

func (t *Transcriber) transcribe(ctx context.Context, samples []float32, scores bool) ([]Segment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !hasAudibleSignal(samples) {
		return nil, nil
	}
	spans, checked := t.speech.spans(samples)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !checked {
		return t.decode(ctx, speechChunk{samples: samples}, nil, scores)
	}
	if len(spans) == 0 {
		return nil, nil
	}
	var result []Segment
	for _, chunk := range speechChunks(samples, spans) {
		parts, err := t.decode(ctx, chunk, spans, scores)
		if err != nil {
			return nil, err
		}
		result = append(result, parts...)
	}
	return result, nil
}

func (t *Transcriber) decode(ctx context.Context, chunk speechChunk, spans []speechSpan, scores bool) ([]Segment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	decoder, err := t.model.NewContext()
	if err != nil {
		return nil, fmt.Errorf("prepare Whisper decoder: %w", err)
	}
	language := t.options.Language
	taglish := language == "taglish"
	if taglish || language == "" {
		language = "auto"
	}
	if err := decoder.SetLanguage(language); err != nil {
		return nil, fmt.Errorf("recognition language %q: %w", language, err)
	}
	decoder.SetThreads(uint(t.options.Threads))
	decoder.SetTranslate(false)
	decoder.SetInitialPrompt(t.options.Prompt)

	err = decoder.Process(chunk.samples, func() bool {
		if ctx.Err() != nil {
			return false
		}
		if taglish {
			// Whisper runs auto-detection before calling this hook. Reject other
			// languages before generating any transcript text for this chunk.
			detected := decoder.DetectedLanguage()
			return detected == "en" || detected == "tl"
		}
		return true
	}, nil, nil)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("decode with Whisper: %w", err)
	}
	var result []Segment
	chunkEnd := chunk.offset + time.Duration(len(chunk.samples))*samplePeriod
	for {
		part, err := decoder.NextSegment()
		switch {
		case errors.Is(err, io.EOF):
			return result, nil
		case err != nil:
			return nil, fmt.Errorf("read Whisper segment: %w", err)
		}
		start := part.Start + chunk.offset
		end := max(start, min(part.End+chunk.offset, chunkEnd))
		if start >= chunkEnd || strings.TrimSpace(part.Text) == "" || !segmentHasSignal(chunk.samples, part.Start, part.End) ||
			(spans != nil && !speechOverlaps(spans, start, end)) {
			continue
		}
		segment := Segment{Text: part.Text, Start: start, End: end}
		if scores {
			segment.TokenScores = scoreTextTokens(decoder, part)
		}
		result = append(result, segment)
	}
}

func scoreTextTokens(decoder whisper.Context, part whisper.Segment) []TokenScore {
	var text strings.Builder
	scores := make([]TokenScore, 0, len(part.Tokens))
	for _, token := range part.Tokens {
		if decoder.IsText(token) {
			start := text.Len()
			text.WriteString(token.Text)
			if text.Len() > start {
				scores = append(scores, TokenScore{start, text.Len(), token.P})
			}
		}
	}
	raw := text.String()
	trimmed := strings.TrimSpace(raw)
	if trimmed != part.Text {
		return nil
	}
	offset := strings.Index(raw, trimmed)
	for i := range scores {
		scores[i].Start -= offset
		scores[i].End -= offset
	}
	return scores
}

func hasAudibleSignal(samples []float32) bool {
	const (
		frameSamples = SampleRate / 10 // 100 ms
		noiseFloor   = 0.0009          // About -61 dBFS RMS, below the optional import peak cutoff.
	)
	for len(samples) > 0 {
		n := min(frameSamples, len(samples))
		var energy float64
		for _, sample := range samples[:n] {
			value := float64(sample)
			energy += value * value
		}
		if energy >= float64(n)*noiseFloor*noiseFloor {
			return true
		}
		samples = samples[n:]
	}
	return false
}

func segmentHasSignal(samples []float32, start, end time.Duration) bool {
	if end <= start {
		return true // An empty or reversed timestamp cannot locate the speech.
	}
	const timestampPadding = 100 * time.Millisecond
	first := min(len(samples), int(max(0, start-timestampPadding)/samplePeriod))
	last := min(len(samples), int(max(0, end+timestampPadding)/samplePeriod))
	return hasAudibleSignal(samples[first:last])
}
