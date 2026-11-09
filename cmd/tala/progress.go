package main

import (
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"tala/internal/app"
)

// Progress always goes to stderr. Redirected stderr receives occasional plain
// lines; terminals get a single updating bar. No inference progress is invented.
type cliProgress struct {
	mu          sync.Mutex
	out         io.Writer
	enabled     bool
	interactive bool
	width       int
	lastLine    string
	lastKey     string
	lineOpen    bool
}

type progressFrame struct {
	id, state, label, detail string
	fraction                 float64
	known, final             bool
	step                     int
}

func newCLIProgress(out io.Writer, mode string) *cliProgress {
	width, terminal := terminalSize(out)
	if width == 0 {
		width = 80
	}
	return &cliProgress{out: out, enabled: mode != "never", width: width,
		interactive: mode == "always" || (terminal && os.Getenv("TERM") != "dumb")}
}

// Both supported platforms expose TIOCGWINSZ; regular files and /dev/null fail
// the ioctl and therefore never receive terminal escape sequences in auto mode.
func terminalSize(out io.Writer) (int, bool) {
	f, ok := out.(*os.File)
	if !ok {
		return 0, false
	}
	var size struct{ rows, cols, x, y uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&size)))
	return int(size.cols), errno == 0
}

func progressClock(seconds float64) string {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		seconds = 0
	}
	n := int64(seconds)
	if n >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", n/3600, n/60%60, n%60)
	}
	return fmt.Sprintf("%02d:%02d", n/60, n%60)
}

func progressBytes(n float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", n, units[i])
	}
	return fmt.Sprintf("%.1f %s", n, units[i])
}

func jobFrame(job app.Job, elapsed time.Duration) progressFrame {
	labels := map[string]string{"waiting": "Waiting", "downloading": "Downloading", "preparing": "Preparing audio", "transcribing": "Transcribing", "paused": "Paused for recording", "completed": "Completed", "failed": "Failed", "cancelled": "Cancelled"}
	f := progressFrame{id: job.ID, state: job.State, label: labels[job.State], step: int(elapsed / (100 * time.Millisecond))}
	if f.label == "" {
		f.label = "Preparing audio"
	}
	f.final = job.State == "completed" || job.State == "failed" || job.State == "cancelled"
	if job.State == "downloading" || ((job.State == "failed" || job.State == "cancelled") && job.SessionID == "" && job.Download.Status != "") {
		f.fraction, f.known = job.Download.Fraction()
		if f.final {
			f.fraction = min(.99, f.fraction)
		}
		if job.Download.Downloaded > 0 {
			f.detail = progressBytes(float64(job.Download.Downloaded))
		}
		if job.Download.Total > 0 {
			if f.detail == "" {
				f.detail = "0 B"
			}
			f.detail += "/"
			if job.Download.Estimated {
				f.detail += "~"
			}
			f.detail += progressBytes(float64(job.Download.Total))
		}
		if job.Download.Speed > 0 {
			f.detail += " " + progressBytes(job.Download.Speed) + "/s"
		}
		if job.Download.ETA > 0 {
			f.detail += " ETA " + progressClock(job.Download.ETA)
		}
		if f.detail == "" {
			f.detail = "(" + progressClock(elapsed.Seconds()) + " elapsed)"
		}
		return f
	}
	f.known = job.Duration > 0 && (job.State == "transcribing" || job.State == "paused" || f.final)
	f.fraction = job.Progress
	if job.State == "completed" {
		f.fraction = 1
	} else {
		f.fraction = math.Min(.99, f.fraction)
	}
	if f.known {
		f.detail = progressClock(job.Processed) + "/" + progressClock(job.Duration) + " "
	} else if job.Processed > 0 {
		f.detail = progressClock(job.Processed) + " processed "
	}
	f.detail += "(" + progressClock(elapsed.Seconds()) + " elapsed)"
	return f
}

func recordingFrame(state string, elapsed, target time.Duration) progressFrame {
	f := progressFrame{id: "recording", state: state, step: int(elapsed / (100 * time.Millisecond))}
	switch state {
	case "recording":
		f.label = "Recording"
		f.known = target > 0
		if f.known {
			f.fraction = float64(elapsed) / float64(target)
		}
		f.detail = progressClock(elapsed.Seconds())
		if target > 0 {
			f.detail += "/" + progressClock(target.Seconds())
		}
	case "stopping":
		f.label, f.detail = "Finishing transcript", progressClock(elapsed.Seconds())+" recorded"
	case "completed":
		f.label, f.detail, f.final = "Recording saved", progressClock(elapsed.Seconds())+" recorded", true
	case "failed":
		f.label, f.final = "Recording failed", true
	default:
		f.label = "Preparing recording"
	}
	return f
}

func (p *cliProgress) Update(f progressFrame) {
	if p == nil || !p.enabled {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if math.IsNaN(f.fraction) || math.IsInf(f.fraction, 0) {
		f.fraction = 0
	}
	f.fraction = max(0, min(1, f.fraction))
	percent := int(f.fraction * 100)
	// Avoid dumping animation frames or a new line for every one-second tick to logs.
	key := fmt.Sprintf("%s/%s/%t/%d/%t", f.id, f.state, f.known, percent/10, f.final)
	if !p.interactive && key == p.lastKey {
		return
	}
	p.lastKey = key
	barWidth := max(4, min(20, p.width/4))
	var bar string
	if f.known {
		filled := int(f.fraction * float64(barWidth))
		bar = "[" + strings.Repeat("=", filled) + strings.Repeat(" ", barWidth-filled) + fmt.Sprintf("] %3d%% ", percent)
	} else if !f.final && p.interactive {
		at := f.step % barWidth
		bar = "[" + strings.Repeat(" ", at) + ">" + strings.Repeat(" ", barWidth-at-1) + "] "
	}
	line := strings.TrimSpace(bar + f.label + " " + f.detail)
	if p.interactive {
		if len(line) >= p.width {
			line = line[:max(1, p.width-1)]
		}
		if line == p.lastLine && p.lineOpen && !f.final {
			return
		}
		fmt.Fprint(p.out, "\r\x1b[2K", line)
		p.lineOpen = true
		if f.final {
			fmt.Fprintln(p.out)
			p.lineOpen = false
		}
	} else {
		fmt.Fprintln(p.out, line)
	}
	p.lastLine = line
}

// Message keeps error/activity messages on their own lines between bar updates.
func (p *cliProgress) Message(message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lineOpen {
		fmt.Fprint(p.out, "\r\x1b[2K")
		p.lineOpen = false
	}
	fmt.Fprintln(p.out, message)
}

func (p *cliProgress) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lineOpen {
		fmt.Fprintln(p.out)
		p.lineOpen = false
	}
}
