package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
)

type DownloadProgress struct {
	Status     string  `json:"status,omitempty"`
	Downloaded int64   `json:"downloaded,omitempty"`
	Total      int64   `json:"total,omitempty"`
	Estimated  bool    `json:"estimated,omitempty"`
	Speed      float64 `json:"speed,omitempty"`
	ETA        float64 `json:"eta,omitempty"`
}

func (p DownloadProgress) Fraction() (float64, bool) {
	if p.Total <= 0 {
		return 0, false
	}
	if p.Status == "finished" {
		return 1, true
	}
	return max(0, min(.999, float64(p.Downloaded)/float64(p.Total))), true
}

const downloadMarker = "__TALA_DOWNLOAD__"
// Explicit numeric defaults keep unavailable fields valid JSON (yt-dlp's
// default NA placeholder is unquoted even with the JSON conversion).
const downloadTemplate = `download:` + downloadMarker + `{"status":%(progress.status)j,"downloaded":%(progress.downloaded_bytes|0)j,"total":%(progress.total_bytes|0)j,"estimate":%(progress.total_bytes_estimate|0)j,"speed":%(progress.speed|0)j,"eta":%(progress.eta|0)j}`

func parseDownloadProgress(line []byte) (DownloadProgress, bool) {
	if !bytes.HasPrefix(line, []byte(downloadMarker)) {
		return DownloadProgress{}, false
	}
	var event struct {
		Status                                  string `json:"status"`
		Downloaded, Total, Estimate, Speed, ETA json.RawMessage
	}
	if json.Unmarshal(line[len(downloadMarker):], &event) != nil || (event.Status != "downloading" && event.Status != "finished") {
		return DownloadProgress{}, false
	}
	// yt-dlp renders unavailable fields as null or "NA", depending on version.
	number := func(raw json.RawMessage) float64 {
		n, err := strconv.ParseFloat(strings.Trim(string(raw), `"`), 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n >= math.MaxInt64 {
			return 0
		}
		return n
	}
	p := DownloadProgress{Status: event.Status, Downloaded: int64(number(event.Downloaded)), Total: int64(number(event.Total)), Speed: number(event.Speed), ETA: number(event.ETA)}
	if p.Total == 0 {
		p.Total = int64(number(event.Estimate))
		p.Estimated = p.Total > 0
	}
	if p.Status == "finished" && p.Downloaded > 0 {
		p.Total, p.Estimated, p.ETA = p.Downloaded, false, 0
	}
	return p, true
}

// Keep subprocess output bounded while delivering newline-delimited progress
// immediately. Long or malformed lines cannot stop the downloader's pipe drain.
type downloadOutput struct {
	pending    []byte
	dropping   bool
	emit       func(DownloadProgress)
	diagnostic *limitedBuffer
}

func (w *downloadOutput) Write(data []byte) (int, error) {
	n := len(data)
	for _, b := range data {
		if b == '\n' {
			w.flush()
			w.dropping = false
		} else if !w.dropping {
			if len(w.pending) >= 64*1024 {
				w.pending = nil
				w.dropping = true
			} else {
				w.pending = append(w.pending, b)
			}
		}
	}
	return n, nil
}
func (w *downloadOutput) flush() {
	if w.dropping || len(w.pending) == 0 {
		return
	}
	line := bytes.TrimSpace(w.pending)
	if p, ok := parseDownloadProgress(line); ok {
		w.emit(p)
	} else if w.diagnostic != nil && !bytes.HasPrefix(line, []byte(downloadMarker)) {
		_, _ = w.diagnostic.Write(line)
		_, _ = w.diagnostic.Write([]byte{'\n'})
	}
	w.pending = w.pending[:0]
}

func streamDownload(ctx context.Context, args []string, progress func(DownloadProgress)) ([]byte, error) {
	cmd, err := command(ctx, "yt-dlp", args...)
	if err != nil {
		return nil, err
	}
	var mu sync.Mutex
	emit := func(p DownloadProgress) {
		if progress != nil {
			mu.Lock()
			defer mu.Unlock()
			progress(p)
		}
	}
	diagnostic := &limitedBuffer{limit: 8192}
	stdout := &downloadOutput{emit: emit}
	stderr := &downloadOutput{emit: emit, diagnostic: diagnostic}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	stdout.flush()
	stderr.flush()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("yt-dlp: %w: %s", err, diagnostic.String())
	}
	return nil, nil
}
