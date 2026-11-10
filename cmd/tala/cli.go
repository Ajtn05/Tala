package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"tala/internal/app"
	"tala/internal/audio"
	"tala/internal/media"
	"tala/internal/models"
	"tala/internal/session"
)

type inputs []string

func (v *inputs) String() string         { return strings.Join(*v, ", ") }
func (v *inputs) Set(value string) error { *v = append(*v, value); return nil }

type options struct {
	start, end, cookieBrowser, cookieProfile                             string
	cookieBrowserSet                                                     bool
	cookiesFile                                                          string
	progress, modelID                                                    string
	selection                                                            media.TimeRange
	checkDependencies, checkModel, ensureModel, record, list, singleLine bool
	input                                                                inputs
	output, model, language, prompt, source, format, textMode, export    string
	duration                                                             time.Duration
	promptSet, skipQuietSet, skipQuiet                                   bool
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
	var o options
	f := flag.NewFlagSet("tala", flag.ContinueOnError)
	f.SetOutput(stderr)
	f.Usage = func() {
		fmt.Fprintln(stderr, "Usage: tala [options] [FILE_OR_URL ...]\n\nTranscribe locally on macOS or Linux. Without a command, open the macOS dashboard.\nPut options before positional inputs. Recognition flags affect this invocation only.")
		f.PrintDefaults()
	}
	f.BoolVar(&o.checkDependencies, "check-dependencies", false, "Report FFmpeg, ffprobe and yt-dlp availability as JSON")
	f.BoolVar(&o.checkModel, "check-model", false, "Verify an existing model without downloading")
	f.BoolVar(&o.ensureModel, "ensure-model", false, "Verify a model; download only if none exists")
	f.Var(&o.input, "transcribe", "Transcribe FILE_OR_URL without the dashboard (repeatable)")
	f.StringVar(&o.start, "start", "", "Start at seconds, MM:SS, or HH:MM:SS (imports only)")
	f.StringVar(&o.end, "end", "", "Stop at this source timestamp (blank: media end; imports only)")
	f.StringVar(&o.cookieBrowser, "cookies-from-browser", "", "Use this signed-in browser for URLs; empty disables saved sign-in")
	f.StringVar(&o.cookieProfile, "browser-profile", "", "Browser profile name/path for --cookies-from-browser")
	f.StringVar(&o.cookiesFile, "cookies", "", "Local Netscape cookies.txt export for Dia or other browsers (original is preserved)")
	f.StringVar(&o.progress, "progress", "auto", "Progress on stderr: auto (terminal bar/plain log lines), always (bar), or never")
	f.StringVar(&o.output, "output", "", "Directory for saved sessions (TALA_OUTPUT_DIR overrides the platform default)")
	f.StringVar(&o.model, "model", "", "Existing supported Whisper GGML model path")
	f.StringVar(&o.modelID, "model-id", "", "Installed or downloadable model: large-v3-turbo, large-v3-turbo-q5, large-v3, small-q5")
	f.StringVar(&o.language, "language", "", "taglish, auto, en, or tl (uses the saved setting when set, otherwise taglish)")
	f.StringVar(&o.prompt, "prompt", "", "Vocabulary/context hint (not rewriting instructions; overrides saved hint)")
	f.BoolVar(&o.skipQuiet, "skip-quiet-imports", false, "Skip nearly silent import windows (overrides the saved setting)")
	f.BoolVar(&o.record, "record", false, "Record live audio until Ctrl+C or --duration expires")
	f.StringVar(&o.source, "source", "microphone", "Recording source: microphone, system, or both (Linux: microphone)")
	f.DurationVar(&o.duration, "duration", 0, "Recording duration, e.g. 30s or 5m (0: until Ctrl+C)")
	f.BoolVar(&o.list, "list", false, "List saved sessions as JSON without loading a model")
	f.StringVar(&o.export, "export", "", "Export a saved SESSION_ID to stdout without loading a model")
	f.StringVar(&o.format, "format", "txt", "Stdout format: txt, md, srt, vtt, or json")
	f.StringVar(&o.textMode, "text-mode", "clean", "Text layout: clean, continuous, timestamps, or sources")
	f.BoolVar(&o.singleLine, "single-line", false, "Join plain text into one line (alias for --text-mode continuous)")
	if err := f.Parse(args); err != nil {
		return o, err
	}
	if o.progress != "auto" && o.progress != "always" && o.progress != "never" {
		return o, errors.New("--progress must be auto, always, or never")
	}
	f.Visit(func(v *flag.Flag) {
		if v.Name == "cookies-from-browser" {
			o.cookieBrowserSet = true
		}
		if v.Name == "prompt" {
			o.promptSet = true
		}
		if v.Name == "skip-quiet-imports" {
			o.skipQuietSet = true
		}
	})
	o.input = append(o.input, f.Args()...)
	var rangeErr error
	o.selection, rangeErr = media.ParseRange(o.start, o.end)
	if rangeErr != nil {
		return o, rangeErr
	}
	if (o.start != "" || o.end != "") && len(o.input) == 0 {
		return o, errors.New("--start and --end require transcription inputs")
	}
	if o.skipQuietSet && len(o.input) == 0 {
		return o, errors.New("--skip-quiet-imports requires transcription inputs")
	}
	if o.cookiesFile != "" && !o.cookieBrowserSet {
		o.cookieBrowser = "other"
		o.cookieBrowserSet = true
	}
	if o.cookieBrowserSet || o.cookieProfile != "" {
		if len(o.input) == 0 {
			return o, errors.New("browser sign-in options require transcription inputs")
		}
		if err := (media.URLAuth{Browser: o.cookieBrowser, Profile: o.cookieProfile, CookiesFile: o.cookiesFile}).Validate(); err != nil {
			return o, err
		}
	}
	if o.singleLine {
		o.textMode = "continuous"
	}
	commands := 0
	for _, enabled := range []bool{o.checkDependencies, o.checkModel, o.ensureModel, o.record, o.list, o.export != "", len(o.input) > 0} {
		if enabled {
			commands++
		}
	}
	if commands > 1 {
		return o, errors.New("choose one command: transcription inputs, --record, --list, --export, or a diagnostic")
	}
	if o.duration < 0 {
		return o, errors.New("--duration cannot be negative")
	}
	if o.duration > 0 && !o.record {
		return o, errors.New("--duration requires --record")
	}
	if o.language != "" && !models.ValidLanguage(o.language) {
		return o, errors.New("--language must be taglish, auto, en, or tl")
	}
	if o.source != "microphone" && o.source != "system" && o.source != "both" {
		return o, errors.New("--source must be microphone, system, or both")
	}
	if runtime.GOOS == "linux" && o.record && o.source != "microphone" {
		return o, errors.New("Linux recording supports --source microphone; transcribe a recorded monitor-source file for system audio")
	}
	if o.format != "txt" && o.format != "md" && o.format != "srt" && o.format != "vtt" && o.format != "json" {
		return o, errors.New("--format must be txt, md, srt, vtt, or json")
	}
	if o.textMode != "clean" && o.textMode != "continuous" && o.textMode != "timestamps" && o.textMode != "sources" {
		return o, errors.New("--text-mode must be clean, continuous, timestamps, or sources")
	}
	if len(o.input) > 1 && (o.format == "srt" || o.format == "vtt") {
		return o, errors.New("export subtitles for one input per invocation so timings belong to a single recording")
	}
	if o.modelID != "" {
		if _, ok := models.Lookup(o.modelID); !ok {
			return o, fmt.Errorf("unknown model ID %q", o.modelID)
		}
		if o.model != "" || !(o.ensureModel || o.checkModel || o.record || len(o.input) > 0) {
			return o, errors.New("--model-id requires transcription or a model check/download and cannot be combined with --model")
		}
	}
	if commands == 0 && (o.language != "" || o.promptSet || o.model != "" || o.singleLine) {
		return o, errors.New("recognition and text options require --transcribe, --record, --export, or a diagnostic command")
	}
	return o, nil
}

