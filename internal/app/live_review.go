package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"tala/internal/models"
	"tala/internal/recording"
	"tala/internal/transcriber"
)

const reviewActive = "Local transcript review"

func (c *Controller) ConfigureLiveReview(enabled bool, modelID string) error {
	spec, ok := models.LookupReview(modelID)
	if !ok {
		return errors.New("choose a live review model")
	}
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.live.Active {
		return errors.New("stop recording before changing live review")
	}
	if enabled && modelID == models.CleanupID && cleanupExecutable(c.Models.Exe) == "" {
		return errors.New("contextual cleanup requires the bundled Tala app")
	}
	if enabled && c.modelStates[modelID].Path == "" {
		return fmt.Errorf("download %s before enabling live review", spec.Name)
	}
	settings := c.Models.Settings()
	settings.LiveReview = enabled
	settings.LiveReviewModel = modelID
	if err := c.Models.Save(settings); err != nil {
		return err
	}
	c.emitLocked("settings", settings)
	return nil
}

type reviewWord struct {
	start, end int
	normalized string
	score      float32
}

var reviewWordPattern = regexp.MustCompile(`[\pL\pN][\pL\pN\pM]*(?:['’.-][\pL\pN][\pL\pN\pM]*)*`)

func reviewWords(seg transcriber.Segment) []reviewWord {
	if len(seg.TokenScores) == 0 {
		return nil
	}
	var words []reviewWord
	token := 0
	for _, match := range reviewWordPattern.FindAllStringIndex(seg.Text, -1) {
		start, end := match[0], match[1]
		for token < len(seg.TokenScores) && seg.TokenScores[token].End <= start {
			token++
		}
		score := float32(1)
		if token == len(seg.TokenScores) || seg.TokenScores[token].Start >= end {
			return nil
		}
		for i := token; i < len(seg.TokenScores) && seg.TokenScores[i].Start < end; i++ {
			score = min(score, seg.TokenScores[i].Probability)
		}
		word := strings.ToLower(strings.ReplaceAll(seg.Text[start:end], "’", "'"))
		words = append(words, reviewWord{start, end, word, score})
	}
	return words
}

const (
	maxDraftWordScore  = 0.90
	minReviewWordScore = 0.78
	minScoreGain       = 0.15
)

func needsReview(words []reviewWord) bool {
	for _, word := range words {
		if word.score <= maxDraftWordScore {
			return true
		}
	}
	return false
}

// Prefer a local substitution over replacing a line with a different reading
// of the whole clip. Compare scores only for words involved in the change.
func reviewAlternative(draft string, before []reviewWord, candidate transcriber.Segment) string {
	after := reviewWords(candidate)
	first := 0
	for first < min(len(before), len(after)) && before[first].normalized == after[first].normalized {
		first++
	}
	oldEnd, newEnd := len(before), len(after)
	for oldEnd > first && newEnd > first && before[oldEnd-1].normalized == after[newEnd-1].normalized {
		oldEnd--
		newEnd--
	}
	oldCount, newCount := oldEnd-first, newEnd-first
	if oldCount < 1 || oldCount > 2 || newCount < 1 || newCount > 2 {
		return ""
	}
	shared := len(before) - oldCount
	oldScore, newScore := float32(1), float32(1)
	for _, word := range before[first:oldEnd] {
		oldScore = min(oldScore, word.score)
	}
	for _, word := range after[first:newEnd] {
		newScore = min(newScore, word.score)
	}
	if len(before) == 1 && len(after) == 1 {
		if oldScore > 0.45 || newScore < 0.95 {
			return ""
		}
	} else if shared < max(oldCount, newCount) || oldScore > maxDraftWordScore ||
		newScore < minReviewWordScore || newScore-oldScore < minScoreGain {
		return ""
	}
	replacement := candidate.Text[after[first].start:after[newEnd-1].end]
	return draft[:before[first].start] + replacement + draft[before[oldEnd-1].end:]
}

type reviewJob struct {
	draft   transcriber.Segment
	words   []reviewWord
	id      string
	samples []float32
	context []string
	expires time.Time
}

type liveReview struct {
	jobs     chan reviewJob
	cancel   context.CancelFunc
	done     chan struct{}
	textOnly bool
	recent   []transcriber.Segment
}

func (c *Controller) startLiveReview(ctx context.Context, id string, primary models.Status) *liveReview {
	settings := c.recognitionSettings()
	if c.headless || !settings.LiveReview || ctx.Err() != nil {
		return nil
	}
	status := c.ModelStateFor(settings.ReviewModelID())
	if status.Path == "" {
		c.setReviewStatus(id, "Download the live review model in Settings")
		return nil
	}
	if status.ID == models.CleanupID {
		runtime, err := startCleanupRuntime(ctx, cleanupExecutable(c.Models.Exe), status.Path)
		if err != nil {
			c.setReviewStatus(id, "Local cleanup unavailable · recording continues")
			return nil
		}
		return c.runReviewWorker(ctx, id, true, func(ctx context.Context, job reviewJob) (string, error) {
			return runtime.suggest(ctx, job.draft.Text, job.context)
		}, runtime.close)
	}

	// Only the review worker owns its second model. If the two selections
	// resolve to the same file, reuse the existing model when it is idle.
	var engine *transcriber.Transcriber
	decode := func(ctx context.Context, samples []float32) ([]transcriber.Segment, error) {
		if status.Path == primary.Path {
			if !c.engineMu.TryLock() {
				return nil, nil
			}
			defer c.engineMu.Unlock()
			return c.engine.TranscribeWithScores(ctx, samples)
		}
		if engine == nil {
			var err error
			engine, err = transcriber.New(transcriber.Config{
				ModelPath: status.Path, Language: settings.Language, Prompt: settings.Prompt, Threads: 2,
			})
			if err != nil {
				return nil, err
			}
		}
		return engine.TranscribeWithScores(ctx, samples)
	}
	return c.runReviewWorker(ctx, id, false, audioReview(decode), func() {
		if engine != nil {
			_ = engine.Close()
		}
	})
}

