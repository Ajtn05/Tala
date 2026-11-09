package audio

import (
	"context"
	"errors"
	"time"

	"golang.org/x/sync/errgroup"
)

// Mode values are part of the dashboard bridge's wire format.
type Mode int

const (
	ModeBoth       Mode = 0
	ModeMicrophone Mode = 1
	ModeSystem     Mode = 2
)

const (
	Microphone = "Microphone"
	System     = "System Audio"
)

// Frame contains mono PCM and its position on the recording's shared clock.
type Frame struct {
	Source  string
	Rate    int
	Start   time.Duration
	Samples []float32
}

// Capture sends frames until stopped. In combined mode emit may be called
// concurrently. Each frame owns its sample buffer.
func Capture(ctx context.Context, mode Mode, emit func(Frame) error) error {
	origin := time.Now()
	switch mode {
	case ModeMicrophone:
		return microphone(ctx, origin, emit)
	case ModeSystem:
		return systemAudio(ctx, origin, emit)
	case ModeBoth:
		group, captureCtx := errgroup.WithContext(ctx)
		group.Go(func() error { return microphone(captureCtx, origin, emit) })
		group.Go(func() error { return systemAudio(captureCtx, origin, emit) })
		return group.Wait()
	default:
		return errors.New("invalid audio source")
	}
}

func Elapsed(frames int64, rate int) time.Duration {
	seconds, fraction := frames/int64(rate), frames%int64(rate)
	return time.Duration(seconds)*time.Second + time.Duration(fraction)*time.Second/time.Duration(rate)
}
