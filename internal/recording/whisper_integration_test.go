package recording

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tala/internal/audio"
	"tala/internal/media"
	"tala/internal/session"
	"tala/internal/transcriber"
)

func TestWhisperRecordingRoundTrip(t *testing.T) {
	model := os.Getenv("TALA_TEST_MODEL")
	if model == "" {
		t.Skip("set TALA_TEST_MODEL to exercise real Whisper inference")
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var pcm []float32
	sample := filepath.Join("..", "..", "third_party", "whisper.cpp", "samples", "jfk.wav")
	if err := media.Stream(ctx, sample, func(data []float32, _ float64) error {
		pcm = append(pcm, data...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	decoder, err := transcriber.New(transcriber.Config{ModelPath: model, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()

	store := &session.Store{Dir: t.TempDir()}
	entry, err := store.Create("live", "Recording round trip", audio.Microphone)
	if err != nil {
		t.Fatal(err)
	}
	input := func(ctx context.Context, send func(audio.Frame) error) error {
		if err := send(audio.Frame{Source: audio.Microphone, Rate: transcriber.SampleRate, Samples: pcm}); err != nil {
			return err
		}
		stop()
		<-ctx.Done()
		return nil
	}
	duration, err := Run(ctx, input, decoder.Transcribe, func(part transcriber.Segment, _ []float32) error {
		_, err := store.Append(entry.ID, session.Segment{Source: part.Source, Text: part.Text, Start: part.Start.Seconds(), End: part.End.Seconds()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := (&session.Store{Dir: store.Dir}).Get(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Segments) == 0 {
		t.Fatal("no transcript was saved")
	}
	for _, part := range reopened.Segments {
		if part.Source != audio.Microphone || part.Start < 0 || part.End > duration.Seconds() || part.End <= part.Start {
			t.Fatalf("invalid saved timing or source: %+v, duration %v", part, duration)
		}
	}
	last := reopened.Segments[len(reopened.Segments)-1]
	if !strings.Contains(strings.ToLower(last.Text), "what you can do for your country") {
		t.Fatalf("last words were missing: %q", last.Text)
	}
}
