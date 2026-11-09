// Package media decodes files as a bounded 16 kHz mono PCM stream. No playback clock is used.
package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Metadata struct {
	Title     string  `json:"title"`
	Duration  float64 `json:"duration"`
	Format    string  `json:"format"`
	URL       string  `json:"url,omitempty"`
	Thumbnail string  `json:"thumbnail,omitempty"`
}

func ValidateURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return errors.New("enter an HTTP or HTTPS media URL without embedded credentials")
	}
	return nil
}

// Commands receive argument arrays. Cancellation terminates the full subprocess group.
func command(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	p, e := Tool(name)
	if e != nil {
		return nil, e
	}
	cmd := exec.CommandContext(ctx, p, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		e := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if e == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return e
	}
	cmd.WaitDelay = 3 * time.Second
	return cmd, nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < b.limit {
		keep := min(n, b.limit-b.Len())
		_, _ = b.Buffer.Write(p[:keep])
	}
	return n, nil
}
func output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd, e := command(ctx, name, args...)
	if e != nil {
		return nil, e
	}
	out := &limitedBuffer{limit: 4 * 1024 * 1024}
	stderr := &limitedBuffer{limit: 8192}
	cmd.Stdout = out
	cmd.Stderr = stderr
	if e = cmd.Run(); e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s: %w: %s", name, e, stderr.String())
	}
	return out.Bytes(), nil
}
func Probe(ctx context.Context, path string) (Metadata, error) {
	abs, e := filepath.Abs(path)
	if e != nil {
		return Metadata{}, e
	}
	info, e := os.Stat(abs)
	if e != nil {
		return Metadata{}, e
	}
	if !info.Mode().IsRegular() {
		return Metadata{}, errors.New("select a regular audio or video file")
	}
	b, e := output(ctx, "ffprobe", "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=codec_type:format=duration,format_name", "-of", "json", abs)
	if e != nil {
		return Metadata{}, e
	}
	var v struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
			Name     string `json:"format_name"`
		} `json:"format"`
	}
	if e = json.Unmarshal(b, &v); e != nil {
		return Metadata{}, e
	}
	if len(v.Streams) == 0 {
		return Metadata{}, errors.New("this file contains no audio stream")
	}
	duration, _ := strconv.ParseFloat(v.Format.Duration, 64)
	return Metadata{Title: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), Duration: duration, Format: v.Format.Name}, nil
}
func Fetch(ctx context.Context, raw string) (Metadata, error) {
	return FetchWithAuth(ctx, raw, URLAuth{})
}
func FetchWithAuth(ctx context.Context, raw string, auth URLAuth) (Metadata, error) {
	if e := ValidateURL(raw); e != nil {
		return Metadata{}, e
	}
	b, e := ytDLPOutput(ctx, auth, "--no-playlist", "--skip-download", "--dump-single-json", "--no-warnings", "--", raw)
	if e != nil {
		return Metadata{}, e
	}
	var v struct {
		Title     string  `json:"title"`
		Duration  float64 `json:"duration"`
		Thumbnail string  `json:"thumbnail"`
		DRM       bool    `json:"has_drm"`
	}
	if e = json.Unmarshal(b, &v); e != nil {
		return Metadata{}, e
	}
	if v.DRM {
		return Metadata{}, errors.New("DRM-protected media is not supported")
	}
	return Metadata{v.Title, v.Duration, "URL", raw, v.Thumbnail}, nil
}
func Download(ctx context.Context, raw, dir string) (string, error) {
	return DownloadWithAuth(ctx, raw, dir, URLAuth{})
}
func DownloadWithAuth(ctx context.Context, raw, dir string, auth URLAuth) (string, error) {
	return DownloadWithProgress(ctx, raw, dir, auth, nil)
}
func DownloadWithProgress(ctx context.Context, raw, dir string, auth URLAuth, progress func(DownloadProgress)) (string, error) {
	if e := ValidateURL(raw); e != nil {
		return "", e
	}
	args := []string{"--no-playlist", "--newline", "--progress", "--progress-delta", "0.5", "--progress-template", downloadTemplate, "--no-warnings", "-f", "bestaudio/best", "-o", filepath.Join(dir, "source.%(ext)s"), "--", raw}
	_, e := ytDLPExecute(ctx, auth, args, func(args []string) ([]byte, error) { return streamDownload(ctx, args, progress) })
	if e != nil {
		return "", e
	}
	files, e := filepath.Glob(filepath.Join(dir, "source.*"))
	if e != nil {
		return "", e
	}
	for _, p := range files {
		if !strings.HasSuffix(p, ".part") && !strings.HasSuffix(p, ".ytdl") {
			return p, nil
		}
	}
	return "", errors.New("download completed without an audio file")
}

