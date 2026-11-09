// Package app owns one recording controller, one inference engine, and a sequential queue.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"tala/internal/audio"
	"tala/internal/media"
	"tala/internal/models"
	"tala/internal/recording"
	"tala/internal/session"
	"tala/internal/transcriber"
)

type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}
type Job struct {
	ID          string                 `json:"id"`
	Title       string                 `json:"title"`
	CustomTitle string                 `json:"customTitle,omitempty"`
	Source      string                 `json:"source"`
	Kind        string                 `json:"kind"`
	State       string                 `json:"state"`
	Progress    float64                `json:"progress"`
	Duration    float64                `json:"duration"`
	Processed   float64                `json:"processed"`
	Elapsed     float64                `json:"elapsed"`
	Error       string                 `json:"error,omitempty"`
	SessionID   string                 `json:"sessionId,omitempty"`
	Retain      bool                   `json:"retain"`
	Range       media.TimeRange        `json:"range,omitempty"`
	Download    media.DownloadProgress `json:"download,omitempty"`
}

type ImportOptions struct {
	Retain bool
	Range  media.TimeRange
	Title  string
}

func (j Job) importTitle(sourceTitle string) string {
	if j.CustomTitle != "" {
		return j.CustomTitle
	}
	return sourceTitle
}

type Live struct {
	Active    bool       `json:"active"`
	State     string     `json:"state"`
	Mode      audio.Mode `json:"mode"`
	SessionID string     `json:"sessionId"`
	Started   time.Time  `json:"started"`
	Error     string     `json:"error,omitempty"`
}
type Controller struct {
	mu          sync.Mutex
	cond        *sync.Cond
	Store       *session.Store
	Models      models.Manager
	engineMu    sync.Mutex
	engine      *transcriber.Transcriber
	tensorMode  string
	Model       models.Status
	live        Live
	liveCancel  context.CancelFunc
	liveDone    chan struct{}
	jobs        []Job
	cancelJob   context.CancelFunc
	activeJob   string
	ctx         context.Context
	cancel      context.CancelFunc
	wake        chan struct{}
	Events      chan Event
	workerDone  chan struct{}
	resync      bool
	modelBusy   bool
	settingsMu  sync.Mutex
	recognition *models.Settings
	headless    bool
	ownedJobs   map[string]bool
}

func New(output string) (*Controller, error) { return newController(output, models.Default()) }

