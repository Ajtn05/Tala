package audio

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gordonklaus/portaudio"
)

type testMicrophone struct {
	read   func() error
	queued int
}

func (m *testMicrophone) Read() error                   { return m.read() }
func (m *testMicrophone) AvailableToRead() (int, error) { return m.queued, nil }

func TestMicrophoneContinuesAfterInputOverflow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const rate = 16000
		samples := make([]float32, rate/50)
		disconnected := errors.New("device disconnected")
		stream := &testMicrophone{}
		var reads int
		stream.read = func() error {
			reads++
			samples[0] = float32(reads)
			switch reads {
			case 2:
				time.Sleep(500 * time.Millisecond)
				stream.queued = 2 * len(samples)
				return portaudio.InputOverflowed
			case 3:
				stream.queued = 0
			case 5:
				return disconnected
			default:
				time.Sleep(20 * time.Millisecond)
			}
			return nil
		}

		var frames []Frame
		err := readMicrophone(t.Context(), time.Now(), rate, stream, samples, func(frame Frame) error {
			frames = append(frames, frame)
			return nil
		})
		if !errors.Is(err, disconnected) {
			t.Fatalf("expected the device failure, got %v", err)
		}
		wantStarts := []time.Duration{0, 20 * time.Millisecond, 40 * time.Millisecond, 520 * time.Millisecond}
		if len(frames) != len(wantStarts) {
			t.Fatalf("got %d frames, want %d", len(frames), len(wantStarts))
		}
		for i, frame := range frames {
			if frame.Start != wantStarts[i] || frame.Samples[0] != float32(i+1) {
				t.Errorf("frame %d starts at %s with sample %g, want %s with sample %d", i, frame.Start, frame.Samples[0], wantStarts[i], i+1)
			}
		}
	})
}
