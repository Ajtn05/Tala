package audio

import (
	"context"
	"errors"
	"time"
)

func systemAudio(context.Context, time.Time, func(Frame) error) error {
	return errors.New("system audio capture is available on macOS")
}