func run(args []string, stdout, stderr io.Writer) int {
	o, err := parseOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if o.model != "" {
		path, err := filepath.Abs(o.model)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := os.Setenv("TALA_MODEL_PATH", path); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	manager := models.Default()
	if o.output == "" {
		o.output = manager.OutputDir()
	}
	o.output, err = filepath.Abs(o.output)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := execute(ctx, o, manager, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, err)
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return 1
	}
	return 0
}

func writeJSON(w io.Writer, v any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(v)
}

func execute(ctx context.Context, o options, manager models.Manager, stdout, stderr io.Writer) error {
	if o.checkDependencies {
		report := media.Dependencies()
		if err := writeJSON(stdout, report); err != nil {
			return err
		}
		if !report.URL.Ready {
			return errors.New("some media tools are missing; microphone recording does not require them")
		}
		return nil
	}
	if o.checkModel || o.ensureModel {
		var status models.Status
		var err error
		switch {
		case o.modelID == "":
			status, err = manager.Ensure(ctx, o.ensureModel, nil)
		case o.ensureModel:
			status, err = manager.Download(ctx, manager.Settings(), o.modelID, nil)
		default:
			status, err = manager.DiscoverModel(ctx, manager.Settings(), o.modelID)
		}
		if writeErr := writeJSON(stdout, status); writeErr != nil {
			return writeErr
		}
		return err
	}
	store := &session.Store{Dir: o.output}
	if o.list {
		items, err := store.List()
		if err != nil {
			return err
		}
		return writeJSON(stdout, items)
	}
	if o.export != "" {
		return exportSession(store, o.export, o, stdout)
	}
	headless := o.record || len(o.input) > 0
	if !headless && runtime.GOOS == "linux" {
		return launchDesktop(o.output)
	}
	if err := os.MkdirAll(manager.Root(), 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(manager.Root(), "studio.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if !headless && runtime.GOOS == "darwin" {
			notifyExisting()
			return nil
		}
		return errors.New("another Tala process is using this workspace; close it or choose a separate TALA_DATA_DIR")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if !headless {
		return launchDesktop(o.output)
	}
	for _, input := range o.input {
		kind := inputKind(input)
		if err := media.Dependencies().Require(kind); err != nil {
			return err
		}
		if kind == "url" {
			if err := media.ValidateURL(input); err != nil {
				return err
			}
		}
	}
	setupLogging(o.output)
	settings := manager.Settings()
	if o.cookieBrowserSet {
		settings.URLAuth = media.URLAuth{Browser: o.cookieBrowser, Profile: o.cookieProfile, CookiesFile: o.cookiesFile}
	}
	if o.language != "" {
		settings.Language = o.language
	}
	if o.promptSet {
		settings.Prompt = o.prompt
	}
	if o.skipQuietSet {
		settings.SkipQuietImports = o.skipQuiet
	}
	c, err := app.NewHeadless(o.output, settings)
	if err != nil {
		return err
	}
	defer c.Close()
	progress := newCLIProgress(stderr, o.progress)
	defer progress.Close()
	// Consume bounded UI events even without a window; completion uses controller
	// snapshots so an overflow or a fast job cannot hide the terminal state.
	done := make(chan struct{})
	eventsDone := make(chan struct{})
	defer func() { close(done); <-eventsDone }()
	go func() {
		defer close(eventsDone)
		for {
			select {
			case event := <-c.Events:
				if event.Type == "error" {
					progress.Message(fmt.Sprint(event.Data))
				} else if event.Type == "activity" && o.progress != "never" {
					progress.Message(fmt.Sprint(event.Data))
				}
				c.AfterEvent()
			case <-done:
				return
			}
		}
	}()
	if err := waitModelFor(ctx, c, progress, o.modelID); err != nil {
		return err
	}
	if o.record {
		mode := map[string]audio.Mode{"microphone": audio.ModeMicrophone, "system": audio.ModeSystem, "both": audio.ModeBoth}[o.source]
		if err := c.SetMode(mode); err != nil {
			return err
		}
		if err := c.StartWithModel(o.modelID); err != nil {
			return err
		}
		id := c.LiveState().SessionID
		progress.Message("Recording. Press Ctrl+C to stop and save.")
		if err := waitRecording(ctx, c, o.duration, progress); err != nil {
			return err
		}
		return exportSession(store, id, o, stdout)
	}
	for i, input := range o.input {
		job, err := c.EnqueueWithOptions(input, inputKind(input), app.ImportOptions{Range: o.selection, ModelID: o.modelID})
		if err != nil {
			return err
		}
		progress.Message(fmt.Sprintf("Transcribing %d/%d: %s", i+1, len(o.input), input))
		id, err := waitJob(ctx, c, job.ID, progress)
		if err != nil {
			return err
		}
		if i > 0 && o.format != "json" {
			separator := "\n"
			if o.format == "txt" && o.textMode == "continuous" {
				separator = " "
			}
			if _, err := fmt.Fprint(stdout, separator); err != nil {
				return err
			}
		}
		if err := exportSession(store, id, o, stdout); err != nil {
			return err
		}
	}
	return nil
}

func inputKind(input string) string {
	if strings.HasPrefix(input, "https://") || strings.HasPrefix(input, "http://") {
		return "url"
	}
	return "file"
}
func waitModel(ctx context.Context, c *app.Controller, progress *cliProgress) error {
	return waitModelFor(ctx, c, progress, "")
}
func waitModelFor(ctx context.Context, c *app.Controller, progress *cliProgress, modelID string) error {
	started := time.Now()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		status := c.ModelStateFor(modelID)
		if status.State != "" && status.State != "checking" {
			if status.Path == "" {
				if modelID != "" {
					return fmt.Errorf("no verified model: %s; use --model-id %s --ensure-model", status.Detail, modelID)
				}
				return fmt.Errorf("no verified model: %s; use --model PATH or --ensure-model", status.Detail)
			}
			return nil
		}
		progress.Update(progressFrame{id: "model", state: "checking", label: "Checking model", step: int(time.Since(started) / (100 * time.Millisecond))})
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func waitJob(ctx context.Context, c *app.Controller, id string, progress *cliProgress) (string, error) {
	started := time.Now()
	last := app.Job{ID: id, State: "waiting"}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, job := range c.Jobs() {
			if job.ID != id {
				continue
			}
			last = job
			progress.Update(jobFrame(job, time.Since(started)))
			switch job.State {
			case "completed":
				return job.SessionID, nil
			case "failed":
				return "", fmt.Errorf("transcription failed (partial session %s): %s", job.SessionID, job.Error)
			case "cancelled":
				return "", context.Canceled
			}
		}
		select {
		case <-ctx.Done():
			_ = c.CancelJob(id)
			last.State = "cancelled"
			progress.Update(jobFrame(last, time.Since(started)))
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}
func waitRecording(ctx context.Context, c *app.Controller, duration time.Duration, progress *cliProgress) error {
	var timer *time.Timer
	var limit <-chan time.Time
	var recordingStarted time.Time
	var recorded time.Duration
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	stopping := false
	for {
		live := c.LiveState()
		if live.State == "recording" && recordingStarted.IsZero() {
			recordingStarted = time.Now()
		}
		if !recordingStarted.IsZero() && !stopping {
			recorded = time.Since(recordingStarted)
		}
		progress.Update(recordingFrame(live.State, recorded, duration))
		if !live.Active {
			if live.Error != "" {
				return errors.New(live.Error)
			}
			return nil
		}
		if !stopping && timer == nil && duration > 0 && live.State == "recording" {
			timer = time.NewTimer(duration)
			limit = timer.C
		}
		select {
		case <-ctx.Done():
			if !stopping {
				if !recordingStarted.IsZero() {
					recorded = time.Since(recordingStarted)
				}
				c.Stop()
				stopping = true
			}
			ctx = context.Background()
		case <-limit:
			if !recordingStarted.IsZero() {
				recorded = time.Since(recordingStarted)
			}
			c.Stop()
			stopping = true
			limit = nil
		case <-ticker.C:
		}
	}
}
func exportSession(store *session.Store, id string, o options, stdout io.Writer) error {
	v, err := store.Get(id)
	if err != nil {
		return err
	}
	if o.format == "json" {
		return writeJSON(stdout, v)
	}
	text, err := session.Export(v, o.format, o.textMode)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, text)
	return err
}