// NewHeadless applies recognition options to this invocation only. Existing
// waiting jobs remain in the queue until the dashboard next resumes them.
func NewHeadless(output string, settings models.Settings) (*Controller, error) {
	return newControllerWithOptions(output, models.Default(), &settings)
}
func newController(output string, manager models.Manager) (*Controller, error) {
	return newControllerWithOptions(output, manager, nil)
}
func newControllerWithOptions(output string, manager models.Manager, recognition *models.Settings) (*Controller, error) {
	c := &Controller{Store: &session.Store{Dir: output}, Models: manager, wake: make(chan struct{}, 1), workerDone: make(chan struct{}), Events: make(chan Event, 128), recognition: recognition, headless: recognition != nil, ownedJobs: map[string]bool{}}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.cond = sync.NewCond(&c.mu)
	if e := os.MkdirAll(output, 0700); e != nil {
		return nil, e
	}
	if e := c.Store.ImportLegacy(); e != nil {
		return nil, e
	}
	b, e := os.ReadFile(c.queuePath())
	if e == nil {
		if e = json.Unmarshal(b, &c.jobs); e != nil {
			return nil, fmt.Errorf("read queue: %w", e)
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	// Interrupted jobs keep their partial session and restart from the original source on retry.
	for i := range c.jobs {
		if c.jobs[i].State != "completed" && c.jobs[i].State != "failed" && c.jobs[i].State != "cancelled" {
			c.jobs[i].State = "waiting"
			c.jobs[i].Progress = 0
			c.jobs[i].Download = media.DownloadProgress{}
		}
	}
	if items, e := c.Store.List(); e == nil {
		for _, v := range items {
			if v.Status == "processing" || v.Status == "recording" {
				_ = c.Store.Update(v.ID, func(v *session.Session) error { v.Status = "interrupted"; return nil })
			}
		}
	}
	go c.worker()
	go c.Discover()
	c.signal()
	return c, nil
}
func (c *Controller) Emit(kind string, data any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.emitLocked(kind, data)
}
func (c *Controller) emitLocked(kind string, data any) {
	if c.resync {
		select {
		case c.Events <- Event{"resync", nil}:
			c.resync = false
		default:
			return
		}
	}
	select {
	case c.Events <- Event{kind, data}:
	default:
		c.resync = true
	}
}
func (c *Controller) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}
func (c *Controller) queuePath() string { return filepath.Join(c.Store.Dir, "queue.json") }
func (c *Controller) saveJobsLocked() error {
	b, e := json.MarshalIndent(c.jobs, "", "  ")
	if e != nil {
		return e
	}
	return models.AtomicWrite(c.queuePath(), b)
}
func (c *Controller) Snapshot() any {
	c.mu.Lock()
	live, model, tensorAcceleration := c.live, c.Model, c.tensorMode
	jobs := append([]Job{}, c.jobs...)
	c.mu.Unlock()
	items, e := c.Store.List()
	if e != nil {
		c.Emit("error", e.Error())
	}
	return map[string]any{
		"live": live, "model": model, "jobs": jobs, "sessions": items,
		"settings": c.Models.Settings(), "dependencies": media.Dependencies(), "output": c.Store.Dir,
		"tensorAcceleration": tensorAcceleration,
	}
}
func (c *Controller) Discover() {
	c.mu.Lock()
	if c.modelBusy {
		c.mu.Unlock()
		return
	}
	c.modelBusy = true
	c.Model = models.Status{State: "checking", Detail: "Looking for an existing model and verifying its checksum…"}
	c.emitLocked("model", c.Model)
	c.mu.Unlock()
	status, e := c.Models.Discover(c.ctx)
	if e != nil && status.Detail == "" {
		status.Detail = e.Error()
	}
	c.mu.Lock()
	c.Model = status
	c.modelBusy = false
	c.emitLocked("model", status)
	c.mu.Unlock()
}
func (c *Controller) ensureEngine() error {
	c.engineMu.Lock()
	defer c.engineMu.Unlock()
	if c.engine != nil {
		return nil
	}
	c.mu.Lock()
	status := c.Model
	c.mu.Unlock()
	if status.Path == "" {
		return errors.New("wait for model verification, or choose a valid model in Settings")
	}
	c.Emit("activity", "Loading Whisper…")
	settings := c.recognitionSettings()
	engine, e := transcriber.New(transcriber.Config{ModelPath: status.Path, Language: settings.Language, Prompt: settings.Prompt})
	if e != nil {
		return e
	}
	c.engine = engine
	activity := "Model ready"
	if runtime.GOOS == "darwin" {
		activity = "Tensor acceleration inactive"
		if transcriber.TensorAccelerationActive() {
			activity = "Tensor acceleration active"
		}
		c.mu.Lock()
		c.tensorMode = activity
		c.emitLocked("tensorAcceleration", activity)
		c.mu.Unlock()
	}
	c.Emit("activity", activity)
	return nil
}
func (c *Controller) Transcribe(ctx context.Context, samples []float32) ([]transcriber.Segment, error) {
	if e := c.ensureEngine(); e != nil {
		return nil, e
	}
	c.engineMu.Lock()
	defer c.engineMu.Unlock()
	s := c.recognitionSettings()
	c.engine.SetOptions(s.Language, s.Prompt)
	return c.engine.Transcribe(ctx, samples)
}
func (c *Controller) recognitionSettings() models.Settings {
	if c.recognition != nil {
		return *c.recognition
	}
	return c.Models.Settings()
}
func (c *Controller) SetMode(mode audio.Mode) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.live.Active {
		return errors.New("stop recording before changing audio sources")
	}
	if mode < 0 || mode > 2 {
		return errors.New("invalid audio source")
	}
	c.live.Mode = mode
	c.emitLocked("live", c.live)
	return nil
}
func (c *Controller) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.live.Active {
		return errors.New("a recording is already active")
	}
	if c.Model.Path == "" {
		return errors.New("a verified Whisper model is required; open Settings")
	}
	source := []string{"Microphone + System Audio", "Microphone", "System Audio"}[c.live.Mode]
	v, e := c.Store.Create("live", "Recording · "+time.Now().Format("Jan 2, 3:04 PM"), source)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.liveCancel = cancel
	c.liveDone = make(chan struct{})
	c.live = Live{Active: true, State: "loading", Mode: c.live.Mode, SessionID: v.ID, Started: time.Now()}
	c.emitLocked("live", c.live)
	c.emitLocked("sessions", nil)
	go c.runLive(ctx, v.ID, c.live.Mode, c.liveDone)
	return nil
}
func (c *Controller) append(id string, seg transcriber.Segment) error {
	v, e := c.Store.Append(id, session.Segment{Text: seg.Text, Start: seg.Start.Seconds(), End: seg.End.Seconds(), Source: seg.Source})
	if e == nil {
		c.Emit("segment", v)
	}
	return e
}
func (c *Controller) runLive(ctx context.Context, id string, mode audio.Mode, done chan struct{}) {
	var duration time.Duration
	err := c.ensureEngine()
	if err == nil && ctx.Err() == nil {
		c.mu.Lock()
		c.live.State = "recording"
		c.emitLocked("live", c.live)
		c.mu.Unlock()
		input := func(capture context.Context, emit func(audio.Frame) error) error {
			return audio.Capture(capture, mode, emit)
		}
		duration, err = recording.Run(ctx, input, c.Transcribe, func(seg transcriber.Segment) error {
			return c.append(id, seg)
		})
	}
	if err == nil {
		err = c.saveRecordingText(id)
	}
	status := "completed"
	if err != nil {
		status = "failed"
		log.Printf("recording: %v", err)
	}
	if finishErr := c.finishSession(id, status, duration.Seconds()); finishErr != nil {
		err = errors.Join(err, finishErr)
		status = "failed"
	}
	c.mu.Lock()
	c.live.Active = false
	c.live.State = status
	c.liveCancel = nil
	if err != nil {
		c.live.Error = err.Error()
	}
	c.emitLocked("live", c.live)
	c.emitLocked("sessions", nil)
	c.cond.Broadcast()
	close(done)
	c.mu.Unlock()
	c.signal()
}
func (c *Controller) saveRecordingText(id string) error {
	v, err := c.Store.Get(id)
	if err != nil || len(v.Segments) == 0 {
		return err
	}
	text, err := session.Export(v, "txt", "sources")
	if err != nil {
		return err
	}
	path := filepath.Join(c.Store.Dir, "transcript_"+v.ID+".txt")
	// Register the export before publishing it so restart recovery will not
	// mistake it for a separate legacy transcript.
	if err := c.Store.Update(id, func(v *session.Session) error {
		v.LegacyPath = path
		return nil
	}); err != nil {
		return err
	}
	return models.AtomicWrite(path, []byte(text))
}

