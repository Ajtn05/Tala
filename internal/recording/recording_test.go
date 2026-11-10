package recording

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"tala/internal/audio"
	"tala/internal/transcriber"
)

func TestStopDrainsAndSavesFinalAudio(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	samples := make([]float32, 24000)
	for i := range samples {
		samples[i] = 0.25
	}
	input := func(ctx context.Context, send func(audio.Frame) error) error {
		if err := send(audio.Frame{Source: audio.Microphone, Rate: 48000, Start: 2 * time.Second, Samples: samples}); err != nil {
			return err
		}
		stop()
		<-ctx.Done()
		return nil
	}
	decode := func(ctx context.Context, pcm []float32) ([]transcriber.Segment, error) {
		if ctx.Err() != nil || len(pcm) != 16000 || pcm[7999] != 0.25 || pcm[8000] != 0 {
			t.Error("stop canceled inference or discarded audio before padding")
		}
		return []transcriber.Segment{{Text: "last words", Start: 100 * time.Millisecond, End: time.Second,
			TokenScores: []transcriber.TokenScore{{Start: 0, End: 4, Probability: 0.5}}}}, nil
	}
	var saved []transcriber.Segment
	duration, err := Run(ctx, input, decode, func(seg transcriber.Segment, clip []float32) error {
		if len(clip) != 8000 || clip[7999] != 0.25 {
			t.Error("review received artificial padding or lost recorded audio")
		}
		saved = append(saved, seg)
		return nil
	})
	if err != nil || duration != 2500*time.Millisecond {
		t.Fatalf("recording ended at %v: %v", duration, err)
	}
	want := transcriber.Segment{Source: audio.Microphone, Start: 2100 * time.Millisecond, End: 2500 * time.Millisecond, Text: "last words",
		TokenScores: []transcriber.TokenScore{{Start: 0, End: 4, Probability: 0.5}}}
	if len(saved) != 1 || !reflect.DeepEqual(saved[0], want) {
		t.Fatalf("saved %#v, want %#v", saved, want)
	}
}

func TestSaveFailureStopsCapture(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	writeErr := errors.New("disk full")
	input := func(ctx context.Context, send func(audio.Frame) error) error {
		samples := make([]float32, 12*16000)
		samples[0] = 0.5
		if err := send(audio.Frame{Source: audio.Microphone, Rate: 16000, Samples: samples}); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}
	decode := func(context.Context, []float32) ([]transcriber.Segment, error) {
		return []transcriber.Segment{{Text: "test", End: time.Second}}, nil
	}
	_, err := Run(ctx, input, decode, func(transcriber.Segment, []float32) error { return writeErr })
	if !errors.Is(err, writeErr) || ctx.Err() != nil {
		t.Fatalf("capture did not stop on storage failure: %v", err)
	}
}

func TestFractionalResampleRetainsTail(t *testing.T) {
	pcm := resample([]float32{1, 1, 1, 1}, 44100)
	if len(pcm) != 2 || pcm[0] != 1 || pcm[1] != 1 {
		t.Fatalf("wrong fractional resampling: %v", pcm)
	}
}
