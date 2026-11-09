package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"tala/internal/app"
	"tala/internal/media"
	"tala/internal/models"
)

func TestImportProgressUsesSelectedAudioAndHonestPhases(t *testing.T) {
	job := app.Job{ID: "clip", State: "transcribing", Duration: 20, Processed: 10, Progress: .5, Range: media.TimeRange{Start: 100, End: 120}}
	f := jobFrame(job, 7*time.Second)
	if !f.known || f.fraction != .5 || !strings.Contains(f.detail, "00:10/00:20") {
		t.Fatalf("selected duration lost: %+v", f)
	}
	for _, state := range []string{"waiting", "downloading", "preparing"} {
		job.State = state
		if f := jobFrame(job, time.Second); f.known {
			t.Fatalf("invented %s percentage: %+v", state, f)
		}
	}
	job.State, job.Progress = "failed", .25
	if f := jobFrame(job, time.Second); !f.final || f.fraction != .25 {
		t.Fatalf("failure incorrectly completed: %+v", f)
	}
	job.State, job.Progress = "transcribing", 1
	if f := jobFrame(job, time.Second); f.fraction >= 1 {
		t.Fatal("100% before session completion")
	}
	job.State = "completed"
	if f := jobFrame(job, time.Second); !f.final || f.fraction != 1 {
		t.Fatalf("completion not reported: %+v", f)
	}
}

func TestDownloadProgressIsSeparateFromTranscription(t *testing.T) {
	job := app.Job{ID: "link", State: "downloading", Duration: 60, Download: media.DownloadProgress{Status: "downloading", Downloaded: 1024, Total: 4096, Speed: 512, ETA: 6}}
	f := jobFrame(job, time.Second)
	if !f.known || f.fraction != .25 || !strings.Contains(f.detail, "1.0 KiB/4.0 KiB") || !strings.Contains(f.detail, "512 B/s") || !strings.Contains(f.detail, "ETA 00:06") {
		t.Fatalf("download stats missing: %+v", f)
	}
	job.Download.Total = 0
	if f := jobFrame(job, time.Second); f.known || !strings.Contains(f.detail, "1.0 KiB") {
		t.Fatalf("unknown-size transfer: %+v", f)
	}
	job.Download.Total, job.Download.Estimated = 4096, true
	if f := jobFrame(job, time.Second); !strings.Contains(f.detail, "/~4.0 KiB") {
		t.Fatalf("estimate not labeled: %+v", f)
	}
	job.State, job.Progress = "transcribing", .5
	if f := jobFrame(job, time.Second); f.fraction != .5 || strings.Contains(f.detail, "KiB") {
		t.Fatalf("download progress leaked into transcription: %+v", f)
	}
}

func TestRedirectedProgressIsBoundedAndHasNoTerminalCodes(t *testing.T) {
	var out bytes.Buffer
	p := newCLIProgress(&out, "auto")
	for i := 0; i < 100; i++ {
		p.Update(jobFrame(app.Job{ID: "job", State: "transcribing", Duration: 100, Processed: float64(i), Progress: float64(i) / 100}, time.Duration(i)*time.Second))
	}
	p.Update(jobFrame(app.Job{ID: "job", State: "completed", Duration: 100, Processed: 100, Progress: 1}, 100*time.Second))
	p.Close()
	text := out.String()
	if strings.ContainsAny(text, "\r\x1b") {
		t.Fatalf("terminal controls in redirected output: %q", text)
	}
	if lines := strings.Count(text, "\n"); lines > 12 || lines < 3 {
		t.Fatalf("unexpected log volume: %d lines", lines)
	}
	if !strings.Contains(text, "100% Completed") || !strings.HasSuffix(text, "\n") {
		t.Fatalf("missing completion: %q", text)
	}
}

func TestTerminalProgressEndsCleanlyAroundMessagesAndCancellation(t *testing.T) {
	var out bytes.Buffer
	p := newCLIProgress(&out, "always")
	p.width = 42
	p.Update(jobFrame(app.Job{ID: "job", State: "downloading"}, time.Second))
	if !strings.Contains(out.String(), "\r\x1b[2K[") || strings.Contains(out.String(), "%") {
		t.Fatalf("download bar: %q", out.String())
	}
	p.Message("Network retry")
	p.Update(jobFrame(app.Job{ID: "job", State: "cancelled", Duration: 100, Processed: 25, Progress: .25}, 5*time.Second))
	p.Close()
	before := out.String()
	p.Close()
	if before != out.String() || !strings.HasSuffix(before, "\n") || !strings.Contains(before, "25% Cancelled") || !strings.Contains(before, "Network retry\n") {
		t.Fatalf("unclean terminal output: %q", before)
	}
	for _, frame := range strings.Split(before, "\r\x1b[2K") {
		if line := strings.Split(frame, "\n")[0]; len(line) >= 42 {
			t.Fatalf("progress overflowed terminal: %q", line)
		}
	}
}

func TestNoProgressStillReportsMessages(t *testing.T) {
	var out bytes.Buffer
	p := newCLIProgress(&out, "never")
	p.Update(jobFrame(app.Job{State: "transcribing", Duration: 10, Progress: .5}, time.Second))
	p.Message("Tool unavailable")
	p.Close()
	if out.String() != "Tool unavailable\n" {
		t.Fatalf("progress never: %q", out.String())
	}
}

func TestRecordingProgressSeparatesCaptureFromFinishing(t *testing.T) {
	f := recordingFrame("recording", 15*time.Second, time.Minute)
	if !f.known || f.fraction != .25 || f.detail != "00:15/01:00" {
		t.Fatalf("timed recording: %+v", f)
	}
	f = recordingFrame("recording", 15*time.Second, 0)
	if f.known || f.detail != "00:15" {
		t.Fatalf("unbounded recording has percentage: %+v", f)
	}
	f = recordingFrame("stopping", 15*time.Second, time.Minute)
	if f.known || f.label != "Finishing transcript" {
		t.Fatalf("draining reported complete: %+v", f)
	}
	f = recordingFrame("completed", 15*time.Second, time.Minute)
	if !f.final || f.known || f.detail != "00:15 recorded" {
		t.Fatalf("early stop pretended to capture a full minute: %+v", f)
	}
}

func TestProgressMessagesAndUpdatesAreSerialized(t *testing.T) {
	var out bytes.Buffer
	p := newCLIProgress(&out, "always")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				p.Update(jobFrame(app.Job{ID: "job", State: "preparing"}, time.Duration(j)*time.Second))
				p.Message("Activity")
			}
		}()
	}
	wg.Wait()
	p.Close()
	if strings.Count(out.String(), "Activity\n") != 80 {
		t.Fatal("lost an activity message")
	}
}

func TestCancelledWaitFinalizesProgressWithoutLoadingEngine(t *testing.T) {
	t.Setenv("TALA_DATA_DIR", t.TempDir())
	c, err := app.NewHeadless(t.TempDir(), models.Settings{Language: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var out bytes.Buffer
	p := newCLIProgress(&out, "always")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = waitJob(ctx, c, "not-enqueued", p)
	if !errors.Is(err, context.Canceled) || !strings.Contains(out.String(), "Cancelled") || !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("cancelled wait: %v %q", err, out.String())
	}
}
