package audio

import (
	"context"
	"errors"
	"fmt"
	"log"
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
	params := portaudio.HighLatencyParameters(device, nil)
	params.Input.Channels = 1
	// Allow short stalls in the reader while keeping the reads themselves small.
	params.Input.Latency = max(params.Input.Latency, 100*time.Millisecond)
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

	return readMicrophone(ctx, origin, rate, stream, samples, emit)
}

type microphoneReader interface {
	Read() error
	AvailableToRead() (int, error)
}

func readMicrophone(ctx context.Context, origin time.Time, rate int, stream microphoneReader, samples []float32, emit func(Frame) error) error {
	start := time.Since(origin)
	var position int64
	var resync bool
	for ctx.Err() == nil {
		if err := stream.Read(); err != nil {
			if !errors.Is(err, portaudio.InputOverflowed) {
				return fmt.Errorf("read microphone: %w", err)
			}
			// PortAudio still fills the buffer on overflow.
			if !resync {
				log.Print("microphone input overflowed; some samples were lost, continuing recording")
			}
			resync = true
		}
		frame := Frame{Source: Microphone, Rate: rate, Start: start + Elapsed(position, rate)}
		frame.Samples = append([]float32(nil), samples...)
		if err := emit(frame); err != nil {
			return err
		}
		position += int64(len(samples))
		if resync {
			// Once the backlog is small, estimate the next frame's position on
			// the shared clock. PortAudio doesn't report how many samples it lost.
			if queued, err := stream.AvailableToRead(); err == nil && queued < len(samples) {
				start = max(start, time.Since(origin)-Elapsed(position+int64(queued), rate))
				resync = false
			}
		}
	}
	return nil
}
