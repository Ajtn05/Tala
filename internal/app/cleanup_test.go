package app

import (
	"strings"
	"testing"
	"time"

	"tala/internal/transcriber"
)

func TestCleanupKeepsEditsTiedToTheTargetLine(t *testing.T) {
	const raw = "Piling ko nandito siya. Pag-ala-gala lang."
	for _, tc := range []struct{ revised, want string }{
		{"Feeling ko nandito siya. Pagala-gala lang.", "Feeling ko nandito siya. Pagala-gala lang."},
		{"I think they are here and just wandering around.", ""},
		{"Ha? Ano? Kailin ko! " + raw, ""},
		{raw, ""},
		{"", ""},
	} {
		if got := validatedCleanup(raw, tc.revised); got != tc.want {
			t.Errorf("cleanup %q = %q, want %q", tc.revised, got, tc.want)
		}
	}
}

func TestCleanupAppliesOnlyUniqueVerbatimSpans(t *testing.T) {
	const raw = "Piling ko nandito siya. Pag-ala-gala lang. I can't leave."
	edits := []cleanupEdit{
		{Before: "Pag-ala-gala", After: "Pagala-gala"},
		{Before: "Piling ko", After: "Feeling ko"},
		{Before: "can", After: "could"},
		{Before: "Text from context", After: "Do not copy"},
	}
	if got := applyCleanupEdits(raw, edits); got != "Feeling ko nandito siya. Pagala-gala lang. I can't leave." {
		t.Fatalf("unexpected suggestion: %q", got)
	}
	if got := applyCleanupEdits(raw, []cleanupEdit{{"Piling ko", "Feeling ko"}, {"Piling", "I think"}}); got != "" {
		t.Fatalf("accepted overlapping replacements: %q", got)
	}
}

func TestCleanupQueuesTextAndNearbyContextWithoutAudio(t *testing.T) {
	r := &liveReview{textOnly: true, jobs: make(chan reviewJob, 4), done: make(chan struct{})}
	r.offer("first", transcriber.Segment{Text: "Ha?", End: time.Second}, nil)
	r.offer("second", transcriber.Segment{Text: "Ano?", Start: time.Second, End: 2 * time.Second}, nil)
	r.offer("third", transcriber.Segment{Text: "Piling ko nandito siya.", Start: 3 * time.Second, End: 4 * time.Second}, nil)
	<-r.jobs
	<-r.jobs
	job := <-r.jobs
	if job.id != "third" || len(job.samples) != 0 || strings.Join(job.context, " ") != "Ha? Ano?" {
		t.Fatalf("wrong cleanup input: %+v", job)
	}
}