func audioReview(decode recording.Decoder) func(context.Context, reviewJob) (string, error) {
	return func(ctx context.Context, job reviewJob) (string, error) {
		parts, err := decode(ctx, job.samples)
		if err != nil || len(parts) != 1 {
			return "", err
		}
		return reviewAlternative(job.draft.Text, job.words, parts[0]), nil
	}
}

func (c *Controller) runReviewWorker(ctx context.Context, id string, textOnly bool, suggest func(context.Context, reviewJob) (string, error), cleanup func()) *liveReview {
	ctx, cancel := context.WithCancel(ctx)
	r := &liveReview{jobs: make(chan reviewJob, 1), cancel: cancel, done: make(chan struct{}), textOnly: textOnly}
	c.setReviewStatus(id, reviewActive)
	go func() {
		defer close(r.done)
		defer cleanup()
		for ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case job := <-r.jobs:
				if !time.Now().Before(job.expires) {
					continue
				}
				requestCtx, stop := context.WithDeadline(ctx, job.expires)
				text, err := suggest(requestCtx, job)
				stop()
				if ctx.Err() != nil {
					return
				}
				if err != nil {
					if errors.Is(err, context.DeadlineExceeded) {
						continue
					}
					c.setReviewStatus(id, "Local review unavailable · recording continues")
					return
				}
				if text != "" && time.Now().Before(job.expires) {
					c.publishReviewSuggestion(id, job.id, text)
				}
			}
		}
	}()
	return r
}

// Review can fall behind without blocking transcript delivery or capture.
func (r *liveReview) offer(id string, draft transcriber.Segment, samples []float32) {
	if r == nil {
		return
	}
	if r.textOnly {
		r.offerText(id, draft)
		return
	}
	if len(samples) < transcriber.SampleRate/5 {
		return
	}
	words := reviewWords(draft)
	if !needsReview(words) {
		return
	}
	// Allocate padding separately so the review never modifies recording PCM.
	if len(samples) < transcriber.SampleRate {
		padded := make([]float32, transcriber.SampleRate)
		copy(padded, samples)
		samples = padded
	}
	r.enqueue(reviewJob{draft: draft, words: words, id: id, samples: samples, expires: time.Now().Add(6 * time.Second)})
}

func (r *liveReview) offerText(id string, draft transcriber.Segment) {
	if len(draft.Text) > 1200 {
		return
	}
	var preceding []string
	for _, prior := range r.recent {
		if gap := draft.Start - prior.End; gap >= 0 && gap <= 35*time.Second {
			preceding = append(preceding, prior.Text)
		}
	}
	if len(draft.Text) <= 400 {
		r.recent = append(r.recent, draft)
		if len(r.recent) > 3 {
			r.recent = r.recent[1:]
		}
	}
	r.enqueue(reviewJob{draft: draft, context: preceding, id: id, expires: time.Now().Add(25 * time.Second)})
}

func (r *liveReview) enqueue(job reviewJob) {
	select {
	case <-r.done:
	case r.jobs <- job:
	default:
	}
}

func (r *liveReview) close() {
	if r != nil {
		r.cancel()
		<-r.done
	}
}

func (c *Controller) setReviewStatus(id, status string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.live.Active && c.live.SessionID == id && c.live.Review != status {
		c.live.Review = status
		c.emitLocked("live", c.live)
	}
}

type suggestionEvent struct {
	ID       string  `json:"sessionId"`
	Segment  string  `json:"segmentId"`
	Text     *string `json:"text"`
	Accepted bool    `json:"accepted,omitempty"`
}

func (c *Controller) publishReviewSuggestion(id, segment, text string) {
	c.editsMu.Lock()
	defer c.editsMu.Unlock()
	added, err := c.Store.Suggest(id, segment, text)
	if err != nil {
		c.setReviewStatus(id, "Could not save a review suggestion · recording continues")
	} else if added {
		c.Emit("reviewSuggestion", suggestionEvent{ID: id, Segment: segment, Text: &text})
	}
}

func (c *Controller) ResolveReviewSuggestion(id, segment string, accept bool) error {
	c.editsMu.Lock()
	defer c.editsMu.Unlock()
	text, err := c.Store.ResolveSuggestion(id, segment, accept)
	if err == nil {
		c.Emit("reviewSuggestion", suggestionEvent{ID: id, Segment: segment, Text: text, Accepted: accept})
	}
	return err
}

func (c *Controller) CorrectSegment(id, segment string, text *string) error {
	c.editsMu.Lock()
	defer c.editsMu.Unlock()
	err := c.Store.Correct(id, segment, text)
	if err == nil {
		c.Emit("correction", struct {
			ID, Segment string
			Text        *string
		}{id, segment, text})
	}
	return err
}
