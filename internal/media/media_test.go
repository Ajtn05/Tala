package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type observedPCMReader struct {
	io.Reader
	reads chan int
}

func (r observedPCMReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.reads <- n
	}
	return n, err
}

func TestPrefetchPreservesEverySampleAndWindowBoundary(t *testing.T) {
	// Include arbitrary float bit patterns, rather than only a periodic tone.
	// Retain each window until decoding ends to detect accidental buffer reuse.
	const window = 30 * 16000
	raw := make([]byte, (2*window+13)*4)
	for i := 0; i < len(raw)/4; i++ {
		binary.LittleEndian.PutUint32(raw[i*4:], uint32(i)*2654435761)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reads := make(chan int, 3)
	chunks := make(chan pcmChunk)
	done := make(chan error, 1)
	go func() {
		done <- decodePCM(ctx, observedPCMReader{bytes.NewReader(raw), reads}, chunks)
		close(chunks)
	}()
	var got []pcmChunk
	select {
	case first := <-chunks:
		got = append(got, first)
	case <-ctx.Done():
		t.Fatal("first window was not delivered")
	}
	// The second window must be read before the first consumer is finished.
	for i := 0; i < 2; i++ {
		select {
		case <-reads:
		case <-ctx.Done():
			t.Fatal("decoder did not read ahead during consumption")
		}
	}
	select {
	case <-reads:
		t.Fatal("decoder read more than one window ahead")
	default:
	}
	for chunk := range chunks {
		got = append(got, chunk)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].offset != 0 || got[1].offset != 30 || got[2].offset != 60 || len(got[2].samples) != 13 {
		t.Fatalf("window boundaries changed; received %d windows", len(got))
	}
	index := 0
	for _, chunk := range got {
		for _, sample := range chunk.samples {
			want := binary.LittleEndian.Uint32(raw[index*4:])
			if math.Float32bits(sample) != want {
				t.Fatalf("sample %d changed: got %08x, want %08x", index, math.Float32bits(sample), want)
			}
			index++
		}
	}
}

func TestPrefetchCancellationWhileWaitingForConsumer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reads := make(chan int, 1)
	chunks := make(chan pcmChunk)
	done := make(chan error, 1)
	go func() {
		done <- decodePCM(ctx, observedPCMReader{bytes.NewReader(make([]byte, 30*16000*4)), reads}, chunks)
	}()
	<-reads
	cancel() // No consumer receives the prefetched window.
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected cancellation error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("prefetch goroutine leaked while waiting for consumer")
	}
}

func TestPrefetchRejectsTruncatedPCM(t *testing.T) {
	if err := decodePCM(context.Background(), bytes.NewReader([]byte{0, 1, 2}), make(chan pcmChunk)); err == nil {
		t.Fatal("accepted a truncated float sample")
	}
}

func TestURLValidation(t *testing.T) {
	for _, s := range []string{"file:///etc/passwd", "ftp://example.com/a", "https://user:password@example.com/a", "javascript:alert(1)", "--exec=bad", "https://"} {
		if ValidateURL(s) == nil {
			t.Errorf("accepted %q", s)
		}
	}
	for _, s := range []string{"https://youtube.com/shorts/example", "https://example.org/a.mp3?q=a&b=1", "http://localhost:8000/audio"} {
		if e := ValidateURL(s); e != nil {
			t.Error(e)
		}
	}
}
func synthetic(t *testing.T, seconds int) string {
	t.Helper()
	tool, e := Tool("ffmpeg")
	if e != nil {
		t.Skip(e)
	}
	p := filepath.Join(t.TempDir(), "lecture with spaces.wav")
	if out, e := exec.Command(tool, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=16000", "-t", strconv.Itoa(seconds), "-ac", "1", p).CombinedOutput(); e != nil {
		t.Fatalf("synthetic fixture: %v %s", e, out)
	}
	return p
}

func TestProbeAndStreamingChunksPreserveTime(t *testing.T) {
	p := synthetic(t, 63)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	m, e := Probe(ctx, p)
	if e != nil || math.Abs(m.Duration-63) > .01 {
		t.Fatalf("probe: %+v %v", m, e)
	}
	var offsets []float64
	var sizes []int
	e = Stream(ctx, p, func(samples []float32, offset float64) error {
		offsets = append(offsets, offset)
		sizes = append(sizes, len(samples))
		if len(samples) > 30*16000 {
			t.Fatal("unbounded PCM chunk")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if len(offsets) != 3 || offsets[1] != 30 || offsets[2] != 60 || sizes[2] != 3*16000 {
		t.Fatalf("incorrect chunk offsets: %v %v", offsets, sizes)
	}
}
func TestStreamCancellationStopsDecoder(t *testing.T) {
	p := synthetic(t, 63)
	ctx, cancel := context.WithCancel(context.Background())
	e := Stream(ctx, p, func(_ []float32, _ float64) error { cancel(); return ctx.Err() })
	if !errors.Is(e, context.Canceled) {
		t.Fatalf("did not cancel: %v", e)
	}
}

func TestStreamConsumerErrorStopsPrefetch(t *testing.T) {
	p := synthetic(t, 63)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	want := errors.New("stop consuming")
	calls := 0
	err := Stream(ctx, p, func(_ []float32, _ float64) error {
		calls++
		return want
	})
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("consumer error lost or subsequent window consumed: %v, calls=%d", err, calls)
	}
}

func TestStreamPreservesDecoderFailureAfterPartialAudio(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '\\000\\000\\200\\077'\nprintf 'fixture decode failed' >&2\nexit 7\n"
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	calls := 0
	err := Stream(context.Background(), "/unused.wav", func(samples []float32, offset float64) error {
		calls++
		if len(samples) != 1 || samples[0] != 1 || offset != 0 {
			t.Fatalf("partial audio changed: %v at %v", samples, offset)
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "fixture decode failed") || calls != 1 {
		t.Fatalf("decoder failure lost: %v, calls=%d", err, calls)
	}
}
func TestProbeRejectsNonAudioAndDirectories(t *testing.T) {
	p := filepath.Join(t.TempDir(), "not audio.txt")
	_ = os.WriteFile(p, []byte("test"), 0600)
	if _, e := Probe(context.Background(), p); e == nil {
		t.Fatal("accepted invalid media")
	}
	if _, e := Probe(context.Background(), filepath.Dir(p)); e == nil {
		t.Fatal("accepted directory")
	}
}
func TestMetadataAndDownloadArgumentsAreNotShellCommands(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	t.Setenv("TALA_TEST_ARGS", args)
	script := filepath.Join(dir, "yt-dlp")
	_ = os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TALA_TEST_ARGS\"\nprintf '%s' '{\"title\":\"Lecture\",\"duration\":12}'\n"), 0700)
	t.Setenv("PATH", dir)
	raw := "https://example.com/audio?title=$(touch%20bad)&a=1"
	m, e := Fetch(context.Background(), raw)
	if e != nil || m.Title != "Lecture" || m.Duration != 12 {
		t.Fatalf("metadata: %+v %v", m, e)
	}
	b, _ := os.ReadFile(args)
	if !strings.Contains(string(b), "--\n"+raw+"\n") {
		t.Fatalf("URL was not an isolated positional argument: %s", b)
	}
}