func (c *Controller) Stop() {
	c.mu.Lock()
	if c.liveCancel != nil {
		c.live.State = "stopping"
		c.liveCancel()
		c.emitLocked("live", c.live)
	}
	c.mu.Unlock()
}
func (c *Controller) finishSession(id, status string, duration float64) error {
	v, e := c.Store.Get(id)
	if e != nil {
		return e
	}
	preview := ""
	if len(v.Segments) > 0 {
		preview = v.Segments[0].Text
	}
	return c.Store.Update(id, func(v *session.Session) error {
		v.Status = status
		v.Duration = duration
		v.Preview = preview
		return nil
	})
}
func (c *Controller) Add(source, kind string, retain bool) error {
	_, err := c.Enqueue(source, kind, retain)
	return err
}
func (c *Controller) Enqueue(source, kind string, retain bool) (Job, error) {
	return c.EnqueueRange(source, kind, retain, media.TimeRange{})
}
func (c *Controller) EnqueueRange(source, kind string, retain bool, selection media.TimeRange) (Job, error) {
	return c.EnqueueWithOptions(source, kind, ImportOptions{Retain: retain, Range: selection})
}
func (c *Controller) EnqueueWithOptions(source, kind string, options ImportOptions) (Job, error) {
	selection := options.Range
	title := strings.TrimSpace(options.Title)
	if len([]rune(title)) > 200 || strings.ContainsAny(title, "\x00\r\n") {
		return Job{}, errors.New("use a transcript title of at most 200 characters on one line")
	}
	if err := selection.Validate(0); err != nil {
		return Job{}, err
	}
	if kind == "url" {
		if e := media.ValidateURL(source); e != nil {
			return Job{}, e
		}
	} else {
		abs, e := filepath.Abs(source)
		if e != nil {
			return Job{}, e
		}
		info, e := os.Stat(abs)
		if e != nil {
			return Job{}, e
		}
		if !info.Mode().IsRegular() {
			return Job{}, errors.New("select a media file")
		}
		source = abs
		kind = "file"
	}
	if err := media.Dependencies().Require(kind); err != nil {
		return Job{}, err
	}
	if kind == "file" && selection != (media.TimeRange{}) {
		meta, err := media.Probe(c.ctx, source)
		if err != nil {
			return Job{}, err
		}
		if err := selection.Validate(meta.Duration); err != nil {
			return Job{}, err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	job := Job{ID: session.ID(), Title: filepath.Base(source), CustomTitle: title, Source: source, Kind: kind, State: "waiting", Retain: options.Retain, Range: selection}
	job.Title = job.importTitle(job.Title)
	c.jobs = append(c.jobs, job)
	if e := c.saveJobsLocked(); e != nil {
		c.jobs = c.jobs[:len(c.jobs)-1]
		return Job{}, e
	}
	if c.ownedJobs == nil {
		c.ownedJobs = map[string]bool{}
	}
	c.ownedJobs[job.ID] = true
	c.emitLocked("jobs", append([]Job{}, c.jobs...))
	c.signal()
	return job, nil
}
func (c *Controller) Jobs() []Job {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Job{}, c.jobs...)
}

// ClearQueue forgets queued work and cancels the active import. Saved sessions
// and original media are independent of the queue and remain available.
func (c *Controller) ClearQueue() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	previous := c.jobs
	c.jobs = []Job{}
	if err := c.saveJobsLocked(); err != nil {
		c.jobs = previous
		return err
	}
	if c.cancelJob != nil {
		c.cancelJob()
	}
	for _, job := range previous {
		delete(c.ownedJobs, job.ID)
	}
	c.cond.Broadcast()
	c.emitLocked("jobs", []Job{})
	return nil
}

