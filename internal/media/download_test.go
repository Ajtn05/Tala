package media

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDownloadProgressParsingAndBoundedOutput(t *testing.T) {
	var events []DownloadProgress
	diagnostics := &limitedBuffer{limit: 8192}
	w := &downloadOutput{emit: func(p DownloadProgress) { events = append(events, p) }, diagnostic: diagnostics}
	parts := []string{
		strings.Repeat("x", 70000) + "\nordinary message\n",
		"__TALA_DOWN", `LOAD__{"status":"downloading","downloaded":250,"total":"NA","estimate":1000,"speed":50,"eta":15}` + "\r\n",
		downloadMarker + `{"status":"downloading","downloaded":400,"total":null,"speed":"NA","eta":null}` + "\n",
		downloadMarker + "not json\n",
		downloadMarker + `{"status":"finished","downloaded":1000,"total":null}`,
	}
	for _, part := range parts {
		_, _ = w.Write([]byte(part))
	}
	w.flush()
	if len(events) != 3 {
		t.Fatalf("lost streamed events: %+v", events)
	}
	if f, known := events[0].Fraction(); !known || f != .25 || !events[0].Estimated || events[0].Speed != 50 || events[0].ETA != 15 {
		t.Fatalf("estimate: %+v", events[0])
	}
	if _, known := events[1].Fraction(); known {
		t.Fatal("invented a total for unknown-size transfer")
	}
	if f, known := events[2].Fraction(); !known || f != 1 || events[2].Estimated {
		t.Fatalf("completion: %+v", events[2])
	}
	if diagnostics.String() != "ordinary message\n" {
		t.Fatalf("progress leaked into diagnostics: %q", diagnostics.String())
	}
}

func TestDownloadProgressArrivesBeforeCommandExits(t *testing.T) {
	dir := t.TempDir()
	release := filepath.Join(dir, "continue")
	t.Setenv("TALA_TEST_CONTINUE", release)
	t.Setenv("PATH", dir)
	script := "#!/bin/sh\nprintf '%s\\n' '__TALA_DOWNLOAD__{\"status\":\"downloading\",\"downloaded\":25,\"total\":100}'\nwhile [ ! -e \"$TALA_TEST_CONTINUE\" ]; do /bin/sleep 0.01; done\nprintf '%s\\n' '__TALA_DOWNLOAD__{\"status\":\"finished\",\"downloaded\":100,\"total\":100}' >&2\n"
	if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "source.wav"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events := make(chan DownloadProgress, 4)
	done := make(chan error, 1)
	go func() {
		_, err := DownloadWithProgress(ctx, "https://example.org/audio", dir, URLAuth{}, func(p DownloadProgress) { events <- p })
		done <- err
	}()
	select {
	case p := <-events:
		if p.Downloaded != 25 {
			t.Fatalf("initial transfer: %+v", p)
		}
	case <-ctx.Done():
		t.Fatal("progress was buffered until command exit")
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if p := <-events; p.Status != "finished" {
		t.Fatalf("stderr progress was lost: %+v", p)
	}
}

func TestDownloadProgressCancellationPreservesContextError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	script := "#!/bin/sh\nprintf '%s\\n' '__TALA_DOWNLOAD__{\"status\":\"downloading\",\"downloaded\":25,\"total\":100}'\n/bin/sleep 10\n"
	if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := DownloadWithProgress(ctx, "https://example.org/audio", dir, URLAuth{}, func(DownloadProgress) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestInstalledYTDLPReportsRealLocalTransferProgress(t *testing.T) {
	if _, err := Tool("yt-dlp"); err != nil {
		t.Skip(err)
	}
	data, err := os.ReadFile(synthetic(t, 63))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method == http.MethodHead {
			return
		}
		for offset := 0; offset < len(data); offset += 32768 {
			if _, err := w.Write(data[offset:min(offset+32768, len(data))]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var events []DownloadProgress
	path, err := DownloadWithProgress(ctx, server.URL+"/fixture.wav", t.TempDir(), URLAuth{}, func(p DownloadProgress) { events = append(events, p) })
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != int64(len(data)) {
		t.Fatalf("download changed: %v %v", info, err)
	}
	intermediate := false
	for _, p := range events {
		if p.Status == "downloading" && p.Downloaded > 0 && p.Downloaded < int64(len(data)) {
			intermediate = true
		}
	}
	if !intermediate || len(events) < 2 || events[len(events)-1].Status != "finished" {
		t.Fatalf("real progress missing: %+v", events)
	}
}
