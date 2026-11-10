// Package recording turns live PCM frames into finalized, source-tagged segments.
package recording

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"tala/internal/audio"
	"tala/internal/transcriber"
)

type Input func(context.Context, func(audio.Frame) error) error
type Decoder func(context.Context, []float32) ([]transcriber.Segment, error)

// Run stops taking new input when ctx is canceled and finalizes the audio that
// was already captured. A decoder or persistence failure aborts all stages.
// Scored segments include a read-only PCM clip for optional review.
func Run(ctx context.Context, input Input, decode Decoder, save func(transcriber.Segment, []float32) error) (time.Duration, error) {
	capture, stop := context.WithCancel(ctx)
	defer stop()
	group, drain := errgroup.WithContext(context.WithoutCancel(ctx))
	cancelCaptureOnFailure := context.AfterFunc(drain, stop)
	defer cancelCaptureOnFailure()

	frames := make(chan audio.Frame, 128)
	windows := make(chan window, 8)
	var captureErr error
	var recorded time.Duration
	group.Go(func() error {
		defer close(frames)
		captureErr = input(capture, func(frame audio.Frame) error {
			select {
			case <-drain.Done():
				return drain.Err()
			case frames <- frame:
				return nil
			default:
				return fmt.Errorf("recording cannot keep up with incoming audio")
			}
		})
		return nil // Finalize audio received before a device failure as well.
	})
	group.Go(func() error {
		defer close(windows)
		var err error
		recorded, err = collect(drain, frames, windows)
		return err
	})
	group.Go(func() error {
		for w := range windows {
			if err := drain.Err(); err != nil {
				return err
			}
			if err := recognize(drain, w, decode, save); err != nil {
				return err
			}
		}
		return nil
	})
	if err := group.Wait(); err != nil {
		return recorded, err
	}
	return recorded, captureErr
}

func recognize(ctx context.Context, w window, decode Decoder, save func(transcriber.Segment, []float32) error) error {
	samples := resample(w.samples, w.rate)
	actual := samples
	if len(samples) < transcriber.SampleRate {
		// Whisper requires a minimum input window. Preserve the actual end time
		// when the last phrase is shorter than that window.
		samples = append(samples, make([]float32, transcriber.SampleRate-len(samples))...)
	}
	parts, err := decode(ctx, samples)
	if err != nil {
		return fmt.Errorf("transcribe %s: %w", w.source, err)
	}
	duration := audio.Elapsed(int64(len(w.samples)), w.rate)
	for _, part := range parts {
		if part.Start >= duration {
			continue
		}
		var clip []float32
		if len(part.TokenScores) > 0 && part.End > part.Start {
			// Give the second pass a little context without including the
			// artificial padding added for short recording windows.
			const margin = 180 * time.Millisecond
			const samplePeriod = time.Second / transcriber.SampleRate
			first := min(len(actual), int(max(0, part.Start-margin)/samplePeriod))
			last := min(len(actual), int(max(0, part.End+margin)/samplePeriod))
			clip = actual[first:last]
		}
		part.Source = w.source
		part.Start = w.start + max(0, part.Start)
		part.End = max(part.Start, w.start+min(duration, part.End))
		if err := save(part, clip); err != nil {
			return fmt.Errorf("save recording segment: %w", err)
		}
	}
	return nil
}

type window struct {
	source  string
	rate    int
	start   time.Duration
	samples []float32
	quiet   time.Duration
	sound   bool
	speech  bool
}

const (
	windowLimit = 12 * time.Second
	pauseLength = 900 * time.Millisecond
)

func collect(ctx context.Context, frames <-chan audio.Frame, windows chan<- window) (time.Duration, error) {
	var recorded time.Duration
	active := make(map[string]window, 2)
	publish := func(w window) error {
		if !w.sound {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case windows <- w:
			return nil
		default:
			return fmt.Errorf("transcription cannot keep up with live recording")
		}
	}
	for {
		select {
		case <-ctx.Done():
			return recorded, ctx.Err()
		case frame, open := <-frames:
			if !open {
				return recorded, finalize(active, publish)
			}
			w, err := collectFrame(active[frame.Source], frame, publish)
			if err != nil {
				return recorded, err
			}
			active[frame.Source] = w
			recorded = max(recorded, frame.Start+audio.Elapsed(int64(len(frame.Samples)), frame.Rate))
		}
	}
}

func finalize(active map[string]window, publish func(window) error) error {
	first, second := active[audio.Microphone], active[audio.System]
	if second.start < first.start {
		first, second = second, first
	}
	if err := publish(first); err != nil {
		return err
	}
	return publish(second)
}

func collectFrame(w window, frame audio.Frame, publish func(window) error) (window, error) {
	if frame.Rate < 8000 || frame.Rate > 384000 || frame.Start < 0 || (frame.Source != audio.Microphone && frame.Source != audio.System) {
		return w, fmt.Errorf("invalid %s audio frame", frame.Source)
	}
	if len(w.samples) > 0 {
		expected := w.start + audio.Elapsed(int64(len(w.samples)), w.rate)
		delta := expected - frame.Start
		if w.rate != frame.Rate || delta > time.Millisecond || delta < -time.Millisecond {
			if err := publish(w); err != nil {
				return w, err
			}
			w = window{}
		}
	}
	for offset := 0; offset < len(frame.Samples); {
		if len(w.samples) == 0 {
			w = window{source: frame.Source, rate: frame.Rate, start: frame.Start + audio.Elapsed(int64(offset), frame.Rate)}
		}
		limit := w.rate * int(windowLimit/time.Second)
		count := min(limit-len(w.samples), len(frame.Samples)-offset)
		part := frame.Samples[offset : offset+count]
		offset += count
		w.samples = append(w.samples, part...)
		var energy float64
		for _, value := range part {
			energy += float64(value) * float64(value)
			w.sound = w.sound || value != 0
		}
		if energy < float64(count)*0.004*0.004 {
			w.quiet += audio.Elapsed(int64(count), w.rate)
		} else {
			w.speech = true
			w.quiet = 0
		}
		if len(w.samples) == limit || (w.speech && w.quiet >= pauseLength) {
			if err := publish(w); err != nil {
				return w, err
			}
			w = window{}
		}
	}
	return w, nil
}
