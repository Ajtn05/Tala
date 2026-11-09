package audio

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/gordonklaus/portaudio"
)

func microphone(ctx context.Context, origin time.Time, emit func(Frame) error) error {
	if ctx.Err() != nil {
		return nil
	}
	if err := portaudio.Initialize(); err != nil {
		return fmt.Errorf("initialize microphone backend: %w", err)
	}
	defer portaudio.Terminate()

	device, err := portaudio.DefaultInputDevice()
	if err != nil {
		return fmt.Errorf("find default microphone: %w", err)
	}
	params := portaudio.LowLatencyParameters(device, nil)
	params.Input.Channels = 1
	rate := int(math.Round(params.SampleRate))
	if rate <= 0 {
		return fmt.Errorf("invalid microphone sample rate: %g", params.SampleRate)
	}
	params.FramesPerBuffer = max(1, rate/50)
	samples := make([]float32, params.FramesPerBuffer)
	stream, err := portaudio.OpenStream(params, samples)
	if err != nil {
		return fmt.Errorf("open microphone: %w", err)
	}
	defer stream.Close()
	if err := stream.Start(); err != nil {
		return fmt.Errorf("start microphone: %w", err)
	}
	defer stream.Abort()

	start := time.Since(origin)
	var position int64
	for ctx.Err() == nil {
		if err := stream.Read(); err != nil {
			return fmt.Errorf("read microphone: %w", err)
		}
		frame := Frame{Source: Microphone, Rate: rate, Start: start + Elapsed(position, rate)}
		frame.Samples = append([]float32(nil), samples...)
		if err := emit(frame); err != nil {
			return err
		}
		position += int64(len(samples))
	}
	return nil
}
