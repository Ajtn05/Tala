package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"tala/internal/session"
)

func TestMeetingLocalSummaryIntegration(t *testing.T) {
	model, executable := os.Getenv("TALA_TEST_SUMMARY_MODEL"), os.Getenv("TALA_TEST_SUMMARY_RUNTIME")
	if model == "" || executable == "" {
		t.Skip("set TALA_TEST_SUMMARY_MODEL and TALA_TEST_SUMMARY_RUNTIME to exercise local inference")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	runtime, err := startLocalRuntime(ctx, executable, model, 16384)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.close()
	const notes = "## Budget first\n- Budget: check status, huwag muna mag-assume.\n\n## Project Cedar launch\n- Need the date and Mira's checklist commitment."
	v := session.Session{Meeting: &session.Meeting{Notes: notes}, Segments: []session.Segment{
		{Text: "We decided to launch Project Cedar on Wednesday.", Start: 1, End: 3, Source: "System Audio"},
		{Text: "Mira will send the launch checklist by Friday.", Start: 4, End: 7, Source: "Microphone"},
		{Text: "We have not approved the budget.", Start: 8, End: 10, Source: "System Audio"},
	}}
	summary, err := enhanceMeeting(ctx, v, func(ctx context.Context, source string) (noteEnhancement, error) {
		return meetingCompletion(runtime.completeMeeting).enhanceNotes(ctx, enhancementInstructions, source)
	})
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(summary)
	for _, want := range []string{"## Budget first", "- Budget: check status, huwag muna mag-assume.", "## Project Cedar launch", "cedar", "wednesday", "mira", "checklist", "friday"} {
		if !strings.Contains(lower, strings.ToLower(want)) {
			t.Errorf("grounded summary missing %q: %s", want, summary)
		}
	}
	if !strings.Contains(lower, "not approved") && !strings.Contains(lower, "not been approved") && !strings.Contains(lower, "unapproved") && !strings.Contains(lower, "hindi pa approved") && !strings.Contains(lower, "hindi pa naaprubahan") {
		t.Errorf("enhancement lost the budget negation: %s", summary)
	}
	if strings.Index(summary, "## Budget first") > strings.Index(summary, "## Project Cedar launch") || strings.Contains(summary, "## Overview") || strings.Contains(summary, "## Key points") || v.Meeting.Notes != notes {
		t.Fatalf("enhancement replaced the user's outline: %s", summary)
	}
	t.Log(summary)
}
