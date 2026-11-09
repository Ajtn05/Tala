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
	Source     string
	Start, End time.Duration
	Text       string
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
}

func New(options Config) (*Transcriber, error) {
	model, err := whisper.New(options.ModelPath)
	if err != nil {
		return nil, fmt.Errorf("open Whisper model: %w", err)
	}
	if options.Threads <= 0 {
		options.Threads = min(4, runtime.GOMAXPROCS(0))
	}
	return &Transcriber{model: model, options: options}, nil
}

func (t *Transcriber) SetOptions(language, prompt string) {
	t.options.Language = language
	t.options.Prompt = prompt
}

func (t *Transcriber) Close() error { return t.model.Close() }

func (t *Transcriber) Transcribe(ctx context.Context, samples []float32) ([]Segment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if digitalSilence(samples) {
		return nil, nil
	}
	decoder, err := t.model.NewContext()
	if err != nil {
		return nil, fmt.Errorf("prepare Whisper decoder: %w", err)
	}
	language := t.options.Language
	if language == "" {
		language = "auto"
	}
	if err := decoder.SetLanguage(language); err != nil {
		return nil, fmt.Errorf("recognition language %q: %w", language, err)
	}
	decoder.SetThreads(uint(t.options.Threads))
	decoder.SetTranslate(false)
	decoder.SetInitialPrompt(t.options.Prompt)

	err = decoder.Process(samples, func() bool { return ctx.Err() == nil }, nil, nil)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("decode with Whisper: %w", err)
	}
	var result []Segment
	for {
		part, err := decoder.NextSegment()
		switch {
		case errors.Is(err, io.EOF):
			return result, nil
		case err != nil:
			return nil, fmt.Errorf("read Whisper segment: %w", err)
		case strings.TrimSpace(part.Text) != "":
			result = append(result, Segment{Text: part.Text, Start: part.Start, End: part.End})
		}
	}
}

func digitalSilence(samples []float32) bool {
	for _, value := range samples {
		if value != 0 {
			return false
		}
	}
	return true
}