// Stream decodes one window ahead while consume processes the current window.
// It retains at most two 30-second PCM chunks and one raw decoding buffer.
// Consecutive windows do not overlap, and consume is always called sequentially.
func Stream(ctx context.Context, path string, consume func([]float32, float64) error) error {
	return StreamRange(ctx, path, TimeRange{}, consume)
}

// StreamRange decodes only the selected audio and reports original-media offsets.
func StreamRange(ctx context.Context, path string, selection TimeRange, consume func([]float32, float64) error) error {
	if err := selection.Validate(0); err != nil {
		return err
	}
	decodeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	abs, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	args := []string{"-nostdin", "-v", "error"}
	if selection.Start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(selection.Start, 'f', -1, 64))
	}
	args = append(args, "-i", abs)
	if selection.End > 0 {
		args = append(args, "-t", strconv.FormatFloat(selection.End-selection.Start, 'f', -1, 64))
	}
	args = append(args, "-map", "0:a:0", "-vn", "-ac", "1", "-ar", "16000", "-f", "f32le", "pipe:1")
	cmd, e := command(decodeCtx, "ffmpeg", args...)
	if e != nil {
		return e
	}
	pipe, e := cmd.StdoutPipe()
	if e != nil {
		return e
	}
	stderr := &limitedBuffer{limit: 8192}
	cmd.Stderr = stderr
	if e = cmd.Start(); e != nil {
		return e
	}
	ok := false
	chunks := make(chan pcmChunk)
	decoded := make(chan struct{})
	var decodeErr error
	go func() {
		defer close(decoded)
		defer close(chunks)
		decodeErr = decodePCM(decodeCtx, pipe, chunks)
	}()
	defer func() {
		cancel()
		if !ok {
			_ = cmd.Cancel()
		}
		<-decoded
		if !ok {
			_ = cmd.Wait()
		}
	}()
	offset := 0.0
	for chunk := range chunks {
		if e = ctx.Err(); e != nil {
			return e
		}
		if e = consume(chunk.samples, selection.Start+chunk.offset); e != nil {
			return e
		}
		offset = chunk.offset + float64(len(chunk.samples))/16000
	}
	<-decoded
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if decodeErr != nil {
		return decodeErr
	}
	e = cmd.Wait()
	ok = true
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e != nil {
		return fmt.Errorf("decode media: %w: %s", e, stderr.String())
	}
	if offset == 0 {
		return errors.New("no audio samples were decoded")
	}
	return nil
}

type pcmChunk struct {
	samples []float32
	offset  float64
}

func decodePCM(ctx context.Context, pipe io.Reader, chunks chan<- pcmChunk) error {
	raw := make([]byte, 30*16000*4)
	offset := 0.0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := io.ReadFull(pipe, raw)
		if n > 0 {
			if n%4 != 0 {
				return errors.New("truncated PCM sample")
			}
			samples := make([]float32, n/4)
			for i := range samples {
				samples[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
			}
			select {
			case chunks <- pcmChunk{samples, offset}:
			case <-ctx.Done():
				return ctx.Err()
			}
			offset += float64(len(samples)) / 16000
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return nil
}
