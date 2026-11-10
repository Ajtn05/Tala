package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"tala/internal/models"
	"tala/internal/session"
	"tala/internal/transcriber"
)

func scoredReviewSegment(text string, scores ...float32) transcriber.Segment {
	seg := transcriber.Segment{Text: text}
	for i, match := range reviewWordPattern.FindAllStringIndex(text, -1) {
		seg.TokenScores = append(seg.TokenScores, transcriber.TokenScore{
			Start: match[0], End: match[1], Probability: scores[i],
		})
	}
	return seg
}

func TestLocalReviewSavesAnExplicitSuggestionAfterDraft(t *testing.T) {
	c := controller(t)
	c.Events = make(chan Event, 10)
	entry, err := c.Store.Create("live", "Live review test", "Microphone")
	if err != nil {
		t.Fatal(err)
	}
	c.live = Live{Active: true, SessionID: entry.ID}
	calls := make(chan []float32, 1)
	release := make(chan struct{})
	review := c.runReviewWorker(t.Context(), entry.ID, false, audioReview(func(ctx context.Context, pcm []float32) ([]transcriber.Segment, error) {
		calls <- pcm
		select {
		case <-release:
			return []transcriber.Segment{scoredReviewSegment("Clear the cache first", 0.99, 0.99, 0.97, 0.99)}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}), func() {})
	defer review.close()

	draft := scoredReviewSegment("Clear the cash first.", 0.99, 0.99, 0.45, 0.99)
	draft.Source, draft.End = "Microphone", time.Second
	seg, err := c.append(entry.ID, draft)
	if err != nil {
		t.Fatal(err)
	}
	samples := make([]float32, transcriber.SampleRate)
	samples[100] = 0.25
	review.offer(seg.ID, draft, samples)
	select {
	case got := <-calls:
		if len(got) != len(samples) || got[100] != 0.25 {
			t.Fatal("review did not receive the recorded PCM")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("local decoder was never called")
	}
	before, err := c.Store.Get(entry.ID)
	if err != nil || len(before.Segments) != 1 || len(before.Suggestions) != 0 {
		t.Fatalf("draft was not saved before the review finished: %+v, %v", before, err)
	}
	close(release)
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-c.Events:
			if event.Type != "reviewSuggestion" {
				continue
			}
		case <-deadline:
			t.Fatal("suggestion was never published")
		}
		break
	}
	pending, err := c.Store.Get(entry.ID)
	if err != nil || pending.Suggestions[seg.ID] != "Clear the cache first." || session.Text(pending, seg) != seg.Text {
		t.Fatalf("suggestion replaced the original or was lost: %+v, %v", pending, err)
	}
	if err := c.ResolveReviewSuggestion(entry.ID, seg.ID, true); err != nil {
		t.Fatal(err)
	}
	accepted, err := c.Store.Get(entry.ID)
	if err != nil || len(accepted.Suggestions) != 0 || session.Text(accepted, seg) != "Clear the cache first." ||
		len(accepted.Segments) != 1 || accepted.Segments[0].Text != seg.Text {
		t.Fatalf("accept did not save a reversible correction: %+v, %v", accepted, err)
	}
}

func TestLocalReviewRequiresInstalledModel(t *testing.T) {
	t.Setenv("TALA_DATA_DIR", t.TempDir())
	c := controller(t)
	if err := c.ConfigureLiveReview(true, models.RecommendedID); err == nil || !strings.Contains(err.Error(), "download") {
		t.Fatalf("enabled review without a model: %v", err)
	}
	c.modelStates = map[string]models.Status{"small-q5": {Path: "/installed/small.bin"}}
	if err := c.ConfigureLiveReview(true, "small-q5"); err != nil {
		t.Fatal(err)
	}
	if settings := c.Models.Settings(); !settings.LiveReview || settings.ReviewModelID() != "small-q5" {
		t.Fatalf("live review model was not saved: %+v", settings)
	}
}

func TestLocalReviewKeepsUnsupportedChangesOut(t *testing.T) {
	draft := scoredReviewSegment("Please send fifteen invoices.", 0.98, 0.99, 0.7, 0.99)
	cases := []struct {
		candidate transcriber.Segment
		want      string
	}{
		{scoredReviewSegment("Please send fifty invoices", 0.98, 0.99, 0.97, 0.99), "Please send fifty invoices."},
		{scoredReviewSegment("Please send fifty invoices", 0.98, 0.99, 0.76, 0.99), ""},
		{scoredReviewSegment("Please pay the bills", 0.98, 0.99, 0.98, 0.99), ""},
		{scoredReviewSegment("Please send fifteen invoices!", 0.98, 0.99, 0.98, 0.99), ""},
		{scoredReviewSegment("Please send invoices", 0.98, 0.99, 0.99), ""},
	}
	for _, tc := range cases {
		if got := reviewAlternative(draft.Text, reviewWords(draft), tc.candidate); got != tc.want {
			t.Errorf("review with %q = %q, want %q", tc.candidate.Text, got, tc.want)
		}
	}
}