func (c *Controller) changeJob(id string, fn func(*Job)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.jobs {
		if c.jobs[i].ID == id {
			fn(&c.jobs[i])
			break
		}
	}
	if e := c.saveJobsLocked(); e != nil {
		c.emitLocked("error", e.Error())
	}
	c.emitLocked("jobs", append([]Job{}, c.jobs...))
}

// Transfer ticks are transient. State transitions persist the latest statistics;
// avoiding a disk sync on each tick keeps progress delivery light during downloads.
func (c *Controller) updateDownload(id string, progress media.DownloadProgress, elapsed float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.jobs {
		if c.jobs[i].ID == id {
			c.jobs[i].Download = progress
			c.jobs[i].Elapsed = elapsed
			c.emitLocked("jobs", append([]Job{}, c.jobs...))
			return
		}
	}
}
func (c *Controller) CancelJob(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.jobs {
		j := &c.jobs[i]
		if j.ID != id {
			continue
		}
		if j.State == "completed" || j.State == "failed" || j.State == "cancelled" {
			return nil
		}
		if c.activeJob == id {
			c.cancelJob()
		} else {
			j.State = "cancelled"
		}
		c.cond.Broadcast()
		if e := c.saveJobsLocked(); e != nil {
			return e
		}
		c.emitLocked("jobs", append([]Job{}, c.jobs...))
		return nil
	}
	return errors.New("job not found")
}
func (c *Controller) Retry(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.jobs {
		j := &c.jobs[i]
		if j.ID == id {
			if j.State != "failed" && j.State != "cancelled" {
				return errors.New("only failed or cancelled jobs can be retried")
			}
			j.State = "waiting"
			j.Error = ""
			j.Progress = 0
			j.Processed = 0
			j.Elapsed = 0
			j.Download = media.DownloadProgress{}
			if e := c.saveJobsLocked(); e != nil {
				return e
			}
			c.emitLocked("jobs", append([]Job{}, c.jobs...))
			c.signal()
			return nil
		}
	}
	return errors.New("job not found")
}
func (c *Controller) waitBatch(ctx context.Context, id string) error {
	stop := context.AfterFunc(ctx, func() { c.mu.Lock(); c.cond.Broadcast(); c.mu.Unlock() })
	defer stop()
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.live.Active && ctx.Err() == nil {
		for i := range c.jobs {
			if c.jobs[i].ID == id {
				c.jobs[i].State = "paused"
			}
		}
		c.emitLocked("jobs", append([]Job{}, c.jobs...))
		c.cond.Wait()
	}
	return ctx.Err()
}
func (c *Controller) worker() {
	defer close(c.workerDone)
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.wake:
		}
		for {
			c.mu.Lock()
			var job *Job
			for i := range c.jobs {
				if c.jobs[i].State == "waiting" && (!c.headless || c.ownedJobs[c.jobs[i].ID]) {
					v := c.jobs[i]
					job = &v
					break
				}
			}
			if job == nil {
				c.mu.Unlock()
				break
			}
			ctx, cancel := context.WithCancel(c.ctx)
			c.activeJob = job.ID
			c.cancelJob = cancel
			c.mu.Unlock()
			err := c.process(ctx, *job)
			cancel()
			c.changeJob(job.ID, func(j *Job) {
				if err != nil {
					if errors.Is(err, context.Canceled) {
						j.State = "cancelled"
					} else {
						j.State = "failed"
						j.Error = err.Error()
					}
				} else {
					j.State = "completed"
					j.Progress = 1
				}
			})
			c.mu.Lock()
			c.activeJob = ""
			c.cancelJob = nil
			c.mu.Unlock()
			c.Emit("sessions", nil)
			if c.ctx.Err() != nil {
				return
			}
		}
	}
}
func (c *Controller) process(ctx context.Context, job Job) error {
	if err := job.Range.Validate(0); err != nil {
		return err
	}
	if err := media.Dependencies().Require(job.Kind); err != nil {
		return err
	}
	if e := c.waitBatch(ctx, job.ID); e != nil {
		return e
	}
	started := time.Now()
	path := job.Source
	var meta media.Metadata
	var err error
	if job.Kind == "url" {
		auth := c.URLAuth()
		c.changeJob(job.ID, func(j *Job) { j.State = "preparing"; j.Download = media.DownloadProgress{} })
		meta, err = media.FetchWithAuth(ctx, job.Source, auth)
		if err != nil {
			return err
		}
		if err := job.Range.Validate(meta.Duration); err != nil {
			return err
		}
		meta.Title = job.importTitle(meta.Title)
		c.changeJob(job.ID, func(j *Job) {
			j.Title = meta.Title
			j.Duration = job.Range.Duration(meta.Duration)
			j.State = "downloading"
		})
		tmp, e := os.MkdirTemp("", "tala-media-*")
		if e != nil {
			return e
		}
		defer os.RemoveAll(tmp)
		path, err = media.DownloadWithProgress(ctx, job.Source, tmp, auth, func(p media.DownloadProgress) {
			c.updateDownload(job.ID, p, time.Since(started).Seconds())
		})
		if err != nil {
			return err
		}
	}
	c.changeJob(job.ID, func(j *Job) { j.State = "preparing" })
	probed, e := media.Probe(ctx, path)
	if e != nil {
		return e
	}
	if meta.Title == "" {
		meta = probed
	}
	meta.Title = job.importTitle(meta.Title)
	if meta.Duration <= 0 {
		meta.Duration = probed.Duration
	}
	if err := job.Range.Validate(probed.Duration); err != nil {
		return err
	}
	selectedDuration := job.Range.Duration(probed.Duration)
	v, e := c.Store.Create(job.Kind, meta.Title, job.Source)
	if e != nil {
		return e
	}
	mediaPath := path
	if job.Kind == "url" && !job.Retain {
		mediaPath = ""
	}
	if job.Kind == "url" && job.Retain {
		dir := filepath.Join(c.Store.Dir, "media", v.ID)
		if e = os.MkdirAll(dir, 0700); e != nil {
			return e
		}
		dest := filepath.Join(dir, filepath.Base(path))
		if e = copyFile(ctx, path, dest); e != nil {
			return e
		}
		path = dest
		mediaPath = dest
	}
	if e = c.Store.Update(v.ID, func(v *session.Session) error {
		v.MediaPath = mediaPath
		v.Duration = selectedDuration
		v.Range = job.Range
		return nil
	}); e != nil {
		return e
	}
	c.changeJob(job.ID, func(j *Job) { j.Title = meta.Title; j.Duration = selectedDuration; j.SessionID = v.ID })
	c.Emit("sessions", nil)
	processed := 0.0
	err = media.StreamRange(ctx, path, job.Range, func(samples []float32, offset float64) error {
		if e := c.waitBatch(ctx, job.ID); e != nil {
			return e
		}
		c.changeJob(job.ID, func(j *Job) { j.State = "transcribing" })
		segs, e := c.Transcribe(ctx, samples)
		if e != nil {
			return e
		}
		end := offset + float64(len(samples))/16000
		for _, seg := range segs {
			seg.Source = "Imported Audio"
			startSec := max(offset, min(end, offset+seg.Start.Seconds()))
			endSec := max(startSec, min(end, offset+seg.End.Seconds()))
			seg.Start = time.Duration(startSec * float64(time.Second))
			seg.End = time.Duration(endSec * float64(time.Second))
			if strings.TrimSpace(seg.Text) != "" {
				if e = c.append(v.ID, seg); e != nil {
					return e
				}
			}
		}
		processed = end - job.Range.Start
		c.changeJob(job.ID, func(j *Job) {
			j.Processed = processed
			j.Elapsed = time.Since(started).Seconds()
			if selectedDuration > 0 {
				j.Progress = min(.999, processed/selectedDuration)
			}
		})
		return ctx.Err()
	})
	status := "completed"
	if err != nil {
		status = "failed"
		if ctx.Err() != nil {
			status = "cancelled"
		}
	}
	if e = c.finishSession(v.ID, status, processed); err == nil {
		err = e
	}
	return err
}
func copyFile(ctx context.Context, src, dest string) error {
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	ok := false
	defer func() {
		out.Close()
		if !ok {
			os.Remove(dest)
		}
	}()
	buf := make([]byte, 1024*1024)
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		n, readErr := in.Read(buf)
		if n > 0 {
			if _, e = out.Write(buf[:n]); e != nil {
				return e
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return readErr
			}
			break
		}
	}
	if e = out.Sync(); e != nil {
		return e
	}
	ok = true
	return nil
}
func (c *Controller) Configure(s models.Settings) error {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	if err := s.URLAuth.Validate(); err != nil {
		return err
	}
	if s.Language != "auto" && s.Language != "en" && s.Language != "tl" {
		return errors.New("choose automatic, English, or Filipino")
	}
	c.mu.Lock()
	if c.live.Active || c.activeJob != "" || c.modelBusy {
		c.mu.Unlock()
		return errors.New("wait for active work before changing settings")
	}
	c.modelBusy = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.modelBusy = false; c.mu.Unlock() }()
	old := c.Models.Settings()
	if s.ModelPath != old.ModelPath && s.ModelPath != "" && s.ModelPath != c.ModelState().Path {
		if e := models.Verify(c.ctx, s.ModelPath); e != nil {
			return e
		}
		c.engineMu.Lock()
		if c.engine != nil {
			c.engineMu.Unlock()
			return errors.New("restart the app before switching a loaded model path")
		}
		c.engineMu.Unlock()
	}
	if e := c.Models.Save(s); e != nil {
		return e
	}
	status, e := c.Models.Discover(c.ctx)
	c.mu.Lock()
	c.Model = status
	c.emitLocked("model", status)
	c.emitLocked("settings", s)
	c.mu.Unlock()
	if s.ModelPath == old.ModelPath && s.ModelDir == old.ModelDir {
		return nil
	}
	return e
}

// URLAuth uses invocation overrides for the CLI and saved settings for the dashboard.
func (c *Controller) URLAuth() media.URLAuth { return c.recognitionSettings().URLAuth }

// SetURLAuth lets a failed import be repaired without requiring a model or idle queue.
// Running imports keep the auth selection captured at the start of process.
func (c *Controller) SetURLAuth(auth media.URLAuth) error {
	if err := auth.Validate(); err != nil {
		return err
	}
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	s := c.Models.Settings()
	s.URLAuth = auth
	if err := c.Models.Save(s); err != nil {
		return err
	}
	c.Emit("settings", s)
	return nil
}

// SetMenuBarIcon can be changed during recording and without a verified model.
func (c *Controller) SetMenuBarIcon(enabled bool) error {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	s := c.Models.Settings()
	s.MenuBarIcon = enabled
	if err := c.Models.Save(s); err != nil {
		return err
	}
	c.Emit("settings", s)
	return nil
}
func (c *Controller) DownloadModel() error {
	c.mu.Lock()
	if c.modelBusy {
		c.mu.Unlock()
		return errors.New("model verification is already running")
	}
	c.modelBusy = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.modelBusy = false; c.mu.Unlock() }()
	status, e := c.Models.Ensure(c.ctx, true, func(n int64) { c.Emit("activity", fmt.Sprintf("Downloading model: %.0f MB", float64(n)/1e6)) })
	c.mu.Lock()
	if e == nil {
		c.Model = status
		c.emitLocked("model", status)
	}
	c.mu.Unlock()
	return e
}
func (c *Controller) Delete(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.live.Active && c.live.SessionID == id {
		return errors.New("stop recording before deleting this session")
	}
	for _, j := range c.jobs {
		if j.SessionID == id && j.ID == c.activeJob {
			return errors.New("cancel processing before deleting this session")
		}
	}
	return c.Store.Delete(id)
}
func (c *Controller) Close() {
	c.cancel()
	c.Stop()
	c.mu.Lock()
	if c.cancelJob != nil {
		c.cancelJob()
	}
	c.cond.Broadcast()
	done := c.liveDone
	c.mu.Unlock()
	if done != nil {
		<-done
	}
	<-c.workerDone
	c.engineMu.Lock()
	if c.engine != nil {
		_ = c.engine.Close()
		c.engine = nil
	}
	c.engineMu.Unlock()
}

func (c *Controller) LiveState() Live           { c.mu.Lock(); defer c.mu.Unlock(); return c.live }
func (c *Controller) ModelState() models.Status { c.mu.Lock(); defer c.mu.Unlock(); return c.Model }

// AfterEvent schedules recovery even when the final event overflowed the bounded queue.
func (c *Controller) AfterEvent() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.resync {
		select {
		case c.Events <- Event{"resync", nil}:
			c.resync = false
		default:
		}
	}
}
